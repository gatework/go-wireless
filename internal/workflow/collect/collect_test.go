package collect

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gatework/go-net-connector/storage"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/report"
)

func TestDeliveryFailureDowngradesPublicationWithoutRecollection(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "output", true: "cancelled-output"}[interrupted], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store, err := report.New(filepath.Join(t.TempDir(), "reports"), report.Options{})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			result := Run(ctx, Options{Workers: 1, Requested: []wireless.Record{{"host": "192.0.2.10"}}, Journal: store,
				Execute: func(ctx context.Context, _ int, targets []wireless.Record, run func(context.Context, wireless.Record) error) error {
					return run(ctx, targets[0])
				},
				Collect: func(_ context.Context, target wireless.Record) (wireless.Snapshot, error) {
					calls++
					observation, err := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Collection, Status: wireless.Complete})
					if err != nil {
						return wireless.Snapshot{}, err
					}
					return wireless.NewSnapshot(wireless.SnapshotOptions{Target: target, Dimensions: map[wireless.Dimension]wireless.Observation{wireless.Collection: observation}, ObservedAt: time.Now()})
				},
				Events: Events{Deliver: func(wireless.Record) error {
					if interrupted {
						cancel()
					}
					return io.ErrClosedPipe
				}},
			})
			if calls != 1 || !result.Failed || result.Interrupted != interrupted {
				t.Fatal("replayed or lost outcome", calls, result)
			}
			body, err := os.ReadFile(filepath.Join(store.Directory(), "summary.json"))
			if err != nil {
				t.Fatal(err)
			}
			var summary wireless.Record
			if err := json.Unmarshal(body, &summary); err != nil {
				t.Fatal(err)
			}
			status := "incomplete"
			if interrupted {
				status = "interrupted"
			}
			if summary["status"] != status || summary["output_error"] == nil || result.Summary["status"] != status {
				t.Fatal("stale successful publication", summary)
			}
		})
	}
}

// The second Finish repairs output delivery; a signal during that repair requires
// a third Finish. Every failed publication/recovery must survive in batch state.
func TestLateFinalizationRetainsSummaryAndFailureHistory(t *testing.T) {
	for _, tc := range []struct {
		name                                                      string
		failRestore, cancelRestore, failThird, successfulDelivery bool
	}{
		{name: "delivery-and-final-write"},
		{name: "delivery-and-restore", failRestore: true},
		{name: "cancel-during-restore", failRestore: true, cancelRestore: true},
		{name: "third-finalization-failure", failRestore: true, cancelRestore: true, failThird: true},
		{name: "cancel-after-successful-delivery", failRestore: true, successfulDelivery: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			writes := 0
			store, err := report.New(filepath.Join(t.TempDir(), "reports"), report.Options{Write: func(ctx context.Context, path string, body []byte) (storage.Receipt, error) {
				if filepath.Base(path) == "summary.json" {
					writes++
					if writes == 4 {
						if _, err := storage.Write(ctx, path, body); err != nil {
							t.Fatal(err)
						}
						return storage.Receipt{State: "committed", Phase: "directory_sync"}, errors.New("final write failed")
					}
					if writes == 5 {
						if tc.cancelRestore {
							cancel()
						}
						if tc.failRestore {
							return storage.Receipt{State: "not_committed", Phase: "write"}, errors.New("restore failed")
						}
					}
					if writes == 7 && tc.failThird {
						return storage.Receipt{State: "not_committed", Phase: "write"}, errors.New("third finalization failed")
					}
				}
				return storage.Write(ctx, path, body)
			}})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			result := Run(ctx, Options{Workers: 1, Requested: []wireless.Record{{"host": "192.0.2.10"}}, Journal: store,
				Execute: func(ctx context.Context, _ int, targets []wireless.Record, run func(context.Context, wireless.Record) error) error {
					return run(ctx, targets[0])
				},
				Collect: func(_ context.Context, target wireless.Record) (wireless.Snapshot, error) {
					calls++
					observation, err := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Collection, Status: wireless.Complete})
					if err != nil {
						return wireless.Snapshot{}, err
					}
					return wireless.NewSnapshot(wireless.SnapshotOptions{Target: target, Dimensions: map[wireless.Dimension]wireless.Observation{wireless.Collection: observation}, ObservedAt: time.Now()})
				},
				Events: Events{Deliver: func(wireless.Record) error {
					if tc.successfulDelivery {
						cancel()
						return nil
					}
					return io.ErrClosedPipe
				}},
			})
			if calls != 1 || !result.Failed || !result.FinalizationFailed || result.RecoveryFailed != tc.failRestore || result.Interrupted != (tc.cancelRestore || tc.successfulDelivery) {
				t.Fatalf("lost finalization outcome: collections=%d result=%+v", calls, result)
			}
			if tc.cancelRestore && !tc.failThird {
				if result.Summary["status"] != "interrupted" || result.Summary["report_error"] == nil || result.Summary["output_error"] == nil {
					t.Fatal("last confirmed summary missing", result.Summary)
				}
			} else if result.Summary != nil {
				t.Fatal("failed publication retained confirmed summary", result.Summary)
			}
			body, err := os.ReadFile(filepath.Join(store.Directory(), "summary.json"))
			if errors.Is(err, os.ErrNotExist) {
				if !tc.failRestore {
					t.Fatal("confirmed checkpoint missing")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var summary wireless.Record
			if err := json.Unmarshal(body, &summary); err != nil || summary["status"] == "complete" {
				t.Fatal("untrusted successful summary", summary, err)
			}
		})
	}
}
