package wireless_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gatework/go-wireless/parser"
	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
)

type testSession struct {
	connected       bool
	output          map[string]string
	failures        map[string]*wireless.FailureError
	stateErr, error error
	calls           []string
	onExecute       func(string)
	receipt         func(string) collect.Receipt
}

func (s *testSession) State(context.Context) (bool, error) { return s.connected, s.stateErr }
func (s *testSession) Execute(_ context.Context, command string) (collect.Receipt, error) {
	s.calls = append(s.calls, command)
	if s.onExecute != nil {
		s.onExecute(command)
	}
	if s.error != nil {
		return collect.Receipt{}, s.error
	}
	if s.receipt != nil {
		return s.receipt(command), nil
	}
	body := s.output[command]
	f := s.failures[command]
	return collect.NewReceipt(collect.ReceiptOptions{Attempted: true, Command: command, Raw: command + "\n" + body + "<WLC>", Body: body, Success: f == nil, Steps: 1, Failure: f}), nil
}
func controllerFixture() *testSession {
	return &testSession{connected: true, output: map[string]string{"screen-length disable": "", "display wlan ap all verbose": "Total number of APs : 0\n", "display wlan client verbose": "Total number of clients : 0\n"}, failures: map[string]*wireless.FailureError{}}
}
func options(s collect.Session, dims ...wireless.Dimension) collect.CollectOptions {
	return collect.CollectOptions{Target: wireless.Record{"host": "192.0.2.10"}, Profile: selectedProfile("h3c"), ConnectorPlatform: "h3c_wireless", Dimensions: dims, Open: func(ctx context.Context, run func(collect.Session) error) error { return run(s) }}
}
func mustCollect(t *testing.T, c *collect.Collector, o collect.CollectOptions) wireless.Snapshot {
	t.Helper()
	s, e := c.Collect(t.Context(), o)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func code(o wireless.Observation) string {
	issues := o.Issues()
	if len(issues) == 0 {
		return ""
	}
	v, _ := issues[0]["code"].(string)
	return v
}
func TestCollectorSharedQueriesFirstFactsAndNoSession(t *testing.T) {
	s := controllerFixture()
	c := collect.NewCollector(collect.CollectorOptions{})
	snap := mustCollect(t, c, options(s, wireless.AccessPoints, wireless.Radios))
	if !snap.Complete() || len(s.calls) != 2 || snap.Dimension(wireless.Clients).Status() != wireless.NotRequested {
		t.Fatal(snap, s.calls)
	}
	if !reflect.DeepEqual(snap.Dimension(wireless.AccessPoints).Evidence(), snap.Dimension(wireless.Radios).Evidence()) {
		t.Fatal("shared metadata diverged")
	}
	s = controllerFixture()
	s.output["display wlan ap all verbose"] = "Total number of APs : 1\nAP name : AP-FIRST\nAP ID : 1\nState : Run\n"
	o := options(s, wireless.AccessPoints)
	o.Open = func(_ context.Context, run func(collect.Session) error) error {
		if e := run(s); e != nil {
			return e
		}
		s.output["display wlan ap all verbose"] = "Total number of APs : 0\n"
		return run(s)
	}
	snap = mustCollect(t, c, o)
	if len(snap.Records(wireless.AccessPoints)) != 1 || snap.Records(wireless.AccessPoints)[0]["name"] != "AP-FIRST" || code(snap.Dimension(wireless.Collection)) != "session_reentered" || len(s.calls) != 2 {
		t.Fatal(snap, s.calls)
	}
	o.Open = func(context.Context, func(collect.Session) error) error { return nil }
	snap = mustCollect(t, c, o)
	if code(snap.Dimension(wireless.AccessPoints)) != "session_not_opened" || snap.Complete() {
		t.Fatal(snap)
	}
}
func TestCollectorPreflightAndOwnership(t *testing.T) {
	for _, change := range []func(*collect.CollectOptions){func(o *collect.CollectOptions) { o.Target = nil }, func(o *collect.CollectOptions) { o.Dimensions = []wireless.Dimension{} }, func(o *collect.CollectOptions) {
		o.Dimensions = []wireless.Dimension{wireless.Clients, wireless.Clients}
	}, func(o *collect.CollectOptions) { o.Dimensions = []wireless.Dimension{"unknown"} }, func(o *collect.CollectOptions) { o.Dimensions = []wireless.Dimension{wireless.Collection} }, func(o *collect.CollectOptions) { o.APNames = []string{"bad\nname"} }, func(o *collect.CollectOptions) { o.ConnectorPlatform = "huawei" }, func(o *collect.CollectOptions) { o.Profile = nil }} {
		o := options(controllerFixture(), wireless.Clients)
		o.Open = func(context.Context, func(collect.Session) error) error {
			t.Fatal("preflight opened device")
			return nil
		}
		change(&o)
		if _, e := collect.NewCollector(collect.CollectorOptions{}).Collect(t.Context(), o); e == nil {
			t.Fatal("invalid input accepted")
		}
	}
	s := controllerFixture()
	o := options(s, wireless.AccessPoints)
	o.Target["nested"] = wireless.Record{"name": "before"}
	o.Open = func(_ context.Context, run func(collect.Session) error) error {
		o.Target["nested"].(wireless.Record)["name"] = "after"
		o.Dimensions[0] = wireless.Clients
		*o.Profile = parser.Profile{}
		return run(s)
	}
	snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
	if snap.Target()["nested"].(map[string]any)["name"] != "before" || !snap.Dimension(wireless.AccessPoints).Complete() {
		t.Fatal("callback mutated selected input")
	}
}
func TestCollectorIndependentFailurePreparationAndClose(t *testing.T) {
	s := controllerFixture()
	s.failures["display wlan ap all verbose"] = wireless.NewFailure("device_error", "execute")
	snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), options(s, wireless.AccessPoints, wireless.Radios, wireless.Clients))
	if !snap.Dimension(wireless.Clients).Complete() || snap.Dimension(wireless.AccessPoints).Status() != wireless.Failed || snap.Dimension(wireless.Collection).Status() != wireless.Partial || len(s.calls) != 3 {
		t.Fatal(snap, s.calls)
	}
	s = controllerFixture()
	s.failures["screen-length disable"] = wireless.NewFailure("device_error", "execute")
	snap = mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), options(s, wireless.AccessPoints, wireless.Clients))
	if len(s.calls) != 1 || snap.Dimension(wireless.Clients).Issues()[0]["skipped"] != true {
		t.Fatal(snap, s.calls)
	}
	s = controllerFixture()
	s.onExecute = func(command string) {
		if command == "display wlan ap all verbose" {
			s.connected = false
		}
	}
	s.failures["display wlan ap all verbose"] = wireless.NewFailure("connection_closed", "execute")
	snap = mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), options(s, wireless.AccessPoints, wireless.Clients))
	if len(s.calls) != 2 || code(snap.Dimension(wireless.Clients)) != "connection_closed" || snap.Dimension(wireless.Clients).Issues()[0]["skipped"] != true {
		t.Fatal(snap, s.calls)
	}
	s = controllerFixture()
	o := options(s, wireless.AccessPoints)
	o.Open = func(_ context.Context, run func(collect.Session) error) error {
		_ = run(s)
		return errors.New("private close message")
	}
	snap = mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
	if !snap.Dimension(wireless.AccessPoints).Complete() || code(snap.Dimension(wireless.Collection)) != "session_close_failed" {
		t.Fatal(snap)
	}
}
func TestCollectorOrderingDependenciesUnsupportedAndParserCompleteness(t *testing.T) {
	s := controllerFixture()
	o := options(s)
	o.Dimensions = nil
	_ = mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
	p, _ := vendors.NewBuiltinRegistry().Fetch("h3c", "")
	dims := []wireless.Dimension{}
	for _, d := range p.Dimensions() {
		if d != wireless.LLDP {
			dims = append(dims, d)
		}
	}
	queries, _ := p.Queries(dims)
	want := []string{p.PreparationCommand()}
	for _, q := range queries {
		want = append(want, q.Command)
	}
	if !reflect.DeepEqual(want, s.calls) {
		t.Fatalf("order %v expected %v", s.calls, want)
	}
	s = controllerFixture()
	s.output["display current-configuration"] = "sysname WLC\nwlan service-template office\n ssid Office\n#\n--More--\nreturn\n"
	snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), options(s, wireless.ServiceTemplates, wireless.APGroups, wireless.Clients))
	if snap.Dimension(wireless.Configuration).Status() != wireless.Partial || snap.Dimension(wireless.ServiceTemplates).Status() != wireless.Partial || !snap.Dimension(wireless.Clients).Complete() || len(s.calls) != 3 {
		t.Fatal(snap, s.calls)
	}
	s = controllerFixture()
	s.output["display wlan ap all verbose"] = ""
	snap = mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), options(s, wireless.AccessPoints))
	if snap.Complete() {
		t.Fatal("transport success became completeness")
	}
	s = controllerFixture()
	o = options(s, wireless.Radios, wireless.LLDP)
	o.Profile = selectedProfile("huawei")
	o.ConnectorPlatform = "huawei"
	s.output["display ap all"] = "Total: 0\n"
	snap = mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
	if snap.Dimension(wireless.Radios).Status() != wireless.Unsupported || snap.Dimension(wireless.LLDP).Status() != wireless.Unsupported || len(s.calls) != 2 {
		t.Fatal(snap, s.calls)
	}
}
func TestCollectorLateAndConcurrentCallbacksNeverReplay(t *testing.T) {
	s := controllerFixture()
	o := options(s, wireless.Clients)
	var saved func(collect.Session) error
	o.Open = func(_ context.Context, run func(collect.Session) error) error {
		saved = run
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { _ = run(s) })
		}
		wg.Wait()
		return nil
	}
	snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
	if code(snap.Dimension(wireless.Collection)) != "session_reentered" || len(s.calls) != 2 {
		t.Fatal(snap, s.calls)
	}
	_ = saved(s)
	if len(s.calls) != 2 {
		t.Fatal("late callback replayed")
	}
}

