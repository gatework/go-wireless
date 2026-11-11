package wireless

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// FailureError contains only validated diagnostic vocabulary, never an external error.
type FailureError struct{ code, stage string }

var failureCodes = strings.Fields(`invalid_command_result command_failed command_execution_failed command_processing_failed command_aborted session_closed session_state_failed session_busy session_scope_ended connection_failed connection_closed authentication_error transport_error prompt_error login_timeout command_timeout write_timeout output_limit_exceeded script_output_limit_exceeded device_error log_error receipt_callback_failed step_callback_failed prepare_failed after_command_failed invalid_output_encoding cleanup_failed session_observer_failed cancelled deadline_exceeded controller_session_lost controller_prompt_mismatch ap_address_unavailable ap_not_online ap_authentication_failed ap_connection_failed ap_login_not_confirmed ap_session_lost ap_response_missing ap_exit_failed session_close_failed ap_collection_failed ap_session_setup_failed invalid_ap_credentials invalid_ap_connection invalid_ap_session invalid_ap_profile invalid_ap_query`)

func NewFailure(code, stage string) *FailureError {
	if !slices.Contains(failureCodes, code) {
		code = "invalid_command_result"
	}
	switch stage {
	case "execute", "response", "parse", "session", "connect", "prepare", "collect", "close", "ap_setup", "login", "cleanup":
	default:
		stage = ""
	}
	return &FailureError{code, stage}
}
func (f *FailureError) Code() string {
	if f == nil {
		return ""
	}
	return f.code
}
func (f *FailureError) Stage() string {
	if f == nil {
		return ""
	}
	return f.stage
}
func (f *FailureError) Error() string    { return "wireless: " + f.Code() }
func (f *FailureError) GoString() string { return f.Error() }
func (f *FailureError) ToMap() Record {
	if f == nil {
		return nil
	}
	r := Record{"code": f.code}
	if f.stage != "" {
		r["stage"] = f.stage
	}
	return r
}
func safeFailure(err error, fallback, stage string) *FailureError {
	if err == nil {
		return nil
	}
	if f, ok := errors.AsType[*FailureError](err); ok {
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

func cloneFailure(f *FailureError) *FailureError {
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
	Failure                *FailureError
	Skipped                bool
}

// Capture owns validated query evidence and an explicit optional response body.
// Accessors detach evidence; formatting and JSON never reveal body text.
// The zero value is invalid and cannot prove an attempted command.
type Capture struct {
	command, body      string
	started, completed time.Time
	evidence           []Record
	failure            *FailureError
	skipped, valid     bool
}

func NewCapture(o CaptureOptions) (Capture, error) {
	if !ValidCommand(o.Command) || o.StartedAt.IsZero() || o.CompletedAt.IsZero() || (o.Failure == nil) != (o.Body != nil) || o.Skipped && (o.Failure == nil || len(o.Evidence) > 0) {
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
func (c Capture) Failure() *FailureError { return cloneFailure(c.failure) }

// Valid reports whether the capture was constructed with checked evidence.
func (c Capture) Valid() bool   { return c.valid }
func (c Capture) Success() bool { return c.valid && c.failure == nil }
func (c Capture) Skipped() bool { return c.skipped }
func (c Capture) String() string {
	return fmt.Sprintf("wireless.Capture{success:%t skipped:%t}", c.Success(), c.skipped)
}
func (c Capture) GoString() string { return c.String() }
func (c Capture) ToMap() Record {
	return Record{"command": c.command, "started_at": formatTime(&c.started), "completed_at": formatTime(&c.completed), "evidence": c.Evidence(), "failure": c.failure.ToMap(), "skipped": c.skipped}
}
func (c Capture) MarshalJSON() ([]byte, error) { return MarshalJSON(c.ToMap()) }
