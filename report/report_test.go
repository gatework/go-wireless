package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gatework/go-net-connector/storage"
	wireless "github.com/gatework/go-wireless"
)

func result() Result {
	return Result{Device: wireless.Record{"host": "192.0.2.10", "name": "WLC", "model": nil, "platform": "h3c"}, Status: wireless.Complete, Dimensions: map[string]DimensionSummary{"configuration": {Status: wireless.Complete, Records: 1, Issues: []string{}}}, DurationMS: 12, ReportSaved: true}
}
func summary(t *testing.T, r *Report) map[string]any {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(r.Directory(), "summary.json"))
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestPrivateRootAndUTCCollisionSuffix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "reports")
	now := func() time.Time { return time.Date(2026, 10, 26, 14, 5, 6, 0, time.FixedZone("HK", 8*3600)) }
	a, e := New(root, Options{Now: now})
	if e != nil {
		t.Fatal(e)
	}
	b, e := New(root, Options{Now: now})
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Base(a.Directory()) != "2026-10-26_06-05-06" || filepath.Base(b.Directory()) != "2026-10-26_06-05-06_1" {
		t.Fatal(a.Directory(), b.Directory())
	}
	for _, path := range []string{root, a.Directory(), filepath.Join(a.Directory(), "devices")} {
		s, _ := os.Lstat(path)
		if s.Mode().Perm() != 0700 {
			t.Fatal(path, s.Mode())
		}
	}
	public := filepath.Join(t.TempDir(), "public")
	_ = os.Mkdir(public, 0755)
	if _, e = New(public, Options{}); e == nil {
		t.Fatal("public accepted")
	}
	s, _ := os.Stat(public)
	if s.Mode().Perm() != 0755 {
		t.Fatal("modified existing mode")
	}
	link := filepath.Join(t.TempDir(), "link")
	_ = os.Symlink(root, link)
	if _, e = New(link, Options{}); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestDeviceSnapshotNumericKindsAndSingleHookWriter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "reports")
	body, e := os.ReadFile("../testdata/float-kind-configuration.cfg")
	if e != nil {
		t.Fatal(e)
	}
	parser, _ := wireless.NewConfig("h3c", "")
	observation, e := parser.Parse(string(body), true)
	if e != nil {
		t.Fatal(e)
	}
	records := observation.Records()
	records[0]["very_large"] = json.Number("9007199254740993123456789")
	observation, e = wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Configuration, Status: wireless.Complete, Records: records})
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := wireless.NewSnapshot(wireless.SnapshotOptions{Target: result().Device, Dimensions: map[wireless.Dimension]wireless.Observation{wireless.Configuration: observation}, ObservedAt: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
	hookCalls := 0
	r, e := New(root, Options{DeviceHook: func(ctx context.Context, document wireless.Record) (Enrichment, error) {
		hookCalls++
		if has(document, "report_saved") || document["snapshot"] == nil {
			t.Fatal(document)
		}
		return Enrichment{Recommendations: wireless.Record{"counts": wireless.Record{"review": 1}}, Artifacts: func(ctx context.Context, r *Report, p PublishedDevice) error {
			if p.Document["recommendations"] == nil || !strings.HasSuffix(p.Path, ".json") {
				t.Fatal(p)
			}
			saved, e := os.ReadFile(p.Path)
			if e != nil || !bytes.Equal(saved, p.JSON) {
				t.Fatal("hook JSON differs from published")
			}
			return r.Publish(ctx, "recommendations/"+p.Basename+".txt", []byte("review only\n"))
		}}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.WriteDevice(context.Background(), result(), &snapshot); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(r.Directory(), "devices/WLC-192.0.2.10.json"))
	if e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{`"max_power": 20.0`, `"rssi": -0.0`, `9007199254740993123456789`, `1.0,`, `5.5`} {
		if !bytes.Contains(b, []byte(token)) {
			t.Fatal("missing", token, string(b))
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var document map[string]any
	if e = decoder.Decode(&document); e != nil {
		t.Fatal(e)
	}
	facts := document["snapshot"].(map[string]any)["dimensions"].(map[string]any)["configuration"].(map[string]any)["records"].([]any)[0].(map[string]any)
	if facts["very_large"] != json.Number("9007199254740993123456789") {
		t.Fatal(facts)
	}
	if hookCalls != 1 || has(document, "report_saved") {
		t.Fatal("hook or report_saved")
	}
	s, _ := os.Stat(filepath.Join(r.Directory(), "recommendations/WLC-192.0.2.10.txt"))
	if s.Mode().Perm() != 0600 {
		t.Fatal(s.Mode())
	}
}
func has(m map[string]any, k string) bool { _, ok := m[k]; return ok }
func TestDeviceJSONSurvivesLaterArtifactFailure(t *testing.T) {
	for _, stage := range []string{"text", "advice"} {
		t.Run(stage, func(t *testing.T) {
			o := Options{}
			if stage == "text" {
				o.Write = func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
					if strings.HasSuffix(path, ".txt") {
						return storage.Receipt{Phase: "rename", State: "not_committed"}, errors.New("private error")
					}
					return storage.Write(ctx, path, b)
				}
			} else {
				o.DeviceHook = func(context.Context, wireless.Record) (Enrichment, error) {
					return Enrichment{Artifacts: func(context.Context, *Report, PublishedDevice) error { return errors.New("private advice error") }}, nil
				}
			}
			r, e := New(filepath.Join(t.TempDir(), "reports"), o)
			if e != nil {
				t.Fatal(e)
			}
			e = r.WriteDevice(context.Background(), result(), nil)
			if e == nil || strings.Contains(e.Error(), "private") {
				t.Fatal(e)
			}
			b, e := os.ReadFile(filepath.Join(r.Directory(), "devices/WLC-192.0.2.10.json"))
			if e != nil {
				t.Fatal(e)
			}
			var doc map[string]any
			_ = json.Unmarshal(b, &doc)
			if doc["snapshot"] != nil || has(doc, "report_saved") {
				t.Fatal(string(b))
			}
		})
	}
}
func TestFinalizePhaseFailuresNeverLeaveCompleteSummary(t *testing.T) {
	phases := []string{"temporary_file", "write", "file_sync", "rename", "directory_open", "directory_sync", "cleanup"}
	for _, phase := range phases {
		for _, target := range []string{"checkpoint", "text", "final"} {
			t.Run(target+"/"+phase, func(t *testing.T) {
				writes := 0
				failed := false
				committed := phase == "directory_open" || phase == "directory_sync" || phase == "cleanup"
				writer := func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
					if strings.HasSuffix(path, "summary.json") {
						writes++
					}
					hit := target == "checkpoint" && strings.HasSuffix(path, "summary.json") && writes == 1 || target == "text" && strings.HasSuffix(path, "summary.txt") || target == "final" && strings.HasSuffix(path, "summary.json") && writes == 2
					if hit && !failed {
						failed = true
						receipt := storage.Receipt{Path: path, State: "not_committed", Phase: phase}
						if committed {
							if _, e := storage.Write(ctx, path, b); e != nil {
								t.Fatal(e)
							}
							receipt.State = "committed"
							if phase == "cleanup" {
								receipt.State = "durable"
							}
						}
						return receipt, errors.New("private ENOSPC")
					}
					return storage.Write(ctx, path, b)
				}
				r, e := New(filepath.Join(t.TempDir(), "reports"), Options{Write: writer})
				if e != nil {
					t.Fatal(e)
				}
				_, e = r.Finish(context.Background(), []Result{result()}, FinishOptions{Requested: []wireless.Record{result().Device}})
				var pe *Error
				if !errors.As(e, &pe) || pe.Phase != phase || pe.Committed != committed {
					t.Fatal(e)
				}
				b, e := os.ReadFile(filepath.Join(r.Directory(), "summary.json"))
				if errors.Is(e, os.ErrNotExist) {
					if target != "checkpoint" {
						t.Fatal(e)
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				var doc map[string]any
				_ = json.Unmarshal(b, &doc)
				if doc["status"] == "complete" {
					t.Fatal("stale success", string(b))
				}
			})
		}
	}
}
func TestFinalRecoveryFailsRetainsOriginalReceiptAndRemovesUntrustedSuccess(t *testing.T) {
	calls := 0
	r, e := New(filepath.Join(t.TempDir(), "reports"), Options{Write: func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
		if strings.HasSuffix(path, "summary.json") {
			calls++
			if calls == 2 {
				_, e := storage.Write(ctx, path, b)
				if e != nil {
					t.Fatal(e)
				}
				return storage.Receipt{State: "committed", Phase: "directory_sync"}, errors.New("original")
			}
			if calls >= 3 {
				return storage.Receipt{State: "not_committed", Phase: "temporary_file"}, errors.New("recovery")
			}
		}
		return storage.Write(ctx, path, b)
	}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.Finish(context.Background(), []Result{result()}, FinishOptions{})
	var pe *Error
	if !errors.As(e, &pe) || pe.Phase != "directory_sync" || !pe.Committed || !r.RecoveryFailed() {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(r.Directory(), "summary.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("untrusted success left")
	}
}
func TestOldSuccessInvalidatedWhenNewCheckpointFails(t *testing.T) {
	fail := false
	r, e := New(filepath.Join(t.TempDir(), "reports"), Options{Write: func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
		if fail {
			return storage.Receipt{Phase: "write"}, errors.New("private")
		}
		return storage.Write(ctx, path, b)
	}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Finish(context.Background(), []Result{result()}, FinishOptions{}); e != nil {
		t.Fatal(e)
	}
	if summary(t, r)["status"] != "complete" {
		t.Fatal("not complete")
	}
	fail = true
	if _, e = r.Finish(context.Background(), []Result{result()}, FinishOptions{Interrupted: true}); e == nil {
		t.Fatal("expected error")
	}
	if _, e = os.Stat(filepath.Join(r.Directory(), "summary.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("stale complete remains")
	}
}
func TestCompletenessAndExecutionSharePublisher(t *testing.T) {
	r, e := New(filepath.Join(t.TempDir(), "reports"), Options{})
	if e != nil {
		t.Fatal(e)
	}
	pending := wireless.Record{"host": "192.0.2.11", "name": "pending"}
	for _, o := range []FinishOptions{{Requested: []wireless.Record{result().Device, pending}}, {ReportError: "report_failed"}, {ExecutionError: "execution_failed"}, {OutputError: wireless.Record{"error_type": "IOError"}}, {Interrupted: true}} {
		s, e := r.Finish(context.Background(), []Result{result()}, o)
		if e != nil {
			t.Fatal(e)
		}
		if s["status"] == "complete" {
			t.Fatal(s)
		}
	}
	if e = r.WriteExecution(context.Background(), wireless.Record{"status": "planned"}); e != nil {
		t.Fatal(e)
	}
	if e = r.Publish(context.Background(), "../outside", []byte("private")); e == nil {
		t.Fatal("escaped")
	}
	outside := t.TempDir()
	if e = os.Symlink(outside, filepath.Join(r.Directory(), "escape")); e != nil {
		t.Fatal(e)
	}
	if e = r.Publish(context.Background(), "escape/outside", []byte("private")); e == nil {
		t.Fatal("symlink allowed")
	}
	files, _ := os.ReadDir(outside)
	if len(files) != 0 {
		t.Fatal("wrote symlink")
	}
}
func TestFinalizeReviewUsesSameRecoveryAndConfinesPath(t *testing.T) {
	r, e := New(filepath.Join(t.TempDir(), "reports"), Options{})
	if e != nil {
		t.Fatal(e)
	}
	checkpoint := wireless.Record{"status": "incomplete", "review_only": true}
	final := wireless.Record{"status": "complete", "review_only": true}
	if e = r.Finalize(context.Background(), "review.json", checkpoint, final, func(ctx context.Context, r *Report) error {
		return r.Publish(ctx, "review.txt", []byte("not executed\n"))
	}); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(filepath.Dir(r.Directory()), "outside.json")
	if e = os.WriteFile(outside, []byte(`{"status":"complete"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = r.Finalize(context.Background(), "../outside.json", checkpoint, final, nil); e == nil {
		t.Fatal("escaped")
	}
	if _, e = os.Stat(outside); e != nil {
		t.Fatal("outside file changed", e)
	}
}
func TestCancellationAfterFinalRenameSurvivesFailedRecovery(t *testing.T) {
	calls := 0
	r, e := New(filepath.Join(t.TempDir(), "reports"), Options{Write: func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
		if strings.HasSuffix(path, "summary.json") {
			calls++
			if calls == 2 {
				_, err := storage.Write(ctx, path, b)
				if err != nil {
					t.Fatal(err)
				}
				return storage.Receipt{State: "committed", Phase: "directory_sync"}, context.Canceled
			}
			if calls >= 3 {
				return storage.Receipt{State: "not_committed", Phase: "write"}, errors.New("recovery failure")
			}
		}
		return storage.Write(ctx, path, b)
	}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.Finish(context.Background(), []Result{result()}, FinishOptions{})
	if !errors.Is(e, context.Canceled) || !r.RecoveryFailed() {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(r.Directory(), "summary.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("stale success after cancellation")
	}
}
