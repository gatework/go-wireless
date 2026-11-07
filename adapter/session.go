// Package adapter borrows the frozen native connector; it never implements a second transport.
package adapter

import (
	"context"
	"errors"
	expect "github.com/gatework/go-expect"
	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-wireless"
	"strings"
	"sync/atomic"
)

type Session struct {
	device       *connector.Device
	closeFailure atomic.Pointer[wireless.Failure]
}

func Borrow(device *connector.Device) (*Session, error) {
	if device == nil {
		return nil, errors.New("wireless: device required")
	}
	return &Session{device: device}, nil
}
func (*Session) String() string                        { return "wireless.adapter.Session" }
func (s *Session) GoString() string                    { return s.String() }
func (s *Session) State(context.Context) (bool, error) { return s.device.Connected(), nil }

// Open owns only the explicitly supplied device's connect/callback/close lifecycle.
func Open(device *connector.Device) wireless.OpenSession {
	return func(ctx context.Context, run func(wireless.Session) error) (err error) {
		if device == nil || run == nil {
			return wireless.NewFailure("connection_failed", "connect")
		}
		if e := device.Connect(ctx); e != nil {
			return failure(e, "connection_failed", "connect")
		}
		session, _ := Borrow(device)
		defer func() {
			closeErr := device.Close()
			if err == nil {
				err = ctx.Err()
			}
			if closeErr != nil || session.CloseFailure() != nil {
				err = errors.Join(err, wireless.NewFailure("session_close_failed", "close"))
			}
		}()
		return run(session)
	}
}
func exactPrompt(prompt string) expect.Pattern {
	// Comware can prefix the login prompt with NUL padding and omit it on the
	// next command. Padding is not part of the controller/AP identity.
	prompt = strings.Trim(prompt, " \t\r\n\x00")
	return expect.Line(prompt)
}
func queryAllowed(platform, command string) bool {
	profile, e := wireless.NewProfile(platform, "")
	if e != nil {
		return false
	}
	if command == profile.PreparationCommand() {
		return true
	}
	for _, v := range profile.Commands() {
		if command == v {
			return true
		}
	}
	return false
}
func failure(err error, fallback, stage string) *wireless.Failure {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return wireless.NewFailure("cancelled", stage)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return wireless.NewFailure("deadline_exceeded", stage)
	}
	if errors.Is(err, connector.ErrBusy) {
		return wireless.NewFailure("session_busy", stage)
	}
	if errors.Is(err, connector.ErrScope) {
		return wireless.NewFailure("session_scope_ended", stage)
	}
	var e *connector.Error
	if errors.As(err, &e) {
		return wireless.NewFailure(e.Code, stage)
	}
	return wireless.NewFailure(fallback, stage)
}
func receipt(command string, result connector.Result, err error) wireless.Receipt {
	steps := result.Steps()
	body := ""
	f := failure(err, "command_execution_failed", "execute")
	if err == nil {
		if len(steps) != 1 || steps[0].Command().Text != command || steps[0].Response().RawPrompt() == "" {
			f = wireless.NewFailure("invalid_command_result", "response")
		} else {
			var e error
			body, e = responseBody(steps[0])
			if e != nil {
				f = wireless.NewFailure("invalid_command_result", "response")
			}
		}
	}
	output, outputErr := result.JoinedOutput()
	if outputErr != nil && f == nil {
		f = wireless.NewFailure("invalid_command_result", "response")
	}
	return wireless.NewReceipt(wireless.ReceiptOptions{Command: command, Output: output, Body: body, Steps: len(steps), Success: f == nil, Failure: f})
}
func (s *Session) Execute(ctx context.Context, command string) (wireless.Receipt, error) {
	if !queryAllowed(s.device.Name(), command) {
		return wireless.Receipt{}, wireless.NewFailure("invalid_command_result", "execute")
	}
	var result connector.Result
	var commandErr error
	err, closeErr := s.device.WithOperationCleanup(ctx, func(op *connector.Operation) error {
		if !op.Connected() {
			return connector.ErrConnection
		}
		prompt := strings.TrimSpace(op.RawPrompt())
		if prompt == "" {
			return wireless.NewFailure("controller_prompt_mismatch", "session")
		}
		var e error
		result, e = op.ExecuteBorrowedCommand(ctx, connector.Command{Text: command, Prompt: exactPrompt(prompt), OutputSensitive: true}, connector.ExecuteOptions{Borrowed: true, SkipPrivilege: true, PreserveSessionOnRejection: true, CapturePartialOutput: true})
		commandErr = e
		// Only the original synchronized rejection may keep the borrowed session.
		rejected, ok := e.(*connector.Error)
		preserved := ok && rejected.Code == "device_error" && ctx.Err() == nil && len(result.Steps()) == 1 && result.Steps()[0].Response().RawPrompt() != ""
		if e != nil && !preserved {
			if closeErr := op.Retire(); closeErr != nil {
				s.closeFailure.CompareAndSwap(nil, wireless.NewFailure("session_close_failed", "close"))
			}
		}
		return nil
	})
	if closeErr != nil {
		s.closeFailure.CompareAndSwap(nil, wireless.NewFailure("session_close_failed", "close"))
	}
	// The retained native failure must not be turned into a successful receipt.
	if commandErr != nil {
		err = commandErr
	}
	return receipt(command, result, err), nil
}

// CloseFailure retains the first retirement failure for hosts borrowing a Session.
func (s *Session) CloseFailure() *wireless.Failure {
	f := s.closeFailure.Load()
	if f == nil {
		return nil
	}
	return wireless.NewFailure(f.Code(), f.Stage())
}

// SafeFailure preserves one typed diagnostic through the existing allowlisted
// boundary. Hosts must visit joined causes separately to retain their order.
// It never retains an arbitrary message, output, source or underlying cause.
func SafeFailure(err error, fallback, stage string) *wireless.Failure {
	if f, ok := err.(*wireless.Failure); ok {
		return wireless.NewFailure(f.Code(), f.Stage())
	}
	if f, ok := err.(*connector.Error); ok {
		if original := wireless.NewFailure(f.Code, f.Phase).Stage(); original != "" {
			stage = original
		}
		return wireless.NewFailure(f.Code, stage)
	}
	return failure(err, fallback, stage)
}

// textPattern compiles adapter-owned static prompt expressions.
func textPattern(expression string) expect.Pattern {
	p, err := expect.Compile(expression)
	if err != nil {
		panic(err)
	}
	return p
}

// responseBody checks canonical text before removing only the acknowledged
// prompt and first matching command echo; command-like body lines survive.
func responseBody(step connector.StepResult) (string, error) {
	response := step.Response()
	text, err := response.Rendered()
	if err != nil {
		return "", err
	}
	prompt, err := response.Prompt()
	if err != nil {
		return "", err
	}
	text = strings.TrimSuffix(text, prompt)
	first, rest, newline := strings.Cut(text, "\n")
	if strings.TrimSpace(first) == strings.TrimSpace(step.Command().Text) {
		if !newline {
			return "", nil
		}
		return rest, nil
	}
	return text, nil
}
