// Package adapter borrows the versioned native connector; it never implements a second transport.
package adapter

import (
	"context"
	"errors"
	expect "github.com/gatework/go-expect"
	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/nativeerr"
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
		if e := device.Connect(ctx); e != nil {
			return nativeerr.Failure(e, "connection_failed", "connect")
		}
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
func receipt(command string, result connector.Result, err error) wireless.Receipt {
	steps := result.Steps()
	body := ""
	f := nativeerr.Failure(err, "command_execution_failed", "execute")
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
	attempts := result.Attempts()
	var raw strings.Builder
	for _, attempt := range attempts {
		raw.WriteString(attempt.Response().Raw())
	}
	return wireless.NewReceipt(wireless.ReceiptOptions{
		Command: command, Raw: raw.String(), Body: body, Steps: len(steps),
		Attempted: len(attempts) > 0, Success: f == nil, Failure: f,
	})
}
func (s *Session) Execute(ctx context.Context, command string) (wireless.Receipt, error) {
	if !queryAllowed(s.device.ProfileName(), command) {
		return wireless.Receipt{}, wireless.NewFailure("invalid_command_result", "execute")
	}
	var result connector.Result
	var commandErr error
	err, closeErr := s.device.WithBorrowedOperation(ctx, func(ctx context.Context, op *connector.Operation) error {
		prompt := strings.TrimSpace(op.RawPrompt())
		if prompt == "" {
			return wireless.NewFailure("controller_prompt_mismatch", "session")
		}
		result, commandErr = op.ExecuteCommand(ctx,
			connector.Command{Text: command, Prompt: exactPrompt(prompt), OutputSensitive: true},
			connector.ExecuteOptions{SkipPrivilege: true, PreserveSessionOnRejection: true})
		// The connector owns rejection preservation and retirement. The failed
		// command remains evidence even when this query does not end the scope.
		return nil
	})
	if closeErr != nil {
		s.closeFailure.CompareAndSwap(nil, wireless.NewFailure("session_close_failed", "close"))
	}
	// The retained native failure must not be turned into a successful receipt.
	if commandErr != nil {
		err = errors.Join(commandErr, err)
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
