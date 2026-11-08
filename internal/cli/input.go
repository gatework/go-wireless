package cli

import (
	"context"
	"io"
	"strings"

	"github.com/gatework/go-wireless/internal/input"
)

// ReadInput uses bounded descriptor polling for *os.File input (including the
// executable's borrowed stdin), without closing it or changing descriptor flags.
// Other blocking injected readers must implement ContextReader.
func (r *Runner) ReadInput(ctx context.Context, p []byte) (int, error) {
	return input.Read(ctx, r.Input, p)
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
