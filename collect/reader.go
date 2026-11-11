package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	wireless "github.com/gatework/go-wireless"
)

// Receipt retains output before decoding and validates one command at the Reader boundary.
// Body and Raw are deliberately explicit accessors; formatting never exposes them.
type Receipt struct {
	command, raw, body string
	steps              int
	success, valid     bool
	attempted          bool
	failure            *wireless.FailureError
}

// ReceiptOptions describes one session command result for NewReceipt.
type ReceiptOptions struct {
	Command, Raw, Body string
	Attempted          bool
	Steps              int
	Success            bool
	Failure            *wireless.FailureError
}

// NewReceipt returns a valid receipt.
func NewReceipt(o ReceiptOptions) Receipt {
	return Receipt{command: o.Command, raw: o.Raw, body: o.Body, steps: o.Steps, success: o.Success, valid: true, attempted: o.Attempted, failure: wireless.CloneFailure(o.Failure)}
}

// Raw returns the raw output, prompts included.
func (r Receipt) Raw() string { return r.raw }

// Body returns the response without the echoed command and prompt.
func (r Receipt) Body() string { return r.body }
func (r Receipt) String() string {
	return fmt.Sprintf("wireless.Receipt{steps:%d success:%t}", r.steps, r.success)
}

// GoString formats metadata only, never output.
func (r Receipt) GoString() string { return r.String() }

// MarshalJSON encodes metadata only, never output.
func (r Receipt) MarshalJSON() ([]byte, error) {
	return json.Marshal(wireless.Record{"steps": r.steps, "success": r.success})
}

// Session is borrowed: Execute must never connect, retry, or close another owner's lease.
type Session interface {
	State(ctx context.Context) (bool, error)
	Execute(ctx context.Context, command string) (Receipt, error)
}

// Clock returns the current time.
type Clock func() time.Time

// Reader serializes borrowed command reads, validates receipts and preserves first failure.
// It never connects, retries or closes the supplied session.
type Reader struct {
	session Session
	clock   Clock
	busy    atomic.Bool
	failure atomic.Pointer[wireless.FailureError]
}

// NewReader returns a reader over a borrowed session; a nil clock means
// time.Now.
func NewReader(s Session, clock Clock) (*Reader, error) {
	if s == nil {
		return nil, errors.New("wireless: session required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Reader{session: s, clock: clock}, nil
}
func (r *Reader) String() string { return "wireless.Reader" }

// GoString formats metadata only.
func (r *Reader) GoString() string { return r.String() }

// Failure returns the first failure that made the session unusable, checking
// the session state.
func (r *Reader) Failure(ctx context.Context) *wireless.FailureError {
	if !r.busy.CompareAndSwap(false, true) {
		return wireless.NewFailure("session_busy", "session")
	}
	defer r.busy.Store(false)
	return r.failureCheck(ctx)
}
func (r *Reader) Read(ctx context.Context, command string) (wireless.Capture, error) {
	if !wireless.ValidCommand(command) {
		return wireless.Capture{}, errors.New("wireless: invalid query")
	}
	if !r.busy.CompareAndSwap(false, true) {
		return wireless.Capture{}, wireless.NewFailure("session_busy", "execute")
	}
	defer r.busy.Store(false)
	start := r.clock()
	if start.IsZero() {
		return wireless.Capture{}, errors.New("wireless: invalid clock")
	}
	if f := r.failureCheck(ctx); f != nil {
		return wireless.NewCapture(wireless.CaptureOptions{Command: command, StartedAt: start, CompletedAt: r.clock(), Failure: f, Skipped: true})
	}
	if e := ctx.Err(); e != nil {
		f := wireless.FailureFrom(e, "command_aborted", "execute")
		r.failure.CompareAndSwap(nil, f)
		return wireless.NewCapture(wireless.CaptureOptions{Command: command, StartedAt: start, CompletedAt: r.clock(), Failure: f, Skipped: true})
	}
	receipt, e := r.session.Execute(ctx, command)
	evidence, body, failure := interpretReceipt(command, receipt, wireless.FailureFrom(e, "command_execution_failed", "execute"))
	evidence, failure = r.stampEvidence(evidence, failure)
	stateFailure := r.state(ctx)
	if stateFailure != nil {
		first := failure
		if first == nil {
			first = stateFailure
		}
		r.failure.CompareAndSwap(nil, first)
	}
	if failure != nil {
		body = nil
	}
	return wireless.NewCapture(wireless.CaptureOptions{Command: command, Body: body, StartedAt: start, CompletedAt: r.clock(), Evidence: evidence, Failure: failure})
}
func (r *Reader) state(ctx context.Context) *wireless.FailureError {
	ok, e := r.session.State(ctx)
	if e != nil {
		return wireless.FailureFrom(e, "session_state_failed", "session")
	}
	if !ok {
		return wireless.NewFailure("session_closed", "session")
	}
	return nil
}
func (r *Reader) failureCheck(ctx context.Context) *wireless.FailureError {
	if f := r.failure.Load(); f != nil {
		return wireless.CloneFailure(f)
	}
	f := r.state(ctx)
	if f != nil {
		r.failure.CompareAndSwap(nil, f)
	}
	return wireless.CloneFailure(r.failure.Load())
}

// interpretReceipt checks a command receipt against the command it answers.
// It returns the output evidence, the body of a successful command and the
// failure, keeping an execution failure in preference to a receipt defect.
func interpretReceipt(command string, receipt Receipt, failure *wireless.FailureError) (evidence []wireless.Record, body *string, _ *wireless.FailureError) {
	if !receipt.valid {
		if failure == nil {
			failure = wireless.NewFailure("invalid_command_result", "response")
		}
		return nil, nil, failure
	}
	if receipt.attempted {
		sum := sha256.Sum256([]byte(receipt.raw))
		evidence = []wireless.Record{{"command": command, "sha256": hex.EncodeToString(sum[:]), "bytes": len(receipt.raw)}}
	}
	if failure == nil && (receipt.command != command || receipt.success && (!receipt.attempted || receipt.steps != 1 || receipt.failure != nil)) {
		failure = wireless.NewFailure("invalid_command_result", "response")
	}
	if failure != nil {
		return evidence, nil, failure
	}
	if receipt.success {
		value := receipt.body
		return evidence, &value, nil
	}
	failure = wireless.CloneFailure(receipt.failure)
	if failure == nil {
		failure = wireless.NewFailure("command_failed", "execute")
	}
	return evidence, nil, failure
}

// stampEvidence records when the output was observed; without a valid clock
// the evidence is dropped and the result is invalid.
func (r *Reader) stampEvidence(evidence []wireless.Record, failure *wireless.FailureError) ([]wireless.Record, *wireless.FailureError) {
	if len(evidence) == 0 {
		return evidence, failure
	}
	observed := r.clock()
	if observed.IsZero() {
		if failure == nil {
			failure = wireless.NewFailure("invalid_command_result", "response")
		}
		return nil, failure
	}
	evidence[0]["observed_at"] = observed.UTC().Format("2006-01-02T15:04:05.000000Z")
	return evidence, failure
}
