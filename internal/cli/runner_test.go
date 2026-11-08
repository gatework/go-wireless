package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gatework/go-wireless/internal/bootstrap"

	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/netdisco"
	"github.com/gatework/go-net-connector/storage"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
	"github.com/gatework/go-wireless/report"
)

func panicFactory(context.Context, wireless.Record, connector.ConnectionConfig) (bootstrap.Connection, error) {
	panic("unexpected device factory")
}

type fixtureSession struct {
	mu       sync.Mutex
	commands []string
	platform string
	body     string
}

func (s *fixtureSession) State(context.Context) (bool, error) { return true, nil }
func (s *fixtureSession) Execute(_ context.Context, command string) (collect.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, command)
	body := ""
	switch command {
	case "screen-length disable", "screen-length 0 temporary":
	case "display wlan ap all verbose":
		body = "Total number of APs : 0\n"
	case "display ap all":
		body = "ID MAC Name Group IP Type State STA Uptime\nTotal: 0\n"
	case "display station all":
		body = "STA MAC AP ID Ap name Rf/WLAN\nTotal: 0\n"
	case "display current-configuration":
		body = "sysname WLC\nreturn\n"
	default:
		return collect.Receipt{}, errors.New("private unexpected command")
	}
	if s.body != "" && command == "display wlan ap all verbose" {
		body = s.body
	}
	return collect.NewReceipt(collect.ReceiptOptions{Attempted: true, Command: command, Raw: command + "\n" + body + "<WLC>", Body: body, Steps: 1, Success: true}), nil
}
func fixtureFactory(closed *atomic.Int32) bootstrap.SessionFactory {
	return func(_ context.Context, d wireless.Record, cfg connector.ConnectionConfig) (bootstrap.Connection, error) {
		platform := StringValue(d["platform"])
		return bootstrap.Connection{Platform: platform, Open: func(ctx context.Context, use func(collect.Session) error) error {
			defer closed.Add(1)
			return use(&fixtureSession{platform: platform})
		}}, nil
	}
}
func runFixture(t *testing.T, body string, args []string, change func(*Dependencies)) (int, map[string]any, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "wireless.yml"), "directory: reports\ndimensions: [access_points]\n"+body)
	var out, err bytes.Buffer
	var closed atomic.Int32
	d := Dependencies{Environment: map[string]string{"NC_DEVICE_USERNAME": "fixture"}, Input: panicReader{}, Output: &out, Error: &err, Discovery: discovery(dir), SessionFactory: fixtureFactory(&closed)}
	if change != nil {
		change(&d)
	}
	code := Run(context.Background(), args, d)
	var summary map[string]any
	if out.Len() > 0 && strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		if e := json.Unmarshal(out.Bytes(), &summary); e != nil {
			t.Fatal(e, out.String())
		}
	}
	return code, summary, out.String(), err.String(), dir
}
func TestStaticInventoryPreviewAndSelectors(t *testing.T) {
	body := "devices:\n - {host: '192.0.2.10', name: WLC-A}\n - {host: '192.0.2.11', name: Spare-WLC}\n - {host: '192.0.3.10', name: WLC-B}\n"
	code, _, out, _, dir := runFixture(t, body, []string{"--plan", "--host", "192.0.2.0/24", "--name", "wlc", "--exclude-name", "spare", "--json"}, func(d *Dependencies) {
		d.Environment = map[string]string{}
		d.SessionFactory = panicFactory
		d.PrepareResources = func(context.Context, int) error { panic("unexpected resource preparation") }
	})
	if code != 0 {
		t.Fatal(code, out)
	}
	var rows []wireless.Record
	if e := json.Unmarshal([]byte(out), &rows); e != nil {
		t.Fatal(e, out)
	}
	if len(rows) != 1 || rows[0]["host"] != "192.0.2.10" || rows[0]["model"] != nil {
		t.Fatal(rows)
	}
	if _, e := os.Stat(filepath.Join(dir, "reports")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("preview created reports")
	}
}
func TestCollectBothPlatformsPrivateReportsAndReuse(t *testing.T) {
	code, summary, out, err, dir := runFixture(t, "devices: [{host: '192.0.2.10', name: H3C}, {host: '192.0.2.11', name: HW, platform: huawei}]\n", []string{"--json"}, nil)
	if code != 0 || summary["status"] != "complete" {
		t.Fatal(code, out, err)
	}
	if strings.Contains(out, "配置：") || !strings.Contains(err, "批次结束：成功 2") {
		t.Fatal(out, err)
	}
	batch := summary["directory"].(string)
	entries, e := os.ReadDir(filepath.Join(batch, "devices"))
	if e != nil || len(entries) != 4 {
		t.Fatal(entries, e)
	}
	for _, entry := range entries {
		info, _ := entry.Info()
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
		if strings.HasSuffix(entry.Name(), ".json") {
			b, _ := os.ReadFile(filepath.Join(batch, "devices", entry.Name()))
			var doc map[string]any
			_ = json.Unmarshal(b, &doc)
			if has(doc, "report_saved") || doc["snapshot"] == nil {
				t.Fatal(string(b))
			}
		}
	}
	for _, path := range []string{filepath.Join(dir, "reports"), batch, filepath.Join(batch, "devices")} {
		s, _ := os.Lstat(path)
		if s.Mode().Perm() != 0700 {
			t.Fatal(path, s.Mode())
		}
	}
	b, _ := os.ReadFile(filepath.Join(batch, "summary.json"))
	if strings.TrimSpace(string(b)) != strings.TrimSpace(out) {
		t.Fatal("stdout differs from durable summary")
	}
}
func TestCredentialAndResourcePreflightBeforeReports(t *testing.T) {
	for _, stage := range []string{"credentials", "newline", "resources"} {
		t.Run(stage, func(t *testing.T) {
			var calls atomic.Int32
			code, _, out, err, dir := runFixture(t, "devices: [{host: '192.0.2.10'}, {host: '192.0.2.11', platform: huawei}]\n", []string{"--json"}, func(d *Dependencies) {
				d.SessionFactory = func(context.Context, wireless.Record, connector.ConnectionConfig) (bootstrap.Connection, error) {
					calls.Add(1)
					return bootstrap.Connection{}, nil
				}
				switch stage {
				case "credentials":
					d.Environment = map[string]string{"NC_H3C_WIRELESS_USERNAME": "h3c"}
				case "newline":
					d.Environment = map[string]string{"NC_DEVICE_USERNAME": "private\nname"}
				case "resources":
					d.PrepareResources = func(_ context.Context, n int) error {
						if n != 2 {
							t.Errorf("reserved %d", n)
						}
						return errors.New("private resource message")
					}
				}
			})
			if code != 2 || calls.Load() != 0 || out != "" || strings.Contains(err, "private") {
				t.Fatal(code, calls.Load(), out, err)
			}
			if _, e := os.Stat(filepath.Join(dir, "reports")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("preflight created reports")
			}
		})
	}
}
func TestHuaweiDefaultsAndAllCredentialsSnapshot(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "wireless.yml"), "directory: reports\ndevices: [{host: '192.0.2.10', platform: huawei}]\n")
	var out, diagnostic bytes.Buffer
	var commands []string
	code := Run(context.Background(), []string{"--json"}, Dependencies{Environment: map[string]string{"NC_DEVICE_USERNAME": "generic", "NC_DEVICE_PASSWORD": "generic-secret", "NC_HUAWEI_USERNAME": "vendor-user", "NC_HUAWEI_PASSWORD": "vendor-secret"}, Input: panicReader{}, Output: &out, Error: &diagnostic, Discovery: discovery(dir), SessionFactory: func(_ context.Context, d wireless.Record, c connector.ConnectionConfig) (bootstrap.Connection, error) {
		if c.Username != "vendor-user" || c.Password != "vendor-secret" {
			t.Error("vendor credentials not resolved")
		}
		return bootstrap.Connection{Platform: "huawei", Open: func(ctx context.Context, use func(collect.Session) error) error {
			s := &fixtureSession{platform: "huawei"}
			err := use(s)
			commands = s.commands
			return err
		}}, nil
	}})
	if code != 0 || strings.Join(commands, "|") != "screen-length 0 temporary|display ap all|display station all" {
		t.Fatal(code, commands, out.String(), diagnostic.String())
	}
	if strings.Contains(out.String()+diagnostic.String(), "secret") {
		t.Fatal("credential disclosure")
	}
}

