package netconnector

import (
	"context"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
	"github.com/gatework/go-wireless/internal/nativeerr"
	"github.com/gatework/go-wireless/parser"
)

var devicePrompt = regexp.MustCompile(`\A<[^<>\r\n]+>\z`)
var authFailure = regexp.MustCompile(`(?i)Login\s+incorrect|Authentication\s+failed`)
var connectionFailure = regexp.MustCompile(`(?i)Connection\s+(?:refused|timed\s+out|closed|reset|was\s+closed)|No\s+route\s+to\s+host|Unable\s+to\s+connect`)

// APSession reads AP queries by telnetting from the controller to each AP
// inside a borrowed controller operation.
type APSession struct {
	device      *connector.Device
	profile     parser.Profile
	credentials collect.APCredentials
	clock       collect.Clock
	usable      atomic.Bool
}

// NewAPSession validates the AP credentials and returns an AP session over
// the controller device.
func NewAPSession(controller *connector.Device, profile *parser.Profile, credentials collect.APCredentials, clock collect.Clock) (*APSession, error) {
	valid := func(s string) bool {
		return strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
	}
	if controller == nil || !valid(credentials.Password) || credentials.Username != "" && !valid(credentials.Username) {
		return nil, wireless.NewFailure("invalid_ap_credentials", "ap_setup")
	}
	if !profile.Valid() || !profile.MatchesConnector(controller.ProfileName()) || !profile.APQuery().Valid() || controller.ProfileName() != "h3c_wireless" {
		return nil, wireless.NewFailure("invalid_ap_profile", "ap_setup")
	}
	if clock == nil {
		clock = time.Now
	}
	a := &APSession{device: controller, profile: *profile, credentials: credentials, clock: clock}
	a.usable.Store(true)
	return a, nil
}
func (*APSession) String() string { return "wireless.adapter.APSession" }

// GoString formats metadata only, never credentials.
func (a *APSession) GoString() string { return a.String() }

// APFactory binds the exact borrowed controller supplied by Collect.
func APFactory(clock collect.Clock) collect.APFactory {
	return func(_ context.Context, s collect.Session, c collect.APCredentials) (collect.APReader, error) {
		native, ok := s.(*Session)
		if !ok || native == nil {
			return nil, wireless.NewFailure("invalid_ap_session", "ap_setup")
		}
		return NewAPSession(native.device, &native.profile, c, clock)
	}
}

type operationSession struct {
	op           *connector.Operation
	prompt       string
	query        parser.APQuery
	synchronized *bool
}

