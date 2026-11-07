package report

import (
	"bytes"
	"context"
	"errors"
	"github.com/gatework/go-net-connector/storage"
	wireless "github.com/gatework/go-wireless"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutionFinalPublicationFailurePreservesCheckpoint(t *testing.T) {
	for _, recoveryFail := range []bool{false, true} {
		t.Run(map[bool]string{true: "recovery_failure", false: "recovered"}[recoveryFail], func(t *testing.T) {
			writes := 0
			r, e := New(filepath.Join(t.TempDir(), "private"), Options{Write: func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
				writes++
				if writes == 2 {
					receipt, e := storage.Write(ctx, path, b)
					if e != nil {
						return receipt, e
					}
					return storage.Receipt{State: "committed", Phase: "directory_fsync"}, errors.New("fsync")
				}
				if writes == 3 && recoveryFail {
					return storage.Receipt{}, errors.New("recovery")
				}
				return storage.Write(ctx, path, b)
			}})
			if e != nil {
				t.Fatal(e)
			}
			e = r.FinalizeExecution(context.Background(), wireless.Record{"status": "configuration_verified", "steps": []wireless.Record{{"index": 0, "status": "acknowledged"}}, "readback_verified": true})
			if e == nil {
				t.Fatal("lost publication failure")
			}
			b, e := os.ReadFile(filepath.Join(r.Directory(), "execution-checkpoint.json"))
			if e != nil || !bytes.Contains(b, []byte(`"status": "uncertain"`)) || !bytes.Contains(b, []byte(`"readback_verified": true`)) {
				t.Fatal(string(b), e)
			}
			b, e = os.ReadFile(filepath.Join(r.Directory(), "execution.json"))
			if e == nil && bytes.Contains(b, []byte(`"status": "configuration_verified"`)) {
				t.Fatal("durable success falsely visible", string(b))
			}
		})
	}
}

func TestExecutionRequiresDurableReceipt(t *testing.T) {
	r, e := New(filepath.Join(t.TempDir(), "private"), Options{Write: func(context.Context, string, []byte) (storage.Receipt, error) {
		return storage.Receipt{State: "committed", Phase: "directory_sync"}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.WriteExecution(context.Background(), wireless.Record{"status": "executing", "pending_command": 0}); e == nil {
		t.Fatal("unconfirmed durability accepted")
	}
}

func TestExecutionLateCancellationCannotLeaveVerifiedPublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	r, e := New(filepath.Join(t.TempDir(), "private"), Options{Write: func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
		writes++
		if writes >= 3 {
			return storage.Receipt{}, errors.New("recovery")
		}
		receipt, e := storage.Write(ctx, path, b)
		if writes == 2 {
			cancel()
		}
		return receipt, e
	}})
	if e != nil {
		t.Fatal(e)
	}
	e = r.FinalizeExecution(ctx, wireless.Record{"status": "configuration_verified", "readback_verified": true, "steps": []wireless.Record{{"index": 0, "status": "acknowledged"}}})
	if !errors.Is(e, context.Canceled) {
		t.Fatal("late cancellation lost", e)
	}
	b, e := os.ReadFile(filepath.Join(r.Directory(), "execution.json"))
	if e == nil && bytes.Contains(b, []byte(`"status": "configuration_verified"`)) {
		t.Fatal("cancelled final still appears verified")
	}
	if _, e = os.Stat(filepath.Join(r.Directory(), "execution-checkpoint.json")); e != nil {
		t.Fatal("intent checkpoint lost", e)
	}
}