func TestFactoryFailureRetainsNullSnapshotAndSafeError(t *testing.T) {
	code, s, out, err, _ := runFixture(t, "devices: [{host: '192.0.2.10'}]\n", []string{"--json"}, func(d *Dependencies) {
		d.SessionFactory = func(context.Context, wireless.Record, connector.ConnectionConfig) (bootstrap.Connection, error) {
			return bootstrap.Connection{}, errors.New("private device error")
		}
	})
	if code != 1 || s["status"] != "incomplete" || strings.Contains(out+err, "private device") {
		t.Fatal(code, out, err)
	}
	paths, _ := filepath.Glob(filepath.Join(s["directory"].(string), "devices/*.json"))
	b, _ := os.ReadFile(paths[0])
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	if doc["snapshot"] != nil || doc["device"].(map[string]any)["model"] != nil || has(doc, "report_saved") {
		t.Fatal(string(b))
	}
}
func TestStoreFailureCancelsSiblingAndRetainsStartedEvidence(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "wireless.yml"), "directory: reports\nparallel: 2\ndimensions: [access_points]\ndevices: [{host: '192.0.2.10'}, {host: '192.0.2.11'}, {host: '192.0.2.12'}]\n")
	var out, err bytes.Buffer
	secondStarted := make(chan struct{})
	var closed atomic.Int32
	code := Run(context.Background(), []string{"--json"}, Dependencies{Environment: map[string]string{"NC_DEVICE_USERNAME": "fixture"}, Input: panicReader{}, Output: &out, Error: &err, Discovery: discovery(dir), SessionFactory: func(_ context.Context, d wireless.Record, _ connector.ConnectionConfig) (bootstrap.Connection, error) {
		return bootstrap.Connection{Platform: "h3c", Open: func(ctx context.Context, use func(collect.Session) error) error {
			defer closed.Add(1)
			if d["host"] == "192.0.2.11" {
				close(secondStarted)
				<-ctx.Done()
				return ctx.Err()
			}
			<-secondStarted
			return use(&fixtureSession{})
		}}, nil
	}, ReportOptions: report.Options{Write: func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
		if strings.HasSuffix(path, ".10.txt") {
			return storage.Receipt{Path: path, State: "not_committed", Phase: "rename"}, errors.New("private storage")
		}
		return storage.Write(ctx, path, b)
	}}})
	if code != 2 || closed.Load() != 2 {
		t.Fatal(code, closed.Load(), err.String())
	}
	var summary map[string]any
	if e := json.Unmarshal(out.Bytes(), &summary); e != nil {
		t.Fatal(e, out.String())
	}
	if summary["status"] != "incomplete" || summary["completed"] != float64(2) || len(summary["pending"].([]any)) != 1 {
		t.Fatal(summary)
	}
	if strings.Contains(out.String()+err.String(), "private storage") {
		t.Fatal("leak")
	}
}
func TestUserCancellationJoinsAndKeeps130OnSummaryFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "summary-failure"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "wireless.yml"), "directory: reports\nparallel: 1\ndimensions: [access_points]\ndevices: [{host: '192.0.2.10'}, {host: '192.0.2.11'}]\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out, err bytes.Buffer
			var closed atomic.Int32
			code := Run(ctx, []string{"--json"}, Dependencies{Environment: map[string]string{"NC_DEVICE_USERNAME": "fixture"}, Input: panicReader{}, Output: &out, Error: &err, Discovery: discovery(dir), SessionFactory: func(context.Context, wireless.Record, connector.ConnectionConfig) (bootstrap.Connection, error) {
				return bootstrap.Connection{Platform: "h3c", Open: func(ctx context.Context, use func(collect.Session) error) error {
					defer closed.Add(1)
					e := use(&fixtureSession{})
					cancel()
					return e
				}}, nil
			}, ReportOptions: report.Options{Write: func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
				if fail && strings.HasSuffix(path, "summary.txt") {
					return storage.Receipt{Phase: "write"}, io.ErrClosedPipe
				}
				return storage.Write(ctx, path, b)
			}}})
			if code != 130 || closed.Load() != 1 {
				t.Fatal(code, closed.Load(), out.String(), err.String())
			}
			paths, _ := filepath.Glob(filepath.Join(dir, "reports/*/summary.json"))
			if len(paths) != 1 {
				t.Fatal(paths)
			}
			b, _ := os.ReadFile(paths[0])
			var summary map[string]any
			_ = json.Unmarshal(b, &summary)
			if summary["status"] != "interrupted" || len(summary["pending"].([]any)) != 1 {
				t.Fatal(string(b))
			}
			if fail && out.Len() != 0 {
				t.Fatal("published stdout after summary error")
			}
		})
	}
}

