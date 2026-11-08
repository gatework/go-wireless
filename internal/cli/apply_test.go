package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/internal/bootstrap"

	"github.com/gatework/go-wireless/vendors"

	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-net-connector/storage"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/report"
)

type applyFixture struct {
	before, after wireless.Record
	read, writes  int
	failAt        int
	inspect       func(int) error
}

func (s *applyFixture) ReadConfiguration(context.Context) (wireless.Record, error) {
	s.read++
	if s.read == 1 {
		return s.before, nil
	}
	return s.after, nil
}
func (s *applyFixture) Write(ctx context.Context, c string) error {
	s.writes++
	if s.inspect != nil {
		if e := s.inspect(s.writes); e != nil {
			return e
		}
	}
	if s.failAt == s.writes {
		return errors.New("private transport detail")
	}
	return ctx.Err()
}
func applySetup(t *testing.T) ([]byte, wireless.Record, wireless.Record) {
	t.Helper()
	plan, e := os.ReadFile("../../testdata/ruby-interop/security.plan.json")
	if e != nil {
		t.Fatal(e)
	}
	source, e := os.ReadFile("../../testdata/ruby-interop/source.cfg")
	if e != nil {
		t.Fatal(e)
	}
	p, _ := vendors.NewBuiltinRegistry().Fetch("h3c_wireless", "")
	before, e := p.Parse(wireless.Configuration, string(source), true)
	if e != nil || before.Status() != wireless.Complete {
		t.Fatal(before, e)
	}
	after, e := p.Parse(wireless.Configuration, strings.ReplaceAll(strings.ReplaceAll(string(source), " cipher-suite tkip\n", ""), " security-ie wpa\n", ""), true)
	if e != nil {
		t.Fatal(e)
	}
	return plan, before.Records()[0], after.Records()[0]
}
func TestApplyDurableIntentReadbackAndNoReplay(t *testing.T) {
	for _, fail := range []int{0, 1, 4} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			plan, before, after := applySetup(t)
			root := t.TempDir()
			path := filepath.Join(root, "plan.json")
			os.WriteFile(path, plan, 0600)
			sha := fmt.Sprintf("%x", sha256.Sum256(plan))
			input := strings.NewReader("APPLY 192.0.2.10 " + sha[:12] + "\n")
			var out, errs bytes.Buffer
			session := &applyFixture{before: before, after: after, failAt: fail}
			var store *report.Report
			opens := 0
			d := Dependencies{Input: input, Output: &out, Error: &errs, Terminal: func(any) bool { return true }, Environment: map[string]string{"NC_DEVICE_USERNAME": "process"}, Discovery: bootstrap.Discovery{Directories: []string{}}, NewReport: func(root string, o report.Options) (*report.Report, error) {
				var e error
				store, e = report.New(root, o)
				return store, e
			}, ApplyFactory: func(ctx context.Context, device wireless.Record, cfg connector.ConnectionConfig, run func(context.Context, change.ApplySession) error) (error, error) {
				opens++
				if cfg.HostKeyPolicy != "strict" || cfg.Username != "process" {
					t.Fatal(cfg)
				}
				return run(ctx, session), nil
			}}
			session.inspect = func(n int) error {
				b, e := os.ReadFile(filepath.Join(store.Directory(), "execution.json"))
				if e != nil {
					return e
				}
				var state map[string]any
				json.Unmarshal(b, &state)
				if state["status"] != "executing" || int(state["pending_command"].(float64)) != n-1 {
					t.Errorf("missing durable intent %s", b)
				}
				return nil
			}
			code := Run(context.Background(), []string{"apply", "--plan", path, "--directory", filepath.Join(root, "executions")}, d)
			if opens != 1 {
				t.Fatalf("opens=%d code=%d output=%s err=%s", opens, code, out.String(), errs.String())
			}
			b, e := os.ReadFile(filepath.Join(store.Directory(), "execution.json"))
			if e != nil {
				t.Fatal(e)
			}
			var state map[string]any
			json.Unmarshal(b, &state)
			if fail == 0 {
				if code != 0 || state["status"] != "configuration_verified" || session.read != 2 || session.writes != 8 {
					t.Fatalf("code=%d read=%d write=%d state=%s err=%s", code, session.read, session.writes, b, errs.String())
				}
			} else if code != 2 || state["status"] != "uncertain" || session.writes != fail || session.read != 1 {
				t.Fatalf("code=%d write=%d state=%s", code, session.writes, b)
			}
		})
	}
}
func TestApplyRejectsTTYAndTamperingBeforeFactory(t *testing.T) {
	plan, _, _ := applySetup(t)
	for _, mode := range []string{"tty", "tampered", "token"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "plan.json")
			b := plan
			if mode == "tampered" {
				b = bytes.ReplaceAll(b, []byte("undo security-ie wpa"), []byte("save force"))
			}
			os.WriteFile(path, b, 0600)
			var out, err bytes.Buffer
			calls := 0
			code := Run(context.Background(), []string{"apply", "--plan", path, "--directory", filepath.Join(root, "logs")}, Dependencies{Input: strings.NewReader("wrong\n"), Output: &out, Error: &err, Terminal: func(any) bool { return mode != "tty" }, ApplyFactory: func(context.Context, wireless.Record, connector.ConnectionConfig, func(context.Context, change.ApplySession) error) (error, error) {
				calls++
				return nil, nil
			}})
			if calls != 0 || code == 0 {
				t.Fatal(calls, code)
			}
			if _, e := os.Stat(filepath.Join(root, "logs")); !os.IsNotExist(e) {
				t.Fatal("created before confirmation")
			}
		})
	}
}
func TestApplyIntentPublicationFailureStopsWrites(t *testing.T) {
	plan, before, after := applySetup(t)
	root := t.TempDir()
	path := filepath.Join(root, "plan.json")
	os.WriteFile(path, plan, 0600)
	sha := fmt.Sprintf("%x", sha256.Sum256(plan))
	session := &applyFixture{before: before, after: after}
	var out, errs bytes.Buffer
	writes := 0
	var store *report.Report
	code := Run(context.Background(), []string{"apply", "--plan", path, "--directory", filepath.Join(root, "logs")}, Dependencies{Input: strings.NewReader("APPLY 192.0.2.10 " + sha[:12] + "\n"), Output: &out, Error: &errs, Environment: map[string]string{"NC_DEVICE_USERNAME": "x"}, Discovery: bootstrap.Discovery{Directories: []string{}}, Terminal: func(any) bool { return true }, ApplyFactory: func(ctx context.Context, _ wireless.Record, _ connector.ConnectionConfig, run func(context.Context, change.ApplySession) error) (error, error) {
		return run(ctx, session), nil
	}, NewReport: func(root string, o report.Options) (*report.Report, error) {
		o.Write = func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
			writes++
			if writes == 2 {
				return storage.Receipt{}, errors.New("failure")
			}
			return storage.Write(ctx, path, b)
		}
		var e error
		store, e = report.New(root, o)
		return store, e
	}})
	if code != 2 || session.writes != 0 {
		t.Fatal(code, session.writes)
	}
	b, _ := os.ReadFile(filepath.Join(store.Directory(), "execution.json"))
	if !bytes.Contains(b, []byte(`"status": "failed"`)) {
		t.Fatal(string(b))
	}
}