type apReaderFunc func(context.Context, collect.APRequest) (collect.APOutcome, error)

func (f apReaderFunc) Read(ctx context.Context, ap collect.APRequest) (collect.APOutcome, error) {
	return f(ctx, ap)
}
func goodAPCapture(t *testing.T, body string) wireless.Capture {
	t.Helper()
	now := time.Now()
	c, e := wireless.NewCapture(wireless.CaptureOptions{Command: selectedProfile("h3c").APQuery().Command(), Body: &body, StartedAt: now, CompletedAt: now, Evidence: []wireless.Record{{"sha256": "synthetic", "ap_name": "OTHER", "command": "wrong", "status": "failed", "kind": "wrong", "attempted": false, "neighbor_count": 99}}})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func apInventory(s *testSession) {
	s.output["display wlan ap all verbose"] = "Total number of APs : 2\nAP name : AP-1\nAP ID : 1\nState : Run\nAP name : AP-2\nAP ID : 2\nState : Run\n"
}
func TestCollectorAPLazyDependenciesSelectionAndBinding(t *testing.T) {
	for _, dims := range [][]wireless.Dimension{{wireless.Clients}, {wireless.LLDP}} {
		s := controllerFixture()
		o := options(s, dims...)
		o.APCredentials = func(context.Context) (collect.APCredentials, error) {
			t.Fatal("resolved unused credentials")
			return collect.APCredentials{}, nil
		}
		snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
		if !snap.Complete() {
			t.Fatal(snap)
		}
	}
	s := controllerFixture()
	apInventory(s)
	o := options(s, wireless.LLDP)
	o.APCredentials = func(context.Context) (collect.APCredentials, error) {
		t.Fatal("resolved credentials without factory")
		return collect.APCredentials{}, nil
	}
	snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
	if code(snap.Dimension(wireless.LLDP)) != "ap_session_required" || !snap.Dimension(wireless.AccessPoints).Complete() {
		t.Fatal(snap)
	}
	names := []string{"AP-1"}
	o = options(s, wireless.LLDP)
	o.APNames = names
	captured := goodAPCapture(t, "No LLDP neighbors\n")
	reads := 0
	c := collect.NewCollector(collect.CollectorOptions{APFactory: func(_ context.Context, session collect.Session, _ collect.APCredentials) (collect.APReader, error) {
		if session != s {
			t.Fatal("controller replaced")
		}
		names[0] = "AP-2"
		return apReaderFunc(func(_ context.Context, ap collect.APRequest) (collect.APOutcome, error) {
			reads++
			if ap.AccessPoint["name"] != "AP-1" {
				t.Fatal("selection changed")
			}
			return collect.APOutcome{Result: &captured}, nil
		}), nil
	}})
	snap = mustCollect(t, c, o)
	ev := snap.Dimension(wireless.LLDP).Evidence()[0]
	if reads != 1 || ev["ap_name"] != "AP-1" || ev["command"] != selectedProfile("h3c").APQuery().Command() || ev["status"] != "complete" || ev["attempted"] != true || ev["neighbor_count"] != 0 || ev["sha256"] != "synthetic" {
		t.Fatal(ev)
	}
}
func TestCollectorAPFailuresDoNotLoseFactsOrOtherAPs(t *testing.T) {
	body := "LLDP neighbor-information of port 1[GE1]:\nLLDP neighbor index: 1\nChassis ID: switch\nPort ID: GE2\n"
	captured := goodAPCapture(t, body)
	for _, which := range []string{"error", "cleanup", "close", "read"} {
		t.Run(which, func(t *testing.T) {
			s := controllerFixture()
			apInventory(s)
			c := collect.NewCollector(collect.CollectorOptions{APFactory: func(context.Context, collect.Session, collect.APCredentials) (collect.APReader, error) {
				return apReaderFunc(func(_ context.Context, ap collect.APRequest) (collect.APOutcome, error) {
					if ap.AccessPoint["name"] == "AP-2" {
						return collect.APOutcome{Error: wireless.NewFailure("ap_not_online", ""), Skipped: true}, nil
					}
					out := collect.APOutcome{Result: &captured}
					switch which {
					case "error":
						out.Error = wireless.NewFailure("device_error", "")
					case "cleanup":
						out.CleanupError = wireless.NewFailure("ap_exit_failed", "")
					case "close":
						out.CloseError = wireless.NewFailure("session_close_failed", "")
					case "read":
						return out, errors.New("private error")
					}
					return out, nil
				}), nil
			}})
			snap := mustCollect(t, c, options(s, wireless.LLDP, wireless.Clients))
			if len(snap.Records(wireless.LLDP)) != 1 || snap.Dimension(wireless.LLDP).Status() != wireless.Partial || !snap.Dimension(wireless.Clients).Complete() || len(snap.Dimension(wireless.LLDP).Evidence()) != 2 {
				t.Fatal(snap)
			}
			data, _ := json.Marshal(snap)
			if strings.Contains(string(data), "private error") {
				t.Fatal("leaked error")
			}
		})
	}
}
func TestCollectorAPInvalidCapturesSetupAndMissingNames(t *testing.T) {
	now := time.Now()
	body := "No LLDP neighbors\n"
	wrong, _ := wireless.NewCapture(wireless.CaptureOptions{Command: "display unrelated", Body: &body, StartedAt: now, CompletedAt: now})
	good := goodAPCapture(t, body)
	zero := wireless.Capture{}
	for _, outcome := range []collect.APOutcome{{Result: &wrong}, {Result: &good, Skipped: true}, {Result: &zero}, {}} {
		s := controllerFixture()
		apInventory(s)
		c := collect.NewCollector(collect.CollectorOptions{APFactory: func(context.Context, collect.Session, collect.APCredentials) (collect.APReader, error) {
			return apReaderFunc(func(context.Context, collect.APRequest) (collect.APOutcome, error) { return outcome, nil }), nil
		}})
		snap := mustCollect(t, c, options(s, wireless.LLDP, wireless.Clients))
		if snap.Dimension(wireless.LLDP).Status() != wireless.Failed || !snap.Dimension(wireless.Clients).Complete() {
			t.Fatal(snap)
		}
	}
	for _, factory := range []collect.APFactory{func(context.Context, collect.Session, collect.APCredentials) (collect.APReader, error) {
		return nil, errors.New("private setup")
	}, func(context.Context, collect.Session, collect.APCredentials) (collect.APReader, error) {
		return nil, nil
	}} {
		s := controllerFixture()
		apInventory(s)
		snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{APFactory: factory}), options(s, wireless.LLDP, wireless.Clients))
		if snap.Dimension(wireless.LLDP).Status() != wireless.Failed || !snap.Dimension(wireless.Clients).Complete() {
			t.Fatal(snap)
		}
	}
	s := controllerFixture()
	apInventory(s)
	o := options(s, wireless.LLDP)
	o.APNames = []string{"missing", "missing"}
	o.APCredentials = func(context.Context) (collect.APCredentials, error) {
		t.Fatal("unused credentials")
		return collect.APCredentials{}, nil
	}
	snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
	if len(snap.Dimension(wireless.LLDP).Issues()) != 2 {
		t.Fatal(snap.Dimension(wireless.LLDP).Issues())
	}
}
func TestCollectorAPSkippedFailuresDeduplicateAndRepeat(t *testing.T) {
	now := time.Now()
	captured, e := wireless.NewCapture(wireless.CaptureOptions{Command: selectedProfile("h3c").APQuery().Command(), StartedAt: now, CompletedAt: now, Failure: wireless.NewFailure("session_closed", "session"), Skipped: true})
	if e != nil {
		t.Fatal(e)
	}
	s := controllerFixture()
	apInventory(s)
	calls := 0
	factoryCalls := 0
	c := collect.NewCollector(collect.CollectorOptions{APFactory: func(context.Context, collect.Session, collect.APCredentials) (collect.APReader, error) {
		factoryCalls++
		return apReaderFunc(func(context.Context, collect.APRequest) (collect.APOutcome, error) {
			calls++
			return collect.APOutcome{Result: &captured, Skipped: true, Error: wireless.NewFailure("session_closed", ""), CleanupError: wireless.NewFailure("session_closed", "")}, nil
		}), nil
	}})
	o := options(s, wireless.LLDP)
	o.Open = func(_ context.Context, run func(collect.Session) error) error { _ = run(s); return run(s) }
	snap := mustCollect(t, c, o)
	if calls != 2 || factoryCalls != 1 || len(snap.Dimension(wireless.LLDP).Issues()) != 2 || snap.Dimension(wireless.LLDP).Evidence()[0]["attempted"] != false || code(snap.Dimension(wireless.Collection)) != "session_reentered" {
		t.Fatal(snap)
	}
}
func TestReaderEvidenceImmutableSafeAndFirstFailure(t *testing.T) {
	s := controllerFixture()
	now := time.Date(2026, 10, 26, 0, 0, 0, 123000000, time.UTC)
	r, _ := collect.NewReader(s, func() time.Time { return now })
	captured, e := r.Read(t.Context(), "display wlan client verbose")
	if e != nil || !captured.Success() {
		t.Fatal(captured, e)
	}
	raw := "display wlan client verbose\nTotal number of clients : 0\n<WLC>"
	ev := captured.Evidence()[0]
	if ev["sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) || ev["bytes"] != len(raw) || ev["observed_at"] != "2026-10-26T00:00:00.123000Z" {
		t.Fatal(ev)
	}
	ev["bytes"] = 0
	if captured.Evidence()[0]["bytes"] == 0 {
		t.Fatal("mutable evidence")
	}
	data, _ := json.Marshal(captured)
	for _, text := range []string{string(data), fmt.Sprint(captured), fmt.Sprintf("%#v", captured)} {
		if strings.Contains(text, "Total number") {
			t.Fatal("body leaked")
		}
	}
	s.failures["display wlan client verbose"] = wireless.NewFailure("device_error", "")
	s.onExecute = func(string) { s.connected = false }
	failed, _ := r.Read(t.Context(), "display wlan client verbose")
	skipped, _ := r.Read(t.Context(), "display wlan ap all verbose")
	if failed.Success() || !skipped.Skipped() || skipped.Failure().Code() != "device_error" || len(skipped.Evidence()) != 0 {
		t.Fatal(failed, skipped)
	}
}
func TestReaderRejectsMalformedReceiptsAndReentry(t *testing.T) {
	for _, response := range []collect.Receipt{{}, collect.NewReceipt(collect.ReceiptOptions{Command: "query", Success: true, Steps: 1}), collect.NewReceipt(collect.ReceiptOptions{Attempted: true, Command: "other", Success: true, Steps: 1}), collect.NewReceipt(collect.ReceiptOptions{Attempted: true, Command: "query", Success: true, Steps: 2}), collect.NewReceipt(collect.ReceiptOptions{Attempted: true, Command: "query", Success: true, Steps: 1, Failure: wireless.NewFailure("device_error", "")}), collect.NewReceipt(collect.ReceiptOptions{Attempted: true, Command: "query", Success: false})} {
		s := controllerFixture()
		s.receipt = func(string) collect.Receipt { return response }
		r, _ := collect.NewReader(s, nil)
		captured, e := r.Read(t.Context(), "query")
		if e != nil || captured.Success() {
			t.Fatal(captured, e)
		}
	}
	s := controllerFixture()
	var r *collect.Reader
	r, _ = collect.NewReader(s, func() time.Time {
		if _, e := r.Read(t.Context(), "nested"); e == nil {
			t.Fatal("clock callback reentered")
		}
		return time.Now()
	})
	_, e := r.Read(t.Context(), "query")
	if e != nil || len(s.calls) != 1 {
		t.Fatal(e, s.calls)
	}
	s = controllerFixture()
	s.stateErr = errors.New("private state")
	r, _ = collect.NewReader(s, nil)
	captured, e := r.Read(t.Context(), "query")
	if e != nil || !captured.Skipped() || captured.Failure().Code() != "session_state_failed" || len(s.calls) != 0 {
		t.Fatal(captured, e)
	}
}

