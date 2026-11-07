package adapter

import (
	"context"
	"errors"
	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/nativeerr"
	"net/netip"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

var devicePrompt = regexp.MustCompile(`\A<[^<>\r\n]+>\z`)
var authFailure = regexp.MustCompile(`(?i)Login\s+incorrect|Authentication\s+failed`)
var connectionFailure = regexp.MustCompile(`(?i)Connection\s+(?:refused|timed\s+out|closed|reset|was\s+closed)|No\s+route\s+to\s+host|Unable\s+to\s+connect`)

type APSession struct {
	device      *connector.Device
	credentials wireless.APCredentials
	clock       wireless.Clock
	usable      atomic.Bool
}

func NewAPSession(controller *connector.Device, credentials wireless.APCredentials, clock wireless.Clock) (*APSession, error) {
	valid := func(s string) bool {
		return strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
	}
	if controller == nil || !valid(credentials.Password) || credentials.Username != "" && !valid(credentials.Username) {
		return nil, wireless.NewFailure("invalid_ap_credentials", "ap_setup")
	}
	if clock == nil {
		clock = time.Now
	}
	a := &APSession{device: controller, credentials: credentials, clock: clock}
	a.usable.Store(true)
	return a, nil
}
func (*APSession) String() string     { return "wireless.adapter.APSession" }
func (a *APSession) GoString() string { return a.String() }

// APFactory binds the exact borrowed controller supplied by Collect.
func APFactory(clock wireless.Clock) wireless.APFactory {
	return func(_ context.Context, s wireless.Session, c wireless.APCredentials) (wireless.APReader, error) {
		native, ok := s.(*Session)
		if !ok {
			return nil, wireless.NewFailure("invalid_ap_session", "ap_setup")
		}
		return NewAPSession(native.device, c, clock)
	}
}

type operationSession struct {
	op           *connector.Operation
	prompt       string
	synchronized *bool
}

func (s operationSession) State(context.Context) (bool, error) { return s.op.Connected(), nil }
func (s operationSession) Execute(ctx context.Context, command string) (wireless.Receipt, error) {
	if command != "screen-length disable" && command != wireless.LLDPCommand {
		return wireless.Receipt{}, wireless.NewFailure("invalid_command_result", "execute")
	}
	result, e := s.op.ExecuteCommand(ctx, connector.Command{Text: command, Prompt: exactPrompt(s.prompt), OutputSensitive: true}, connector.ExecuteOptions{SkipPrivilege: true, PreserveSessionOnRejection: true})
	// A live operation after an error is the connector's verified synchronized
	// rejection case. The adapter only binds that state to this AP identity.
	*s.synchronized = ctx.Err() == nil && s.op.Connected() && strings.TrimSpace(s.op.RawPrompt()) == s.prompt
	return receipt(command, result, e), nil
}
func (a *APSession) Read(ctx context.Context, ap wireless.Record) (out wireless.APOutcome, err error) {
	fail := func(code string) (wireless.APOutcome, error) {
		return wireless.APOutcome{Error: wireless.NewFailure(code, "execute"), Skipped: true}, nil
	}
	if !a.usable.Load() {
		return fail("controller_session_lost")
	}
	name, _ := ap["name"].(string)
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return fail("ap_address_unavailable")
	}
	status, _ := ap["operational_status"].(string)
	if wireless.OnlineStatus(status) != "online" {
		return fail("ap_not_online")
	}
	ip, _ := ap["management_ip"].(string)
	address, e := netip.ParseAddr(ip)
	if e != nil || !address.Is4() || address.IsUnspecified() {
		return fail("ap_address_unavailable")
	}
	acquired := false
	var retirementErr error
	e, retirementErr = a.device.WithBorrowedOperation(ctx, func(ctx context.Context, op *connector.Operation) error {
		acquired = true
		if !a.usable.Load() || !op.Connected() {
			a.usable.Store(false)
			out.Error = wireless.NewFailure("controller_session_lost", "session")
			out.Skipped = true
			return nil
		}
		original := strings.TrimSpace(op.RawPrompt())
		if !devicePrompt.MatchString(original) {
			out.Error = wireless.NewFailure("controller_prompt_mismatch", "session")
			return nil
		}
		a.usable.Store(false)
		apPrompt := ""
		returned := false
		synchronized := false
		defer func() {
			// Cancellation never triggers another command; only lease-owned retirement.
			if ctx.Err() == nil && op.Connected() && synchronized {
				current := strings.TrimSpace(op.RawPrompt())
				if apPrompt != "" && current == original {
					returned = true
				} else if apPrompt != "" && current == apPrompt {
					result, exitErr := op.ExecuteCommand(ctx, connector.Command{Text: "quit", Prompt: exactPrompt(original), Sensitive: true, OutputSensitive: true}, connector.ExecuteOptions{SkipPrivilege: true})
					returned = exitErr == nil && len(result.Steps()) == 1 && strings.TrimSpace(op.RawPrompt()) == original
				}
				a.usable.Store(returned && op.Connected() && strings.TrimSpace(op.RawPrompt()) == original)
			}
			if !a.usable.Load() {
				out.CleanupError = wireless.NewFailure("ap_exit_failed", "cleanup")
				if op.Connected() {
					_ = op.Retire() // WithBorrowedOperation retains the cleanup result.
				}
			}
		}()
		interactions := []connector.Interaction{{Pattern: textPattern(`(?i)password:\s*\z`), Reply: a.credentials.Password + "\n", Sensitive: true, Limit: 1}}
		if a.credentials.Username != "" {
			interactions = append([]connector.Interaction{{Pattern: textPattern(`(?i)(?:login|username):\s*\z`), Reply: a.credentials.Username + "\n", Sensitive: true, Limit: 1}}, interactions...)
		}
		timeout := 10 * time.Second
		login, loginErr := op.ExecuteCommand(ctx, connector.Command{Text: "telnet " + address.String(), Prompt: textPattern(`(?:\A|[\r\n])[ \t\x00]*<[^<>\r\n]+>[ \t\x00]*[\r\n]*\z`), Timeout: &timeout, Interactions: interactions, Sensitive: true, OutputSensitive: true}, connector.ExecuteOptions{SkipPrivilege: true})
		if loginErr != nil {
			out.Error = nativeerr.Failure(loginErr, "ap_connection_failed", "login")
			return nil
		}
		if len(login.Steps()) != 1 {
			out.Error = wireless.NewFailure("ap_response_missing", "login")
			return nil
		}
		synchronized = true
		current := strings.TrimSpace(op.RawPrompt())
		returned = current == original
		if !returned && devicePrompt.MatchString(current) {
			apPrompt = current
		}
		output, outputErr := login.JoinedOutput()
		if outputErr != nil {
			out.Error = wireless.NewFailure("ap_response_missing", "login")
			return nil
		}
		if authFailure.MatchString(output) {
			out.Error = wireless.NewFailure("ap_authentication_failed", "login")
			return nil
		}
		if connectionFailure.MatchString(output) {
			out.Error = wireless.NewFailure("ap_connection_failed", "login")
			return nil
		}
		if apPrompt == "" {
			out.Error = wireless.NewFailure("ap_login_not_confirmed", "login")
			return nil
		}
		reader, _ := wireless.NewReader(operationSession{op, apPrompt, &synchronized}, a.clock)
		prep, prepErr := reader.Read(ctx, "screen-length disable")
		if prepErr != nil {
			out.Error = nativeerr.Failure(prepErr, "command_execution_failed", "prepare")
			return nil
		}
		if !prep.Success() {
			out.Error = prep.Failure()
			return nil
		}
		if strings.TrimSpace(op.RawPrompt()) != apPrompt {
			out.Error = wireless.NewFailure("ap_session_lost", "execute")
			return nil
		}
		cap, capErr := reader.Read(ctx, wireless.LLDPCommand)
		if capErr != nil {
			out.Error = nativeerr.Failure(capErr, "command_execution_failed", "execute")
			return nil
		}
		out.Result = &cap
		if !cap.Success() {
			out.Error = cap.Failure()
		} else if strings.TrimSpace(op.RawPrompt()) != apPrompt {
			out.Error = wireless.NewFailure("ap_session_lost", "execute")
		}
		return nil
	})
	if retirementErr != nil {
		out.CloseError = wireless.NewFailure("session_close_failed", "close")
	}
	if e != nil {
		if out.Error == nil {
			out.Error = nativeerr.Failure(e, "ap_collection_failed", "execute")
		}
		if !acquired {
			out.Skipped = true
		}
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			err = e
		}
	}
	return
}
