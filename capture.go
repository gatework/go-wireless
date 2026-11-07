package wireless

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// Failure contains only validated diagnostic vocabulary, never an external error.
type Failure struct{ code, stage string }

var failureCodes = strings.Fields(`invalid_command_result command_failed command_execution_failed command_processing_failed command_aborted session_closed session_state_failed session_busy session_scope_ended connection_failed connection_closed authentication_error transport_error prompt_error login_timeout command_timeout write_timeout output_limit_exceeded script_output_limit_exceeded device_error log_error receipt_callback_failed step_callback_failed prepare_failed after_command_failed invalid_output_encoding cleanup_failed session_observer_failed cancelled deadline_exceeded controller_session_lost controller_prompt_mismatch ap_address_unavailable ap_not_online ap_authentication_failed ap_connection_failed ap_login_not_confirmed ap_session_lost ap_response_missing ap_exit_failed session_close_failed ap_collection_failed ap_session_setup_failed invalid_ap_credentials invalid_ap_connection invalid_ap_session`)

func NewFailure(code, stage string) *Failure {
	if !slices.Contains(failureCodes, code) {
		code = "invalid_command_result"
	}
	switch stage {
	case "execute", "response", "parse", "session", "connect", "prepare", "collect", "close", "ap_setup", "login", "cleanup":
	default:
		stage = ""
	}
	return &Failure{code, stage}
}
func (f *Failure) Code() string {
	if f == nil {
		return ""
	}
	return f.code
}
func (f *Failure) Stage() string {
	if f == nil {
		return ""
	}
	return f.stage
}
func (f *Failure) Error() string    { return "wireless: " + f.Code() }
func (f *Failure) GoString() string { return f.Error() }
func (f *Failure) ToMap() Record {
	if f == nil {
		return nil
	}
	r := Record{"code": f.code}
	if f.stage != "" {
		r["stage"] = f.stage
	}
	return r
}
func safeFailure(err error, fallback, stage string) *Failure {
	if err == nil {
		return nil
	}
	if f, ok := errors.AsType[*Failure](err); ok {
		return NewFailure(f.Code(), stage)
	}
	if errors.Is(err, context.Canceled) {
		return NewFailure("cancelled", stage)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewFailure("deadline_exceeded", stage)
	}
	return NewFailure(fallback, stage)
}
func validCommand(s string) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

// Receipt retains output before decoding and validates one command at the Reader boundary.
// Body and Raw are deliberately explicit accessors; formatting never exposes them.
type Receipt struct {
	command, raw, body string
	steps              int
	success, valid     bool
	attempted          bool
	failure            *Failure
}
type ReceiptOptions struct {
	Command, Raw, Body string
	Attempted          bool
	Steps              int
	Success            bool
	Failure            *Failure
}

func NewReceipt(o ReceiptOptions) Receipt {
	return Receipt{command: o.Command, raw: o.Raw, body: o.Body, steps: o.Steps, success: o.Success, valid: true, attempted: o.Attempted, failure: cloneFailure(o.Failure)}
}
func (r Receipt) Raw() string  { return r.raw }
func (r Receipt) Body() string { return r.body }
func (r Receipt) String() string {
	return fmt.Sprintf("wireless.Receipt{steps:%d success:%t}", r.steps, r.success)
}
func (r Receipt) GoString() string { return r.String() }
func (r Receipt) MarshalJSON() ([]byte, error) {
	return json.Marshal(Record{"steps": r.steps, "success": r.success})
}
func cloneFailure(f *Failure) *Failure {
	if f == nil {
		return nil
	}
	return NewFailure(f.code, f.stage)
}

type CaptureOptions struct {
	Command                string
	Body                   *string
	StartedAt, CompletedAt time.Time
	Evidence               []Record
	Failure                *Failure
	Skipped                bool
}
type Capture struct {
	command, body      string
	started, completed time.Time
	evidence           []Record
	failure            *Failure
	skipped, valid     bool
}

