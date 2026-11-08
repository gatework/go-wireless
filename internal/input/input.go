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
	ReadContext(context.Context, []byte) (int, error)
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
	Context context.Context
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

func ReadFile(ctx context.Context, path string, required, allowMissing bool, limit int64) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) && (!required || allowMissing) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrInput
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || limit > 0 && info.Size() > limit {
		return nil, false, ErrInput
	}
	var reader io.Reader = Reader{Source: file, Context: ctx}
	if limit > 0 {
		reader = io.LimitReader(reader, limit+1)
	}
	body, err := io.ReadAll(reader)
	if cancelled := ctx.Err(); cancelled != nil {
		return nil, false, cancelled
	}
	if err != nil || limit > 0 && int64(len(body)) > limit {
		return nil, false, ErrInput
	}
	return body, true, nil
}

func Document(ctx context.Context, path string) ([]byte, error) {
	body, _, err := ReadFile(ctx, path, true, false, 0)
	return body, err
}
