package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// ContextReader is the cooperative contract for injected blocking input. Reads
// run synchronously and must return when ctx is canceled. Ordinary io.Readers
// must return promptly; the CLI never closes them or spawns a reader goroutine.
// A Runner exclusively reads its input for the duration of a command.
type ContextReader interface {
	ReadContext(context.Context, []byte) (int, error)
}

// ReadInput uses bounded descriptor polling for *os.File input (including the
// executable's borrowed stdin), without closing it or changing descriptor flags.
// Other blocking injected readers must implement ContextReader.
func (r *Runner) ReadInput(ctx context.Context, p []byte) (int, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if len(p) == 0 {
		return 0, nil
	}
	var n int
	var e error
	switch input := r.Input.(type) {
	case ContextReader:
		n, e = input.ReadContext(ctx, p)
	case *os.File:
		n, e = readDescriptor(ctx, input, p)
	default:
		n, e = r.Input.Read(p)
	}
	if cancelled := ctx.Err(); cancelled != nil {
		return 0, cancelled
	}
	return n, e
}

// ReadLine reads one prompt response without prefetching subsequent input.
func (r *Runner) ReadLine(ctx context.Context) (string, error) {
	var line strings.Builder
	var one [1]byte
	for {
		n, e := r.ReadInput(ctx, one[:])
		if n > 0 {
			line.WriteByte(one[0])
			if one[0] == '\n' {
				return line.String(), nil
			}
		}
		if e != nil {
			return line.String(), e
		}
		if n == 0 {
			return line.String(), io.ErrNoProgress
		}
	}
}

type inputReader struct {
	runner *Runner
	ctx    context.Context
}

func (r inputReader) Read(p []byte) (int, error) { return r.runner.ReadInput(r.ctx, p) }
func readDescriptor(ctx context.Context, file *os.File, p []byte) (int, error) {
	raw, e := file.SyscallConn()
	if e != nil {
		return 0, e
	}
	for {
		if e = ctx.Err(); e != nil {
			return 0, e
		}
		n := 0
		var readErr error
		e = raw.Control(func(fd uintptr) {
			readiness := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			ready, pollErr := unix.Poll(readiness, 50)
			if pollErr != nil {
				readErr = pollErr
				return
			}
			if ready == 0 {
				return
			}
			if readiness[0].Revents&unix.POLLNVAL != 0 {
				readErr = os.ErrClosed
				return
			}
			// This command is the exclusive reader. Read only after readiness; no
			// descriptor mutation or interrupting goroutine is required for stdin.
			if readiness[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0 {
				n, readErr = unix.Read(int(fd), p)
				if n == 0 && readErr == nil {
					readErr = io.EOF
				}
			}
		})
		if e != nil {
			return 0, e
		}
		if n > 0 {
			return n, readErr
		}
		if readErr != nil && !errors.Is(readErr, unix.EINTR) && !errors.Is(readErr, unix.EAGAIN) {
			return 0, readErr
		}
	}
}