func TestReaderDistinguishesNoAttemptFromEmptyReceivedBytes(t *testing.T) {
	for _, attempted := range []bool{false, true} {
		t.Run(fmt.Sprint(attempted), func(t *testing.T) {
			s := controllerFixture()
			s.receipt = func(command string) collect.Receipt {
				return collect.NewReceipt(collect.ReceiptOptions{Command: command, Attempted: attempted, Failure: wireless.NewFailure("command_timeout", "execute")})
			}
			reader, _ := collect.NewReader(s, nil)
			capture, err := reader.Read(t.Context(), "query")
			if err != nil || capture.Success() || capture.Failure().Code() != "command_timeout" {
				t.Fatal(capture, err)
			}
			evidence := capture.Evidence()
			if !attempted && len(evidence) != 0 {
				t.Fatal("unattempted command acquired byte evidence", evidence)
			}
			if attempted && (len(evidence) != 1 || evidence[0]["bytes"] != 0 || evidence[0]["sha256"] != fmt.Sprintf("%x", sha256.Sum256(nil))) {
				t.Fatal("empty attempted response lost its evidence", evidence)
			}
		})
	}
}

func TestCaptureValidationAndReaderClockBoundaries(t *testing.T) {
	now := time.Now()
	body := "private body"
	for _, o := range []wireless.CaptureOptions{{Command: "bad\n", Body: &body, StartedAt: now, CompletedAt: now}, {Command: "query", Body: &body}, {Command: "query", StartedAt: now, CompletedAt: now}, {Command: "query", Body: &body, Failure: wireless.NewFailure("device_error", ""), StartedAt: now, CompletedAt: now}, {Command: "query", Skipped: true, Failure: wireless.NewFailure("device_error", ""), Evidence: []wireless.Record{{"bytes": 1}}, StartedAt: now, CompletedAt: now}} {
		if _, e := wireless.NewCapture(o); e == nil {
			t.Fatal("invalid capture accepted")
		}
	}
	if wireless.NewFailure("private credential string", "private stage").Code() != "invalid_command_result" {
		t.Fatal("unsafe failure accepted")
	}
	if _, e := collect.NewReader(nil, nil); e == nil {
		t.Fatal("nil session accepted")
	}
	s := controllerFixture()
	r, _ := collect.NewReader(s, func() time.Time { return time.Time{} })
	if _, e := r.Read(t.Context(), "query"); e == nil || len(s.calls) != 0 {
		t.Fatal("zero clock allowed I/O")
	}
	r, _ = collect.NewReader(s, nil)
	if _, e := r.Read(t.Context(), "bad\n"); e == nil {
		t.Fatal("invalid query accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	captured, e := r.Read(ctx, "query")
	if e != nil || !captured.Skipped() || captured.Failure().Code() != "cancelled" || len(s.calls) != 0 {
		t.Fatal(captured, e)
	}
	ticks := 0
	r, _ = collect.NewReader(s, func() time.Time {
		ticks++
		if ticks == 2 {
			return time.Time{}
		}
		return now
	})
	captured, e = r.Read(t.Context(), "query")
	if e != nil || captured.Success() || len(captured.Evidence()) != 0 {
		t.Fatal(captured, e)
	}
}
func TestCollectorAPCredentialsFailureAndUnsupportedCommandViews(t *testing.T) {
	s := controllerFixture()
	apInventory(s)
	o := options(s, wireless.LLDP)
	o.APCredentials = func(context.Context) (collect.APCredentials, error) {
		return collect.APCredentials{}, errors.New("private credentials")
	}
	c := collect.NewCollector(collect.CollectorOptions{APFactory: func(context.Context, collect.Session, collect.APCredentials) (collect.APReader, error) {
		t.Fatal("invalid credentials reached factory")
		return nil, nil
	}})
	snap := mustCollect(t, c, o)
	if code(snap.Dimension(wireless.LLDP)) != "invalid_ap_credentials" {
		t.Fatal(snap)
	}
	for _, body := range []string{"\xff", "% Unrecognized command found at '^' position.\n"} {
		s := controllerFixture()
		s.output["display wlan ap all verbose"] = body
		snap := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), options(s, wireless.AccessPoints, wireless.Radios, wireless.Clients))
		if snap.Dimension(wireless.AccessPoints).Complete() || !snap.Dimension(wireless.Clients).Complete() || !reflect.DeepEqual(snap.Dimension(wireless.AccessPoints).Evidence(), snap.Dimension(wireless.Radios).Evidence()) {
			t.Fatal(snap)
		}
	}
}