type failingWriter struct {
	needle string
	b      bytes.Buffer
	always bool
}

func (w *failingWriter) Write(b []byte) (int, error) {
	if w.always || bytes.Contains(b, []byte(w.needle)) {
		return 0, errors.New("private terminal error")
	}
	return w.b.Write(b)
}
func TestProgressOutputFailuresBecomeDurableWithoutReplay(t *testing.T) {
	for _, stage := range []string{"配置：", "批次结束："} {
		t.Run(stage, func(t *testing.T) {
			var calls atomic.Int32
			writer := &failingWriter{needle: stage}
			code, s, out, _, _ := runFixture(t, "devices: [{host: '192.0.2.10'}]\n", []string{"--json"}, func(d *Dependencies) {
				d.Error = writer
				f := d.SessionFactory
				d.SessionFactory = func(ctx context.Context, dev wireless.Record, c connector.ConnectionConfig) (bootstrap.Connection, error) {
					calls.Add(1)
					return f(ctx, dev, c)
				}
			})
			if code != 1 || s["output_error"] == nil || s["status"] != "incomplete" || calls.Load() != 1 {
				t.Fatal(code, s, calls.Load())
			}
			b, _ := os.ReadFile(filepath.Join(s["directory"].(string), "summary.json"))
			if strings.TrimSpace(out) != strings.TrimSpace(string(b)) {
				t.Fatal("durable mismatch")
			}
		})
	}
}
func TestQuietAndTerminalProgress(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(map[bool]string{false: "tty", true: "quiet"}[quiet], func(t *testing.T) {
			args := []string{"--json"}
			if quiet {
				args = append(args, "--quiet")
			}
			code, _, out, err, _ := runFixture(t, "devices: [{host: '192.0.2.10'}]\n", args, func(d *Dependencies) { d.Terminal = func(any) bool { return true } })
			if code != 0 || !json.Valid([]byte(out)) {
				t.Fatal(code, out, err)
			}
			if quiet && err != "" {
				t.Fatal(err)
			}
			if !quiet && (!strings.Contains(err, "\x1b[2K") || !strings.Contains(err, "1/1 100%")) {
				t.Fatal(err)
			}
		})
	}
}
func TestNativeInventoryRulesFilteringAndExternalSourceInjection(t *testing.T) {
	body := "netdisco: {source: http, cache_enabled: false}\ninventory: {exclude_names: [spare]}\n"
	code, _, out, err, _ := runFixture(t, body, []string{"--plan", "--json"}, func(d *Dependencies) {
		d.SessionFactory = panicFactory
		d.InventoryFetch = func(ctx context.Context, _ bootstrap.InventorySettings) (netdisco.CacheResult, error) {
			return netdisco.CacheResult{Rows: []inventory.Device{{IP: "192.0.2.10", Name: "WLC", Vendor: "H3C", Model: "WX3540"}, {IP: "192.0.2.11", Name: "Spare-WLC", Vendor: "H3C", Model: "WX3540"}, {IP: "192.0.2.12", Name: "Switch", Vendor: "H3C", Model: "S6520"}}}, nil
		}
	})
	if code != 0 {
		t.Fatal(code, out, err)
	}
	var devices []wireless.Record
	_ = json.Unmarshal([]byte(out), &devices)
	if len(devices) != 1 || devices[0]["platform"] != "h3c_wireless" {
		t.Fatal(out)
	}
}
func TestParseOfflineAndNumericIntent(t *testing.T) {
	cases := []struct {
		body   string
		args   []string
		code   int
		status string
	}{{"sysname WLC\nreturn\n", []string{"--json"}, 1, "partial"}, {"sysname WLC\nreturn\n", []string{"--complete", "--json"}, 0, "complete"}, {"", []string{"--platform", "huawei", "--complete", "--json"}, 1, "unsupported"}, {"No LLDP neighbors\n", []string{"--dimension", "lldp", "--ap-name", "AP-LAB", "--complete", "--json"}, 0, "complete"}, {"No LLDP neighbors\n", []string{"--dimension", "lldp", "--complete"}, 2, ""}}
	for _, c := range cases {
		var out, err bytes.Buffer
		code := Run(context.Background(), append([]string{"parse"}, c.args...), Dependencies{Environment: map[string]string{}, Input: strings.NewReader(c.body), Output: &out, Error: &err})
		if code != c.code {
			t.Fatal(code, c.code, out.String(), err.String())
		}
		if c.status != "" {
			var doc map[string]any
			_ = json.Unmarshal(out.Bytes(), &doc)
			if doc["status"] != c.status {
				t.Fatal(out.String())
			}
		}
	}
	var out, err bytes.Buffer
	code := Run(context.Background(), []string{"parse", "--file", "../../testdata/float-kind-configuration.cfg", "--json", "--complete"}, Dependencies{Environment: map[string]string{}, Input: panicReader{}, Output: &out, Error: &err})
	if code != 0 {
		t.Fatal(code, err.String())
	}
	for _, token := range []string{`"max_power": 20.0`, `"rssi": -0.0`, `1.0,`, `5.5`} {
		if !strings.Contains(out.String(), token) {
			t.Fatal("missing", token, out.String())
		}
	}
}

