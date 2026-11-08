package bootstrap

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrepareResourcesUsesActiveDeviceBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		active      int
		current     uint64
		maximum     uint64
		getError    bool
		setError    bool
		wantCurrent uint64
		wantSets    int
		wantError   bool
	}{
		{name: "empty", maximum: 4096},
		{name: "already sufficient", active: 4, current: 1024, maximum: 4096, wantCurrent: 1024},
		{name: "raise soft limit", active: 2, current: 128, maximum: 4096, wantCurrent: 160, wantSets: 1},
		{name: "maximum batch", active: 500, current: 128, maximum: 10000, wantCurrent: 8128, wantSets: 1},
		{name: "hard limit", active: 4, current: 128, maximum: 160, wantCurrent: 128, wantError: true},
		{name: "get failure", active: 1, getError: true, wantError: true},
		{name: "set failure", active: 1, current: 128, maximum: 4096, wantCurrent: 128, setError: true, wantSets: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limit := unix.Rlimit{Cur: tc.current, Max: tc.maximum}
			gets, sets := 0, 0
			get := func(resource int, value *unix.Rlimit) error {
				gets++
				if resource != unix.RLIMIT_NOFILE {
					t.Fatal("unexpected resource", resource)
				}
				if tc.getError {
					return errors.New("private system error")
				}
				*value = limit
				return nil
			}
			set := func(resource int, value *unix.Rlimit) error {
				sets++
				if resource != unix.RLIMIT_NOFILE || value.Max != tc.maximum {
					t.Fatal("changed resource or hard limit", resource, value)
				}
				if tc.setError {
					return errors.New("private system error")
				}
				limit = *value
				return nil
			}
			err := prepareFileLimit(t.Context(), tc.active, get, set)
			if errors.Is(err, errInsufficientResources) != tc.wantError || sets != tc.wantSets || limit.Cur != tc.wantCurrent {
				t.Fatalf("error=%v gets=%d sets=%d limit=%v", err, gets, sets, limit)
			}
			if tc.active == 0 && gets != 0 {
				t.Fatal("empty batch read process limit")
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("system detail escaped")
			}
		})
	}
}

func TestPrepareResourcesRejectsInvalidOrCancelledBatchBeforeSyscall(t *testing.T) {
	unexpected := func(int, *unix.Rlimit) error { t.Fatal("unexpected syscall"); return nil }
	for _, active := range []int{-1, 501} {
		if err := prepareFileLimit(t.Context(), active, unexpected, unexpected); !errors.Is(err, ErrOptions) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, active := range []int{0, 1} {
		if err := prepareFileLimit(ctx, active, unexpected, unexpected); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestPrepareResourcesSerializesProcessLimitUpdates(t *testing.T) {
	limit := unix.Rlimit{Cur: 128, Max: 4096}
	get := func(_ int, value *unix.Rlimit) error { *value = limit; return nil }
	set := func(_ int, value *unix.Rlimit) error { limit = *value; return nil }
	var group sync.WaitGroup
	for active := 1; active <= 32; active++ {
		group.Go(func() {
			if err := prepareFileLimit(t.Context(), active, get, set); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if limit.Cur != 640 || limit.Max != 4096 {
		t.Fatal("concurrent callers lost a required limit", limit)
	}
}
