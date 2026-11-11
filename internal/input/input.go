package input

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

var ErrInput = errors.New("wireless: invalid input file")

// ContextReader is the cooperative contract for injected blocking input. Reads
// run synchronously and must return when ctx is canceled. Ordinary io.Readers
// must return promptly; the CLI never closes them or spawns a reader goroutine.
// A Runner exclusively reads its input for the duration of a command.
type ContextReader interface {
	ReadContext(ctx context.Context, p []byte) (int, error)
}

func Read(ctx context.Context, input io.Reader, p []byte) (int, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if len(p) == 0 {
		return 0, nil
	}
	var n int
	var e error
	switch input := input.(type) {
	case ContextReader:
		n, e = input.ReadContext(ctx, p)
	case *os.File:
		n, e = readDescriptor(ctx, input, p)
	default:
		n, e = input.Read(p)
	}
	if cancelled := ctx.Err(); cancelled != nil {
		return 0, cancelled
	}
	return n, e
}

type Reader struct {
	Source  io.Reader
	Context context.Context //nolint:containedctx // an io.Reader adapter cannot take ctx per Read
}

func (r Reader) Read(p []byte) (int, error) { return Read(r.Context, r.Source, p) }
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

// Document reads a required evidence file. Configuration limits belong to the
// connector config package; captures and plans retain their own size semantics.
func Document(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrInput
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrInput
	}
	body, err := io.ReadAll(Reader{Source: file, Context: ctx})
	if cancelled := ctx.Err(); cancelled != nil {
		return nil, cancelled
	}
	if err != nil {
		return nil, ErrInput
	}
	return body, nil
}