func NewCapture(o CaptureOptions) (Capture, error) {
	if !validCommand(o.Command) || o.StartedAt.IsZero() || o.CompletedAt.IsZero() || (o.Failure == nil) != (o.Body != nil) || o.Skipped && (o.Failure == nil || len(o.Evidence) > 0) {
		return Capture{}, errors.New("wireless: invalid capture")
	}
	evidence, e := copyRecords(o.Evidence)
	if e != nil {
		return Capture{}, errors.New("wireless: invalid capture evidence")
	}
	body := ""
	if o.Body != nil {
		body = *o.Body
	}
	return Capture{o.Command, body, o.StartedAt, o.CompletedAt, evidence, cloneFailure(o.Failure), o.Skipped, true}, nil
}
func (c Capture) Command() string        { return c.command }
func (c Capture) Body() string           { return c.body }
func (c Capture) StartedAt() time.Time   { return c.started }
func (c Capture) CompletedAt() time.Time { return c.completed }
func (c Capture) Evidence() []Record     { r, _ := copyRecords(c.evidence); return r }
func (c Capture) Failure() *Failure      { return cloneFailure(c.failure) }
func (c Capture) Success() bool          { return c.valid && c.failure == nil }
func (c Capture) Skipped() bool          { return c.skipped }
func (c Capture) String() string {
	return fmt.Sprintf("wireless.Capture{success:%t skipped:%t}", c.Success(), c.skipped)
}
func (c Capture) GoString() string { return c.String() }
func (c Capture) ToMap() Record {
	return Record{"command": c.command, "started_at": formatTime(&c.started), "completed_at": formatTime(&c.completed), "evidence": c.Evidence(), "failure": c.failure.ToMap(), "skipped": c.skipped}
}
func (c Capture) MarshalJSON() ([]byte, error) { return MarshalJSON(c.ToMap()) }

// Session is borrowed: Execute must never connect, retry, or close another owner's lease.
type Session interface {
	State(context.Context) (bool, error)
	Execute(context.Context, string) (Receipt, error)
}
type Clock func() time.Time
type Reader struct {
	session Session
	clock   Clock
	busy    atomic.Bool
	failure atomic.Pointer[Failure]
}

func NewReader(s Session, clock Clock) (*Reader, error) {
	if s == nil {
		return nil, errors.New("wireless: session required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Reader{session: s, clock: clock}, nil
}
func (r *Reader) String() string   { return "wireless.Reader" }
func (r *Reader) GoString() string { return r.String() }
func (r *Reader) state(ctx context.Context) *Failure {
	ok, e := r.session.State(ctx)
	if e != nil {
		return safeFailure(e, "session_state_failed", "session")
	}
	if !ok {
		return NewFailure("session_closed", "session")
	}
	return nil
}
func (r *Reader) failureCheck(ctx context.Context) *Failure {
	if f := r.failure.Load(); f != nil {
		return cloneFailure(f)
	}
	f := r.state(ctx)
	if f != nil {
		r.failure.CompareAndSwap(nil, f)
	}
	return cloneFailure(r.failure.Load())
}
func (r *Reader) Failure(ctx context.Context) *Failure {
	if !r.busy.CompareAndSwap(false, true) {
		return NewFailure("session_busy", "session")
	}
	defer r.busy.Store(false)
	return r.failureCheck(ctx)
}
func (r *Reader) Read(ctx context.Context, command string) (Capture, error) {
	if !validCommand(command) {
		return Capture{}, errors.New("wireless: invalid query")
	}
	if !r.busy.CompareAndSwap(false, true) {
		return Capture{}, NewFailure("session_busy", "execute")
	}
	defer r.busy.Store(false)
	start := r.clock()
	if start.IsZero() {
		return Capture{}, errors.New("wireless: invalid clock")
	}
	if f := r.failureCheck(ctx); f != nil {
		return NewCapture(CaptureOptions{Command: command, StartedAt: start, CompletedAt: r.clock(), Failure: f, Skipped: true})
	}
	if e := ctx.Err(); e != nil {
		f := safeFailure(e, "command_aborted", "execute")
		r.failure.CompareAndSwap(nil, f)
		return NewCapture(CaptureOptions{Command: command, StartedAt: start, CompletedAt: r.clock(), Failure: f, Skipped: true})
	}
	receipt, e := r.session.Execute(ctx, command)
	failure := safeFailure(e, "command_execution_failed", "execute")
	var evidence []Record
	var body *string
	if receipt.valid {
		if receipt.attempted {
			sum := sha256.Sum256([]byte(receipt.raw))
			evidence = []Record{{"command": command, "sha256": hex.EncodeToString(sum[:]), "bytes": len(receipt.raw)}}
		}
		if receipt.command != command || receipt.success && (!receipt.attempted || receipt.steps != 1 || receipt.failure != nil) {
			if failure == nil {
				failure = NewFailure("invalid_command_result", "response")
			}
		}
		if failure == nil {
			if receipt.success {
				value := receipt.body
				body = &value
			} else {
				failure = cloneFailure(receipt.failure)
				if failure == nil {
					failure = NewFailure("command_failed", "execute")
				}
			}
		}
	} else if failure == nil {
		failure = NewFailure("invalid_command_result", "response")
	}
	if len(evidence) > 0 {
		observed := r.clock()
		if observed.IsZero() {
			evidence = nil
			if failure == nil {
				failure = NewFailure("invalid_command_result", "response")
			}
		} else {
			evidence[0]["observed_at"] = observed.UTC().Format("2006-01-02T15:04:05.000000Z")
		}
	}
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
	return NewCapture(CaptureOptions{Command: command, Body: body, StartedAt: start, CompletedAt: r.clock(), Evidence: evidence, Failure: failure})
}
