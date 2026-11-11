package wireless_test

import (
	"errors"
	"os"
	"os/exec"
	"runtime/debug"
	"testing"

	wireless "github.com/gatework/go-wireless"
)

func TestFingerprintRejectsCycles(t *testing.T) {
	t.Parallel()
	if os.Getenv("WIRELESS_FINGERPRINT_CYCLE_CHILD") == "1" {
		debug.SetMaxStack(64 << 10)
		object := wireless.Record{}
		object["self"] = object
		list := make([]any, 1)
		list[0] = list
		type node struct{ Next *node }
		pointer := &node{}
		pointer.Next = pointer
		for _, value := range []any{object, list, pointer} {
			if _, err := wireless.Fingerprint(value); !errors.Is(err, wireless.ErrDocument) {
				t.Fatalf("cycle returned %v", err)
			}
		}
		shared := wireless.Record{"label": "leaf"}
		if _, err := wireless.Fingerprint(wireless.Record{"left": shared, "right": shared}); err != nil {
			t.Fatal(err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestFingerprintRejectsCycles$")
	child.Env = append(os.Environ(), "WIRELESS_FINGERPRINT_CYCLE_CHILD=1")
	if err := child.Run(); err != nil {
		t.Fatalf("cycle rejection subprocess: %v", err)
	}
}