func (s operationSession) State(context.Context) (bool, error) { return s.op.Connected(), nil }
func (s operationSession) Execute(ctx context.Context, command string) (collect.Receipt, error) {
	if command != s.query.PreparationCommand() && command != s.query.Command() {
		return collect.Receipt{}, wireless.NewFailure("invalid_command_result", "execute")
	}
	result, e := s.op.ExecuteCommand(ctx, connector.Command{Text: command, Prompt: exactPrompt(s.prompt), OutputSensitive: true}, connector.ExecuteOptions{SkipPrivilege: true, PreserveSessionOnRejection: true})
	// A live operation after an error is the connector's verified synchronized
	// rejection case. The adapter only binds that state to this AP identity.
	*s.synchronized = ctx.Err() == nil && s.op.Connected() && strings.TrimSpace(s.op.RawPrompt()) == s.prompt
	return receipt(command, result, e), nil
}
func (a *APSession) Read(ctx context.Context, request collect.APRequest) (out collect.APOutcome, err error) {
	if !a.profile.MatchesAPQuery(request.Query) {
		return skippedAP("invalid_ap_query"), nil
	}
	if !a.usable.Load() {
		return skippedAP("controller_session_lost"), nil
	}
	address, code := apTarget(wireless.CloneRecord(request.AccessPoint))
	if code != "" {
		return skippedAP(code), nil
	}
	acquired := false
	e, retirementErr := a.device.WithBorrowedOperation(ctx, func(ctx context.Context, op *connector.Operation) error {
		acquired = true
		v := &apVisit{session: a, op: op, out: &out}
		v.run(ctx, address, request.Query)
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
	return out, err
}

// skippedAP is the outcome of an AP that was not visited.
func skippedAP(code string) collect.APOutcome {
	return collect.APOutcome{Error: wireless.NewFailure(code, "execute"), Skipped: true}
}

// apTarget checks that ap can be visited: a valid name, online, and a usable
// IPv4 management address. It returns the failure code otherwise.
func apTarget(ap wireless.Record) (netip.Addr, string) {
	name, _ := ap["name"].(string)
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return netip.Addr{}, "ap_address_unavailable"
	}
	status, _ := ap["operational_status"].(string)
	if parser.OnlineStatus(status) != "online" {
		return netip.Addr{}, "ap_not_online"
	}
	ip, _ := ap["management_ip"].(string)
	address, e := netip.ParseAddr(ip)
	if e != nil || !address.Is4() || address.IsUnspecified() {
		return netip.Addr{}, "ap_address_unavailable"
	}
	return address, ""
}

// apVisit is one controller-to-AP hop inside a borrowed controller operation.
// The controller session stays usable only when the visit provably returns
// to the original controller prompt.
type apVisit struct {
	session      *APSession
	op           *connector.Operation
	out          *collect.APOutcome
	original     string // the controller prompt before the hop
	apPrompt     string // the AP prompt once logged in
	returned     bool
	synchronized bool // the prompt state is known, so leaving may send quit
}

// run checks the controller, logs in to the AP, runs the query and always
// leaves the AP.
func (v *apVisit) run(ctx context.Context, address netip.Addr, query parser.APQuery) {
	a, op, out := v.session, v.op, v.out
	if !a.usable.Load() || !op.Connected() {
		a.usable.Store(false)
		out.Error = wireless.NewFailure("controller_session_lost", "session")
		out.Skipped = true
		return
	}
	v.original = strings.TrimSpace(op.RawPrompt())
	if !devicePrompt.MatchString(v.original) {
		out.Error = wireless.NewFailure("controller_prompt_mismatch", "session")
		return
	}
	a.usable.Store(false)
	defer v.leave(ctx)
	if failure := v.login(ctx, address); failure != nil {
		out.Error = failure
		return
	}
	v.query(ctx, query)
}

// leave returns to the controller prompt if needed and marks the controller
// session usable only when that is confirmed; otherwise it retires the
// operation. Cancellation never triggers another command.
func (v *apVisit) leave(ctx context.Context) {
	a, op := v.session, v.op
	if ctx.Err() == nil && op.Connected() && v.synchronized {
		current := strings.TrimSpace(op.RawPrompt())
		if v.apPrompt != "" && current == v.original {
			v.returned = true
		} else if v.apPrompt != "" && current == v.apPrompt {
			result, exitErr := op.ExecuteCommand(ctx, connector.Command{Text: "quit", Prompt: exactPrompt(v.original), Sensitive: true, OutputSensitive: true}, connector.ExecuteOptions{SkipPrivilege: true})
			_, validationErr := result.SingleAccepted("quit")
			v.returned = exitErr == nil && validationErr == nil && strings.TrimSpace(op.RawPrompt()) == v.original
		}
		a.usable.Store(v.returned && op.Connected() && strings.TrimSpace(op.RawPrompt()) == v.original)
	}
	if !a.usable.Load() {
		v.out.CleanupError = wireless.NewFailure("ap_exit_failed", "cleanup")
		if op.Connected() {
			_ = op.Retire() // WithBorrowedOperation retains the cleanup result.
		}
	}
}

// login telnets from the controller to the AP and confirms the AP prompt.
func (v *apVisit) login(ctx context.Context, address netip.Addr) *wireless.FailureError {
	a, op := v.session, v.op
	interactions := []connector.Interaction{{Pattern: textPattern(`(?i)password:\s*\z`), Reply: a.credentials.Password + "\n", Sensitive: true, Limit: 1}}
	if a.credentials.Username != "" {
		interactions = append([]connector.Interaction{{Pattern: textPattern(`(?i)(?:login|username):\s*\z`), Reply: a.credentials.Username + "\n", Sensitive: true, Limit: 1}}, interactions...)
	}
	timeout := 10 * time.Second
	loginCommand := "telnet " + address.String()
	login, loginErr := op.ExecuteCommand(ctx, connector.Command{Text: loginCommand, Prompt: textPattern(`(?:\A|[\r\n])[ \t\x00]*<[^<>\r\n]+>[ \t\x00]*[\r\n]*\z`), Timeout: &timeout, Interactions: interactions, Sensitive: true, OutputSensitive: true}, connector.ExecuteOptions{SkipPrivilege: true})
	if loginErr != nil {
		return nativeerr.Failure(loginErr, "ap_connection_failed", "login")
	}
	if _, validationErr := login.SingleAccepted(loginCommand); validationErr != nil {
		return wireless.NewFailure("ap_response_missing", "login")
	}
	v.synchronized = true
	current := strings.TrimSpace(op.RawPrompt())
	v.returned = current == v.original
	if !v.returned && devicePrompt.MatchString(current) {
		v.apPrompt = current
	}
	output, outputErr := login.JoinedOutput()
	switch {
	case outputErr != nil:
		return wireless.NewFailure("ap_response_missing", "login")
	case authFailure.MatchString(output):
		return wireless.NewFailure("ap_authentication_failed", "login")
	case connectionFailure.MatchString(output):
		return wireless.NewFailure("ap_connection_failed", "login")
	case v.apPrompt == "":
		return wireless.NewFailure("ap_login_not_confirmed", "login")
	}
	return nil
}

// query runs the AP preparation and the LLDP query, requiring the AP prompt
// to remain.
func (v *apVisit) query(ctx context.Context, query parser.APQuery) {
	op, out := v.op, v.out
	reader, _ := collect.NewReader(operationSession{op: op, prompt: v.apPrompt, query: query, synchronized: &v.synchronized}, v.session.clock)
	prep, prepErr := reader.Read(ctx, query.PreparationCommand())
	if prepErr != nil {
		out.Error = nativeerr.Failure(prepErr, "command_execution_failed", "prepare")
		return
	}
	if !prep.Success() {
		out.Error = prep.Failure()
		return
	}
	if strings.TrimSpace(op.RawPrompt()) != v.apPrompt {
		out.Error = wireless.NewFailure("ap_session_lost", "execute")
		return
	}
	captured, capErr := reader.Read(ctx, query.Command())
	if capErr != nil {
		out.Error = nativeerr.Failure(capErr, "command_execution_failed", "execute")
		return
	}
	out.Result = &captured
	if !captured.Success() {
		out.Error = captured.Failure()
	} else if strings.TrimSpace(op.RawPrompt()) != v.apPrompt {
		out.Error = wireless.NewFailure("ap_session_lost", "execute")
	}
}