func TestTransportObjectsDoNotExposeCredentialsBodiesOrCallbacks(t *testing.T) {
	s := controllerFixture()
	r, _ := collect.NewReader(s, func() time.Time { t.Fatal("formatter called clock"); return time.Now() })
	c := collect.NewCollector(collect.CollectorOptions{Clock: func() time.Time { t.Fatal("formatter called clock"); return time.Now() }})
	receipt := collect.NewReceipt(collect.ReceiptOptions{Attempted: true, Command: "query", Raw: "private receipt", Body: "private receipt", Success: true, Steps: 1})
	values := []any{receipt, r, c, collect.APCredentials{Username: "private credential", Password: "private credential"}, collect.APOutcome{Error: wireless.NewFailure("private error", "private stage")}, wireless.NewFailure("private error", "private stage")}
	for _, v := range values {
		j, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		text := fmt.Sprintf("%v %#v %s", v, v, j)
		if strings.Contains(text, "private") {
			t.Fatalf("secret leaked: %T", v)
		}
	}
	if receipt.Raw() != "private receipt" || receipt.Body() != "private receipt" {
		t.Fatal("explicit receipt access lost")
	}
}

func TestReviewOpenerDiagnosticsPreserveCancellationAndClose(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		codes []string
	}{
		{"cancel", context.Canceled, []string{"cancelled"}},
		{"deadline", context.DeadlineExceeded, []string{"deadline_exceeded"}},
		{"typed", wireless.NewFailure("transport_error", "close"), []string{"transport_error"}},
		{"cancel_close", errors.Join(context.Canceled, wireless.NewFailure("session_close_failed", "close")), []string{"cancelled", "session_close_failed"}},
		{"wrapped_join", fmt.Errorf("private: %w", errors.Join(context.DeadlineExceeded, wireless.NewFailure("session_close_failed", "close"))), []string{"deadline_exceeded", "session_close_failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := controllerFixture()
			o := options(s, wireless.Clients)
			o.Open = func(_ context.Context, run func(collect.Session) error) error {
				if e := run(s); e != nil {
					return e
				}
				return tc.err
			}
			snapshot := mustCollect(t, collect.NewCollector(collect.CollectorOptions{}), o)
			if !snapshot.Dimension(wireless.Clients).Complete() {
				t.Fatal("lost first facts")
			}
			got := []string{}
			for _, issue := range snapshot.Dimension(wireless.Collection).Issues() {
				got = append(got, issue["code"].(string))
			}
			if !reflect.DeepEqual(got, tc.codes) {
				t.Fatalf("codes %v want %v", got, tc.codes)
			}
		})
	}
}

func selectedProfile(platform string) *parser.Profile {
	profile, _ := vendors.NewBuiltinRegistry().Fetch(platform, "")
	return profile
}
