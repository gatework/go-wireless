package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gatework/go-net-connector/storage"
	"github.com/gatework/go-wireless/report"
)

type cancelRead struct {
	cancel context.CancelFunc
	body   string
	closed atomic.Int32
}

func (r *cancelRead) Read(p []byte) (int, error) { n := copy(p, r.body); r.cancel(); return n, io.EOF }
func (r *cancelRead) Close() error               { r.closed.Add(1); return nil }
func TestReviewParseCancellationDuringBorrowedReadDiscardsFacts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := &cancelRead{cancel: cancel, body: "sysname WLC\nreturn\n"}
	var out, diagnostic bytes.Buffer
	code := Run(ctx, []string{"parse", "--complete", "--json"}, Dependencies{Environment: map[string]string{}, Input: input, Output: &out, Error: &diagnostic})
	if code != 130 || out.Len() != 0 || input.closed.Load() != 0 {
		t.Fatal(code, out.String(), diagnostic.String(), input.closed.Load())
	}
}
func TestReviewInitCancellationDuringPromptDoesNotCreateFiles(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := &cancelRead{cancel: cancel, body: "yes\n"}
	var out, diagnostic bytes.Buffer
	code := Run(ctx, []string{"collect", "--interactive"}, Dependencies{Environment: map[string]string{}, Input: input, Output: &out, Error: &diagnostic, Discovery: discovery(dir), Terminal: func(any) bool { return true }})
	if code != 130 || out.Len() != 0 || input.closed.Load() != 0 {
		t.Fatal(code, out.String(), diagnostic.String(), input.closed.Load())
	}
	files, e := os.ReadDir(dir)
	if e != nil || len(files) != 0 {
		t.Fatal(files, e)
	}
}
func TestReviewStdoutRecoveryInterruptRetains130AndCheckpoint(t *testing.T) {
	for _, failure := range []string{"none", "once", "permanent"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "wireless.yml"), "directory: reports\ndimensions: [access_points]\ndevices: [{host: '192.0.2.10'}]\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var closed atomic.Int32
			var diagnostic bytes.Buffer
			stdout := &failingWriter{always: true}
			jsonWrites := 0
			code := Run(ctx, []string{"--json"}, Dependencies{Environment: map[string]string{"NC_DEVICE_USERNAME": "fixture"}, Input: panicReader{}, Output: stdout, Error: &diagnostic, Discovery: discovery(dir), SessionFactory: fixtureFactory(&closed), ReportOptions: report.Options{Write: func(ctx context.Context, path string, b []byte) (storage.Receipt, error) {
				if strings.HasSuffix(path, "summary.json") {
					jsonWrites++
					if jsonWrites == 3 {
						cancel()
					}
					if jsonWrites == 3 && failure == "once" || jsonWrites >= 3 && failure == "permanent" {
						return storage.Receipt{State: "not_committed", Phase: "write"}, errors.New("private recovery")
					}
				}
				return storage.Write(ctx, path, b)
			}}})
			if code != 130 || ctx.Err() == nil || closed.Load() != 1 {
				t.Fatal(code, ctx.Err(), closed.Load(), diagnostic.String())
			}
			paths, _ := filepath.Glob(filepath.Join(dir, "reports/*/summary.json"))
			if failure == "permanent" {
				if len(paths) != 0 {
					b, _ := os.ReadFile(paths[0])
					var doc map[string]any
					_ = json.Unmarshal(b, &doc)
					if doc["status"] == "complete" {
						t.Fatal("stale success", string(b))
					}
				}
				return
			}
			if len(paths) != 1 {
				t.Fatal(paths)
			}
			b, _ := os.ReadFile(paths[0])
			var summary map[string]any
			_ = json.Unmarshal(b, &summary)
			if summary["status"] != "interrupted" || summary["output_error"] == nil {
				t.Fatal(string(b))
			}
		})
	}
}
func TestReviewSubprocessParseSIGINTWithOpenStdin(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess build")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "wireless")
	if b, e := exec.Command("go", "build", "-o", binary, "../cmd/wireless").CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	if b, e := exec.Command(binary, "--version").CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	command := exec.Command(binary, "parse", "--complete", "--json")
	input, e := command.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	var out, diagnostic bytes.Buffer
	command.Stdout = &out
	command.Stderr = &diagnostic
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	defer func() { _ = input.Close(); _ = command.Process.Kill() }()
	if _, e = io.WriteString(input, "sysname WLC\nreturn\n"); e != nil {
		t.Fatal(e)
	}
	// Warm executable, then confirm it remains in its input phase before SIGINT.
	select {
	case e := <-done:
		t.Fatal("exited before cancellation", e)
	case <-time.After(250 * time.Millisecond):
	}
	if e = command.Process.Signal(os.Interrupt); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		var exit *exec.ExitError
		if !errors.As(e, &exit) || exit.ExitCode() != 130 {
			t.Fatal("exit", e, out.String(), diagnostic.String())
		}
	case <-time.After(2 * time.Second):
		_ = input.Close()
		_ = command.Process.Kill()
		<-done
		t.Fatal("SIGINT did not unblock stdin")
	}
	if out.Len() != 0 {
		t.Fatal("facts emitted after cancellation", out.String())
	}
}

type cooperativeInput struct {
	started        chan struct{}
	active, closed atomic.Int32
}

func (r *cooperativeInput) Read([]byte) (int, error) { panic("blocking input must use ReadContext") }
func (r *cooperativeInput) ReadContext(ctx context.Context, _ []byte) (int, error) {
	r.active.Add(1)
	defer r.active.Add(-1)
	close(r.started)
	<-ctx.Done()
	return 0, ctx.Err()
}
func (r *cooperativeInput) Close() error { r.closed.Add(1); return nil }
func TestReviewCooperativeBlockingInputJoinsWithoutClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := &cooperativeInput{started: make(chan struct{})}
	var out, diagnostic bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"parse", "--json"}, Dependencies{Environment: map[string]string{}, Input: input, Output: &out, Error: &diagnostic})
	}()
	<-input.started
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("cooperative reader not joined")
	}
	if input.active.Load() != 0 || input.closed.Load() != 0 || out.Len() != 0 {
		t.Fatal(input.active.Load(), input.closed.Load(), out.String())
	}
}
func TestReviewBorrowedDescriptorPromptCancellationKeepsInputOpen(t *testing.T) {
	dir := t.TempDir()
	input, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, diagnostic bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"collect", "--interactive"}, Dependencies{Environment: map[string]string{}, Input: input, Output: &out, Error: &diagnostic, Discovery: discovery(dir), Terminal: func(any) bool { return true }})
	}()
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatal(code, diagnostic.String())
		}
	case <-time.After(time.Second):
		t.Fatal("descriptor prompt did not observe cancellation")
	}
	if _, e = writer.Write([]byte("still owned by caller")); e != nil {
		t.Fatal("CLI closed borrowed input", e)
	}
	files, e := os.ReadDir(dir)
	if e != nil || len(files) != 0 {
		t.Fatal(files, e)
	}
}
