package netconnector

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	expect "github.com/gatework/go-expect"
	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
	"github.com/gatework/go-wireless/internal/nativeerr"
	"github.com/gatework/go-wireless/parser"
)

// Session runs the profile's declared collection commands on one controller
// device.
type Session struct {
	device       *connector.Device
	profile      parser.Profile
	commands     map[string]bool
	closeFailure atomic.Pointer[wireless.FailureError]
}

// Borrow binds a validated query profile without connecting or owning device close.
func Borrow(device *connector.Device, profile *parser.Profile) (*Session, error) {
	if device == nil {
		return nil, errors.New("wireless: device required")
	}
	if !profile.Valid() || !profile.MatchesConnector(device.ProfileName()) {
		return nil, errors.New("wireless: connector profile mismatch")
	}
	scope, err := profile.CollectionScope(profile.Dimensions())
	if err != nil {
		return nil, err
	}
	commands := map[string]bool{profile.PreparationCommand(): true}
	for _, query := range scope.Queries() {
		commands[query.Command] = true
	}
	return &Session{device: device, profile: *profile, commands: commands}, nil
}
func (*Session) String() string { return "wireless.adapter.Session" }

// GoString formats metadata only.
func (s *Session) GoString() string { return s.String() }

// State reports whether the controller session is still connected.
func (s *Session) State(context.Context) (bool, error) { return s.device.Connected(), nil }

// Open owns only the explicitly supplied device's connect/callback/close lifecycle.
func Open(device *connector.Device, profile *parser.Profile) collect.OpenSession {
	selected, validationErr := Borrow(device, profile)
	return func(ctx context.Context, run func(collect.Session) error) (err error) {
		if validationErr != nil || run == nil {
			return wireless.NewFailure("connection_failed", "connect")
		}
		session := &Session{device: selected.device, profile: selected.profile, commands: selected.commands}
		connectErr := device.Connect(ctx)
		if errors.Is(connectErr, connector.ErrBusy) {
			// Admission failed: the active lease still belongs to its caller.
			return nativeerr.Failure(connectErr, "connection_failed", "connect")
		}
		defer func() {
			closeErr := device.Close()
			if err == nil {
				err = ctx.Err()
			}
			if closeErr != nil || session.CloseFailure() != nil {
				err = errors.Join(err, wireless.NewFailure("session_close_failed", "close"))
			}
		}()
		if connectErr != nil {
			return nativeerr.Failure(connectErr, "connection_failed", "connect")
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
func receipt(command string, result connector.Result, err error) collect.Receipt {
	steps := result.Steps()
	body := ""
	f := nativeerr.Failure(err, "command_execution_failed", "execute")
	if err == nil {
		step, validationErr := result.SingleAccepted(command)
		if validationErr != nil {
			f = wireless.NewFailure("invalid_command_result", "response")
		} else {
			var e error
			body, e = captureBody(step)
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
	return collect.NewReceipt(collect.ReceiptOptions{
		Command: command, Raw: raw.String(), Body: body, Steps: len(steps),
		Attempted: len(attempts) > 0, Success: f == nil, Failure: f,
	})
}

// Execute runs one declared command and returns its receipt.
func (s *Session) Execute(ctx context.Context, command string) (collect.Receipt, error) {
	if !s.commands[command] {
		return collect.Receipt{}, wireless.NewFailure("invalid_command_result", "execute")
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
func (s *Session) CloseFailure() *wireless.FailureError {
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

// captureBody preserves Wireless's historical framing: one separator from the
// matched prompt belongs to the body. Connector Body retains all separators.
func captureBody(step connector.StepResult) (string, error) {
	body, err := step.Body()
	if err != nil {
		return "", err
	}
	prompt, err := step.Response().Prompt()
	if err != nil {
		return "", err
	}
	separators := len(prompt) - len(strings.TrimLeft(prompt, "\n"))
	return strings.TrimSuffix(body, strings.Repeat("\n", max(separators-1, 0))), nil
}