type cancelWriter struct {
	cancel  context.CancelFunc
	b       bytes.Buffer
	stage   string
	failure bool
}

func (w *cancelWriter) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte(w.stage)) {
		w.cancel()
		if w.failure {
			return 0, io.ErrClosedPipe
		}
	}
	return w.b.Write(b)
}
func TestLateCancellationAndOutputErrorRewritesCheckpoint(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancellation", true: "cancel-and-output-failure"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "wireless.yml"), "directory: reports\ndimensions: [access_points]\ndevices: [{host: '192.0.2.10'}]\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := &cancelWriter{cancel: cancel, stage: "批次结束：", failure: fail}
			var out bytes.Buffer
			var closed atomic.Int32
			code := Run(ctx, []string{"--json"}, Dependencies{Environment: map[string]string{"NC_DEVICE_USERNAME": "fixture"}, Input: panicReader{}, Output: &out, Error: writer, Discovery: discovery(dir), SessionFactory: fixtureFactory(&closed)})
			if code != 130 || closed.Load() != 1 {
				t.Fatal(code, closed.Load())
			}
			var summary map[string]any
			if e := json.Unmarshal(out.Bytes(), &summary); e != nil {
				t.Fatal(e, out.String())
			}
			if summary["status"] != "interrupted" {
				t.Fatal(summary)
			}
			if fail && summary["output_error"] == nil {
				t.Fatal("missing output failure")
			}
			b, e := os.ReadFile(filepath.Join(summary["directory"].(string), "summary.json"))
			if e != nil || strings.TrimSpace(string(b)) != strings.TrimSpace(out.String()) {
				t.Fatal("durable late cancellation mismatch", e)
			}
		})
	}
}
func TestStdoutFailureInvalidatesDurableSuccessWithoutReplay(t *testing.T) {
	writer := &failingWriter{always: true}
	var calls atomic.Int32
	code, _, _, _, dir := runFixture(t, "devices: [{host: '192.0.2.10'}]\n", []string{"--json"}, func(d *Dependencies) {
		d.Output = writer
		factory := d.SessionFactory
		d.SessionFactory = func(ctx context.Context, device wireless.Record, c connector.ConnectionConfig) (bootstrap.Connection, error) {
			calls.Add(1)
			return factory(ctx, device, c)
		}
	})
	if code != 2 || calls.Load() != 1 {
		t.Fatal(code, calls.Load())
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "reports/*/summary.json"))
	b, _ := os.ReadFile(paths[0])
	var summary map[string]any
	_ = json.Unmarshal(b, &summary)
	if summary["status"] != "incomplete" || summary["output_error"] == nil {
		t.Fatal(string(b))
	}
}
func TestExecutorFailureKeepsPendingAndRunnerReuseIsIndependent(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "wireless.yml"), "directory: reports\ndimensions: [access_points]\ndevices: [{host: '192.0.2.10'}, {host: '192.0.2.11'}]\n")
	var out, diagnostic bytes.Buffer
	var closed atomic.Int32
	runner := New(Dependencies{Environment: map[string]string{"NC_DEVICE_USERNAME": "fixture"}, Input: panicReader{}, Output: &out, Error: &diagnostic, Discovery: discovery(dir), SessionFactory: fixtureFactory(&closed), Execute: func(ctx context.Context, _ int, devices []wireless.Record, run func(context.Context, wireless.Record) error) error {
		if e := run(ctx, devices[0]); e != nil {
			return e
		}
		return errors.New("private scheduler")
	}})
	if code := runner.Run(context.Background(), []string{"--json"}); code != 2 {
		t.Fatal(code, out.String(), diagnostic.String())
	}
	var summary map[string]any
	_ = json.Unmarshal(out.Bytes(), &summary)
	if summary["completed"] != float64(1) || len(summary["pending"].([]any)) != 1 || summary["execution_error"] == nil {
		t.Fatal(summary)
	}
	first := summary["directory"].(string)
	out.Reset()
	diagnostic.Reset()
	runner.Execute = bootstrap.Execute
	if code := runner.Run(context.Background(), []string{"--json"}); code != 0 {
		t.Fatal(code, out.String(), diagnostic.String())
	}
	_ = json.Unmarshal(out.Bytes(), &summary)
	if summary["completed"] != float64(2) || summary["directory"] == first {
		t.Fatal(summary)
	}
	out.Reset()
	if code := runner.Run(context.Background(), []string{"--parallel", "0"}); code != 2 {
		t.Fatal(code)
	}
	b, _ := os.ReadFile(filepath.Join(first, "summary.json"))
	if !bytes.Contains(b, []byte(`"execution_error"`)) {
		t.Fatal("old report changed")
	}
}

