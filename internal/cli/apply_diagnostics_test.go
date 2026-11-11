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
	"reflect"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/internal/bootstrap"

	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-net-connector/storage"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/report"
)

func TestApplyRetainsIndependentSafeDiagnostics(t *testing.T) {
	for _, mode := range []string{"connection", "joined_command", "native_phase", "cancellation", "cleanup_publication"} {
		t.Run(mode, func(t *testing.T) {
			plan, before, after := applySetup(t)
			root := t.TempDir()
			path := filepath.Join(root, "plan.json")
			if e := os.WriteFile(path, plan, 0600); e != nil {
				t.Fatal(e)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(plan))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const private = "PRIVATE_DIAGNOSTIC_SENTINEL"
			session := &applyFixture{before: before, after: after}
			session.inspect = func(int) error {
				if mode == "cancellation" {
					cancel()
				}
				var first error = wireless.NewFailure("command_timeout", "execute")
				if mode == "native_phase" {
					first = &connector.Error{Code: "command_timeout", Phase: "parse", Source: private}
				}
				return errors.Join(first, fmt.Errorf(private+": %w", wireless.NewFailure("device_error", "response")), errors.New(private))
			}
			var out, stderr bytes.Buffer
			var store *report.Report
			opens := 0
			failedPublication := false
			code := Run(ctx, []string{"apply", "--plan", path, "--directory", filepath.Join(root, "logs")}, Dependencies{Input: strings.NewReader("APPLY 192.0.2.10 " + hash[:12] + "\n"), Output: &out, Error: &stderr, Terminal: func(any) bool { return true }, Environment: map[string]string{"NC_DEVICE_USERNAME": "fixture"}, Discovery: bootstrap.Discovery{Directories: []string{}}, ApplyFactory: func(ctx context.Context, _ wireless.Record, _ connector.ConnectionConfig, run func(context.Context, change.ApplySession) error) (error, error) {
				opens++
				if mode == "connection" {
					return wireless.NewFailure("authentication_error", "connect"), nil
				}
				e := run(ctx, session)
				if mode == "cleanup_publication" {
					return e, errors.Join(wireless.NewFailure("transport_error", "cleanup"), errors.New(private))
				}
				return e, nil
			}, NewReport: func(root string, o report.Options) (*report.Report, error) {
				o.Write = func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
					var state map[string]any
					_ = json.Unmarshal(b, &state)
					if mode == "cleanup_publication" && state["status"] == "uncertain" {
						failedPublication = true
						return storage.Receipt{State: "not_committed", Phase: "rename"}, errors.New(private)
					}
					return storage.Write(ctx, path, b)
				}
				var e error
				store, e = report.New(root, o)
				return store, e
			}})
			wantCode := 2
			if mode == "cancellation" {
				wantCode = 130
			}
			if code != wantCode || opens != 1 {
				t.Fatal(code, opens, stderr.String())
			}
			body, e := os.ReadFile(filepath.Join(store.Directory(), "execution.json"))
			if e != nil {
				t.Fatal(e)
			}
			state, e := wireless.DecodeJSONDocument(body)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(body)+out.String()+stderr.String(), private) {
				t.Fatal("private error text leaked")
			}
			if mode == "cleanup_publication" {
				if !failedPublication || session.writes != 1 || state["pending_command"] != json.Number("0") || state["status"] != "executing" {
					t.Fatal("intent lost/replay", session.writes, state)
				}
				for line := range strings.SplitSeq(stderr.String(), "\n") {
					if after0, ok := strings.CutPrefix(line, "安全诊断："); ok {
						state, e = wireless.DecodeJSONDocument([]byte(after0))
						if e != nil {
							t.Fatal(e)
						}
					}
				}
				if state["report_errors"] == nil {
					t.Fatal("publication diagnosis absent from stderr", stderr.String())
				}
				if !strings.Contains(stderr.String(), "诊断仅保留在终端") {
					t.Fatal("durability not distinguished", stderr.String())
				}
			}
			wantOperations := []any{map[string]any{"code": "authentication_error", "stage": "connect"}}
			wantStatus := "failed"
			if mode != "connection" {
				wantStatus = "uncertain"
				wantOperations = []any{map[string]any{"code": "command_timeout", "stage": "execute"}, map[string]any{"code": "device_error", "stage": "response"}, map[string]any{"code": "command_execution_failed", "stage": "execute"}}
				if session.writes != 1 || session.read != 1 || state["pending_command"] != json.Number("0") {
					t.Fatal("intent changed or replayed", state)
				}
			} else if session.writes != 0 || session.read != 0 {
				t.Fatal("connected after auth failure")
			}
			if mode == "native_phase" {
				wantOperations[0].(map[string]any)["stage"] = "parse"
			}
			if state["status"] != wantStatus || !reflect.DeepEqual(state["operation_errors"], wantOperations) {
				t.Fatalf("diagnostic loss/order: %v", state)
			}
			if mode == "cancellation" {
				want := []any{map[string]any{"code": "cancelled", "stage": "execute"}}
				if state["error"] != "interrupted" || !reflect.DeepEqual(state["cancellation_errors"], want) {
					t.Fatal(state)
				}
			}
			if mode == "cleanup_publication" {
				want := []any{map[string]any{"code": "transport_error", "stage": "cleanup"}, map[string]any{"code": "session_close_failed", "stage": "close"}}
				if !reflect.DeepEqual(state["cleanup_errors"], want) {
					t.Fatal(state)
				}
				publications := state["report_errors"].([]any)
				p := publications[0].(map[string]any)
				if p["phase"] != "rename" || p["committed"] != false {
					t.Fatal(publications)
				}
			}
		})
	}
}