func TestApplyFailureBoundaries(t *testing.T) {
	for _, mode := range []string{"precheck", "ack_failure", "intent_cancel", "readback", "cleanup", "final_fsync", "final_recovery_failure"} {
		t.Run(mode, func(t *testing.T) {
			plan, before, after := applySetup(t)
			root := t.TempDir()
			path := filepath.Join(root, "plan.json")
			os.WriteFile(path, plan, 0600)
			sha := fmt.Sprintf("%x", sha256.Sum256(plan))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session := &applyFixture{before: before, after: after}
			if mode == "precheck" {
				session.before = after
			}
			if mode == "readback" {
				session.after = before
			}
			var out, errs bytes.Buffer
			var store *report.Report
			publicationFailed := false
			code := Run(ctx, []string{"apply", "--plan", path, "--directory", filepath.Join(root, "logs")}, Dependencies{Input: strings.NewReader("APPLY 192.0.2.10 " + sha[:12] + "\n"), Output: &out, Error: &errs, Environment: map[string]string{"NC_DEVICE_USERNAME": "x"}, Discovery: bootstrap.Discovery{Directories: []string{}}, Terminal: func(any) bool { return true }, ApplyFactory: func(ctx context.Context, _ wireless.Record, _ connector.ConnectionConfig, run func(context.Context, change.ApplySession) error) (error, error) {
				e := run(ctx, session)
				if mode == "cleanup" {
					return e, errors.New("sensitive close error")
				}
				return e, nil
			}, NewReport: func(root string, o report.Options) (*report.Report, error) {
				o.Write = func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
					var state map[string]any
					json.Unmarshal(b, &state)
					if mode == "ack_failure" && session.writes == 1 && !publicationFailed {
						publicationFailed = true
						return storage.Receipt{}, errors.New("ack persistence")
					}
					if strings.HasPrefix(mode, "final_") && state["status"] == "configuration_verified" {
						publicationFailed = true
						receipt, e := storage.Write(ctx, path, b)
						if e != nil {
							return receipt, e
						}
						return storage.Receipt{State: "committed", Phase: "directory_fsync"}, errors.New("fsync")
					}
					if mode == "final_recovery_failure" && publicationFailed {
						return storage.Receipt{}, errors.New("restoration")
					}
					receipt, e := storage.Write(ctx, path, b)
					if mode == "intent_cancel" && state["status"] == "executing" {
						cancel()
					}
					return receipt, e
				}
				var e error
				store, e = report.New(root, o)
				return store, e
			}})
			wantCode := 2
			if mode == "intent_cancel" {
				wantCode = 130
			}
			if code != wantCode {
				t.Fatal(code, errs.String())
			}
			wantWrites := 8
			if mode == "precheck" || mode == "intent_cancel" {
				wantWrites = 0
			}
			if mode == "ack_failure" {
				wantWrites = 1
			}
			if session.writes != wantWrites {
				t.Fatal("replay or extra command", session.writes)
			}
			b, e := os.ReadFile(filepath.Join(store.Directory(), "execution.json"))
			if mode == "final_recovery_failure" {
				if e == nil && bytes.Contains(b, []byte(`"status": "configuration_verified"`)) {
					t.Fatal("false final success")
				}
				b, e = os.ReadFile(filepath.Join(store.Directory(), "execution-checkpoint.json"))
			}
			if e != nil {
				t.Fatal(e)
			}
			var state map[string]any
			json.Unmarshal(b, &state)
			wantStatus := "uncertain"
			if mode == "precheck" {
				wantStatus = "failed"
			}
			if state["status"] != wantStatus {
				t.Fatal(string(b))
			}
			if mode == "intent_cancel" && state["pending_command"] != float64(0) {
				t.Fatal("intent lost", string(b))
			}
			if mode == "cleanup" || strings.HasPrefix(mode, "final_") {
				if state["readback_verified"] != true {
					t.Fatal("truthful readback lost")
				}
			}
			if strings.Contains(string(b), "sensitive") {
				t.Fatal("private error exposed")
			}
		})
	}
}
func TestApplyCredentialDiscoveryAndExplicitLastProcessFirst(t *testing.T) {
	plan, before, after := applySetup(t)
	root := t.TempDir()
	path := filepath.Join(root, "plan.json")
	os.WriteFile(path, plan, 0600)
	os.WriteFile(filepath.Join(root, ".env"), []byte("NC_DEVICE_USERNAME=default\nNC_DEVICE_PASSWORD=default-password\nNC_KNOWN_HOSTS=./known-hosts\n"), 0600)
	explicit := filepath.Join(root, "explicit.env")
	os.WriteFile(explicit, []byte("NC_DEVICE_USERNAME=explicit\nNC_DEVICE_PASSWORD=explicit-password\n"), 0600)
	os.WriteFile(filepath.Join(root, "wireless.yml"), []byte("invalid: [unrelated public YAML"), 0600)
	sha := fmt.Sprintf("%x", sha256.Sum256(plan))
	var out, errs bytes.Buffer
	session := &applyFixture{before: before, after: after}
	seen := false
	code := Run(context.Background(), []string{"apply", "--plan", path, "--env-file", explicit, "--directory", filepath.Join(root, "logs")}, Dependencies{Input: strings.NewReader("APPLY 192.0.2.10 " + sha[:12] + "\n"), Output: &out, Error: &errs, Environment: map[string]string{"NC_DEVICE_USERNAME": "process", "NC_HOST_KEY_POLICY": "accept_new"}, Discovery: bootstrap.Discovery{CWD: root, Directories: []string{root}}, Terminal: func(any) bool { return true }, ApplyFactory: func(ctx context.Context, _ wireless.Record, cfg connector.ConnectionConfig, run func(context.Context, change.ApplySession) error) (error, error) {
		seen = true
		if cfg.Username != "process" || cfg.Password != "explicit-password" || cfg.KnownHosts != filepath.Join(root, "known-hosts") || cfg.HostKeyPolicy != "strict" || cfg.AllowTelnetFallback || cfg.AllowLegacySSH {
			t.Fatal("credential precedence/trust boundary", cfg)
		}
		return run(ctx, session), nil
	}})
	if code != 0 || !seen {
		t.Fatal(code, errs.String())
	}
}
