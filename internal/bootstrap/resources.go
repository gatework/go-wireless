package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/unix"
)

var errInsufficientResources = errors.New("wireless: insufficient file descriptors")

// The limit is process-wide, so read/raise is one critical section even when
// callers run independent Runner instances.
var resourceLimitMu sync.Mutex

// PrepareResources raises the open file limit enough for active concurrent
// devices.
func PrepareResources(ctx context.Context, active int) error {
	return prepareFileLimit(ctx, active, unix.Getrlimit, unix.Setrlimit)
}

func prepareFileLimit(ctx context.Context, active int, get, set func(int, *unix.Rlimit) error) error {
	if active < 0 || active > 500 {
		return ErrOptions
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if active == 0 {
		return nil
	}
	resourceLimitMu.Lock()
	defer resourceLimitMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	required := uint64(128 + 16*active)
	failure := fmt.Errorf("%w: %d active devices require %d descriptors", errInsufficientResources, active, required)
	var limit unix.Rlimit
	if err := get(unix.RLIMIT_NOFILE, &limit); err != nil {
		return failure
	}
	if limit.Cur >= required {
		return nil
	}
	if limit.Max < required {
		return failure
	}
	limit.Cur = required
	if err := set(unix.RLIMIT_NOFILE, &limit); err != nil {
		return failure
	}
	return nil
}