type apReadFunc func(context.Context, collect.APRequest) (collect.APOutcome, error)

func (f apReadFunc) Read(ctx context.Context, ap collect.APRequest) (collect.APOutcome, error) {
	return f(ctx, ap)
}
func TestExplicitLLDPUsesIndependentLazyAPCredentials(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default-controller-only", true: "explicit-lldp"}[explicit], func(t *testing.T) {
			body := "devices: [{host: '192.0.2.10'}]\n"
			args := []string{"--json"}
			if explicit {
				args = append(args, "--dimensions", "lldp", "--ap-name", "AP-LAB")
			}
			var apCalls atomic.Int32
			var controller *fixtureSession
			code, summary, out, err, _ := runFixture(t, body, args, func(d *Dependencies) {
				d.Environment = map[string]string{"NC_DEVICE_USERNAME": "controller", "NC_DEVICE_PASSWORD": "controller-private", "WIRELESS_AP_USERNAME": "ap-user", "WIRELESS_AP_PASSWORD": "ap-private"}
				d.SessionFactory = func(context.Context, wireless.Record, connector.ConnectionConfig) (bootstrap.Connection, error) {
					return bootstrap.Connection{Platform: "h3c", Open: func(ctx context.Context, use func(collect.Session) error) error {
						controller = &fixtureSession{body: "Total number of APs : 1\nAP name : AP-LAB\nAP ID : 7\nState : Run\n"}
						return use(controller)
					}}, nil
				}
				d.APFactory = func(_ context.Context, session collect.Session, credentials collect.APCredentials) (collect.APReader, error) {
					apCalls.Add(1)
					if session != controller || credentials.Username != "ap-user" || credentials.Password != "ap-private" {
						t.Error("wrong AP/controller credentials")
					}
					return apReadFunc(func(_ context.Context, ap collect.APRequest) (collect.APOutcome, error) {
						if ap.AccessPoint["name"] != "AP-LAB" {
							t.Error("wrong AP")
						}
						body := "No LLDP neighbors\n"
						now := time.Now()
						capture, e := wireless.NewCapture(wireless.CaptureOptions{Command: ap.Query.Command(), Body: &body, StartedAt: now, CompletedAt: now})
						return collect.APOutcome{Result: &capture}, e
					}), nil
				}
			})
			if code != 0 {
				t.Fatal(code, out, err)
			}
			if explicit && apCalls.Load() != 1 || !explicit && apCalls.Load() != 0 {
				t.Fatal("unexpected AP factory calls", apCalls.Load())
			}
			if explicit {
				dimensions := summary["devices"].([]any)[0].(map[string]any)["dimensions"].(map[string]any)
				if dimensions["lldp"].(map[string]any)["status"] != "complete" {
					t.Fatal(dimensions)
				}
			}
			if strings.Contains(out+err, "private") {
				t.Fatal("secret leaked")
			}
		})
	}
}
func TestRecommendationHookSelectedPerRunAndArtifactsFailureRetainsUnsaved(t *testing.T) {
	for _, scripts := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "scripts"}[scripts], func(t *testing.T) {
			args := []string{"--json"}
			if scripts {
				args = append(args, "--scripts")
			}
			var calls atomic.Int32
			code, s, out, err, _ := runFixture(t, "devices: [{host: '192.0.2.10'}]\n", args, func(d *Dependencies) {
				d.RecommendationHook = func(flag bool) report.DeviceHook {
					if flag != scripts {
						t.Error("wrong scripts flag")
					}
					return func(_ context.Context, document wireless.Record) (report.Enrichment, error) {
						calls.Add(1)
						dims := document["snapshot"].(map[string]any)["dimensions"].(map[string]any)
						if dims["access_points"].(map[string]any)["status"] != "complete" {
							t.Error("hook not given materialized wire document")
						}
						return report.Enrichment{Recommendations: wireless.Record{"counts": wireless.Record{}}, Artifacts: func(context.Context, *report.Report, report.PublishedDevice) error {
							return errors.New("private hook failure")
						}}, nil
					}
				}
			})
			if code != 2 || calls.Load() != 1 {
				t.Fatal(code, calls.Load(), out, err)
			}
			device := s["devices"].([]any)[0].(map[string]any)
			if device["report_saved"] != false || s["report_error"] == nil {
				t.Fatal(s)
			}
			paths, _ := filepath.Glob(filepath.Join(s["directory"].(string), "devices/*.json"))
			if len(paths) != 1 {
				t.Fatal(paths)
			}
		})
	}
}
func TestParseEvidenceFileIsNotSubjectToConfigFileBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.cfg")
	write(t, path, "#"+strings.Repeat("x", 16<<20)+"\nsysname WLC\nreturn\n")
	var out, diagnostic bytes.Buffer
	code := Run(context.Background(), []string{"parse", "--file", path, "--complete"}, Dependencies{Environment: map[string]string{}, Input: panicReader{}, Output: &out, Error: &diagnostic})
	if code != 0 {
		t.Fatal(code, diagnostic.String(), out.String())
	}
}

func has(m wireless.Record, key string) bool { _, ok := m[key]; return ok }
