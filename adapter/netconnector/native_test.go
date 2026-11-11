package netconnector

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gatework/go-wireless/parser"
	"github.com/gatework/go-wireless/vendors"

	expect "github.com/gatework/go-expect"
	"github.com/gatework/go-net-connector/connector"
	ncvendors "github.com/gatework/go-net-connector/vendors"
	"github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
)

type exchange struct{ send, reply string }
type closeTransport struct {
	net.Conn
	fail   bool
	closes atomic.Int32
}

func (c *closeTransport) Close() error {
	c.closes.Add(1)
	_ = c.Conn.Close()
	if c.fail {
		return errors.New("private close error")
	}
	return nil
}
func fixture(t *testing.T, steps []exchange, closeFail bool, event func(connector.SessionEvent), loginPrompt ...string) (*connector.Device, *closeTransport) {
	t.Helper()
	return fixtureObserver(t, steps, closeFail, func(_ context.Context, e connector.SessionEvent) error {
		if event != nil {
			event(e)
		}
		return nil
	}, loginPrompt...)
}
func fixtureObserver(t *testing.T, steps []exchange, closeFail bool, event func(context.Context, connector.SessionEvent) error, loginPrompt ...string) (*connector.Device, *closeTransport) {
	t.Helper()
	client, server := net.Pipe()
	transport := &closeTransport{Conn: client, fail: closeFail}
	done := make(chan error, 1)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(time.Second):
			t.Error("fixture did not exit")
		}
	})
	go func() {
		defer server.Close()
		prompt := "<WLC>"
		if len(loginPrompt) > 0 {
			prompt = loginPrompt[0]
		}
		if _, e := io.WriteString(server, prompt); e != nil {
			done <- e
			return
		}
		for _, step := range steps {
			buf := make([]byte, len(step.send))
			_, e := io.ReadFull(server, buf)
			if e != nil {
				done <- fmt.Errorf("expected next write %q: %w", step.send, e)
				return
			}
			if string(buf) != step.send {
				done <- fmt.Errorf("unexpected write %q, expected %q", buf, step.send)
				return
			}
			if _, e = io.WriteString(server, step.reply); e != nil {
				done <- e
				return
			}
		}
		b := make([]byte, 512)
		n, e := server.Read(b)
		if n > 0 {
			done <- fmt.Errorf("unexpected extra write %q", b[:n])
			return
		}
		if !errors.Is(e, io.EOF) && !errors.Is(e, io.ErrClosedPipe) {
			done <- e
			return
		}
		done <- nil
	}()
	platform, e := ncvendors.NewBuiltinRegistry().Lookup("h3c_wireless")
	if e != nil {
		t.Fatal(e)
	}
	d, e := connector.New(platform.Profile(), connector.ConnectionConfig{Host: "fixture.invalid", CommandTimeout: 150 * time.Millisecond, WriteTimeout: time.Second, OnSessionEvent: event}, connector.WithTransportFactory(func(context.Context, connector.ConnectionConfig) (io.ReadWriteCloser, error) { return transport, nil }))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = d.Close() })
	if e = d.Connect(t.Context()); e != nil {
		t.Fatal(e)
	}
	return d, transport
}

func TestNativeControllerNULPaddedLoginPromptDoesNotPoisonCommands(t *testing.T) {
	d, _ := fixture(t, []exchange{
		{"screen-length disable\n", "screen-length disable\r\n<WLC>"},
		{"display wlan client verbose\n", "Total number of clients : 0\r\n\x00<WLC>"},
	}, false, nil, "\r\n\x00<WLC>")
	s, _ := Borrow(d, selectedProfile())
	r, _ := collect.NewReader(s, nil)
	for _, command := range []string{"screen-length disable", "display wlan client verbose"} {
		receipt, err := r.Read(t.Context(), command)
		if err != nil || !receipt.Success() || !d.Connected() {
			t.Fatalf("NUL-padded prompt rejected %q: %v, receipt=%+v", command, err, receipt)
		}
	}
	client, server := net.Pipe()
	session, err := expect.NewSession(client)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	go func() { defer server.Close(); _, _ = io.WriteString(server, "\r\n<OTHER>") }()
	if _, err := session.Expect(t.Context(), []expect.Case{{Pattern: exactPrompt("\x00<WLC>")}}); err == nil {
		t.Fatal("padding normalization broadened the controller identity")
	}
}
func apRecord() wireless.Record {
	return wireless.Record{"name": "AP-1", "operational_status": "online", "management_ip": "192.0.2.7"}
}
func makeAP(t *testing.T, d *connector.Device) *APSession {
	t.Helper()
	a, e := NewAPSession(d, selectedProfile(), collect.APCredentials{Username: "ap-user", Password: "ap-secret"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func loginSteps() []exchange {
	return []exchange{{"telnet 192.0.2.7\n", "Username:"}, {"ap-user\n", "Password:"}, {"ap-secret\n", "\r\n<AP>"}}
}
func TestNativeAPDialoguePaginationAndExactReturn(t *testing.T) {
	steps := append(loginSteps(), exchange{"screen-length disable\n", "screen-length disable\r\n<AP>"}, exchange{selectedProfile().APQuery().Command() + "\n", selectedProfile().APQuery().Command() + "\r\nNo LLDP neighbors\r\n---- More ----"}, exchange{" ", "\r\n<AP>"}, exchange{"quit\n", "quit\r\n<WLC>"})
	d, _ := fixture(t, steps, false, nil)
	out, e := readNativeAP(t.Context(), makeAP(t, d), apRecord())
	if e != nil || out.Error != nil || out.CleanupError != nil || out.CloseError != nil || out.Result == nil || !out.Result.Success() {
		t.Fatalf("outcome %v %v %+v", out.Error, e, out.Result)
	}
	if !strings.Contains(out.Result.Body(), "No LLDP neighbors") || strings.Contains(out.Result.Body(), "More") || d.RawPrompt() != "<WLC>" {
		t.Fatalf("body/prompt %q %q", out.Result.Body(), d.RawPrompt())
	}
	if len(out.Result.Evidence()) != 1 {
		t.Fatal("missing source receipt")
	}
}
func TestNativeRejectedQueryContinuesAndDefaultStillCloses(t *testing.T) {
	rejection := "% Unrecognized command found at '^' position.\r\n<WLC>"
	t.Run("borrowed_reader", func(t *testing.T) {
		d, _ := fixture(t, []exchange{{"display wlan ap all verbose\n", rejection}, {"display wlan client verbose\n", "Total number of clients : 0\r\n<WLC>"}}, false, nil)
		s, _ := Borrow(d, selectedProfile())
		r, _ := collect.NewReader(s, nil)
		bad, e := r.Read(t.Context(), "display wlan ap all verbose")
		if e != nil || bad.Success() || bad.Failure().Code() != "device_error" || !d.Connected() {
			t.Fatalf("rejection %v %v connected=%t", bad, e, d.Connected())
		}
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(rejection)))
		if bad.Evidence()[0]["sha256"] != want {
			t.Fatal("rejection evidence lost")
		}
		next, e := r.Read(t.Context(), "display wlan client verbose")
		if e != nil || !next.Success() {
			t.Fatalf("independent query %v %v", next, e)
		}
	})
	t.Run("default", func(t *testing.T) {
		d, _ := fixture(t, []exchange{{"show\n", rejection}}, false, nil)
		result, e := d.ExecuteCommand(t.Context(), connector.Command{Text: "show"}, connector.ExecuteOptions{})
		if e == nil || d.Connected() || len(result.Steps()) != 0 {
			t.Fatal("changed default rejection behavior")
		}
	})
}
func TestNativeAPBadCredentialsAndReturnedController(t *testing.T) {
	for _, reply := range []string{"Login incorrect\r\n<WLC>", "Connection closed\r\n<WLC>", "<WLC>"} {
		t.Run(reply, func(t *testing.T) {
			steps := loginSteps()
			steps[2].reply = reply
			d, _ := fixture(t, steps, false, nil)
			out, e := readNativeAP(t.Context(), makeAP(t, d), apRecord())
			if e != nil || out.Error == nil || out.CleanupError != nil || !d.Connected() {
				t.Fatalf("%v %v %v", e, out.Error, out.CleanupError)
			}
		})
	}
}
func TestNativeAPUnknownPromptClosesWithoutQuitAndPreservesPartial(t *testing.T) {
	steps := append(loginSteps(), exchange{"screen-length disable\n", "<AP>"}, exchange{selectedProfile().APQuery().Command() + "\n", "partial secret raw\r\n[UNKNOWN]"})
	d, tr := fixture(t, steps, true, nil)
	a := makeAP(t, d)
	out, e := readNativeAP(t.Context(), a, apRecord())
	if e != nil || out.Error == nil || out.CleanupError == nil || out.CloseError == nil || out.Result == nil || out.Result.Success() || d.Connected() {
		t.Fatalf("failure diagnostics %v %v %v %v", e, out.Error, out.CleanupError, out.CloseError)
	}
	if out.Result.Evidence()[0]["bytes"] != len("partial secret raw\r\n[UNKNOWN]") {
		t.Fatalf("partial receipt: %v", out.Result.Evidence())
	}
	if tr.closes.Load() != 2 {
		t.Fatalf("unexpected native failure-close attempts %d", tr.closes.Load())
	}
	again, _ := readNativeAP(t.Context(), a, apRecord())
	if !again.Skipped || again.Error.Code() != "controller_session_lost" {
		t.Fatal("reused uncertain controller")
	}
}
func TestNativeAPCancellationRetiresInsideLease(t *testing.T) {
	d, tr := fixture(t, []exchange{{"telnet 192.0.2.7\n", "Password:"}, {"ap-secret\n", "waiting"}}, false, nil)
	a := makeAP(t, d)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	out, e := readNativeAP(ctx, a, apRecord())
	if !errors.Is(e, context.DeadlineExceeded) || out.CleanupError == nil || d.Connected() || tr.closes.Load() != 1 {
		t.Fatalf("cancellation %v %v closes=%d", e, out.CleanupError, tr.closes.Load())
	}
}
func TestNativeLeaseBusyReentryStaleAndCancellationRetire(t *testing.T) {
	var d *connector.Device
	var op *connector.Operation
	events := 0
	event := func(e connector.SessionEvent) {
		if e.Name == "command_start" && op != nil {
			events++
			if !errors.Is(op.Retire(), connector.ErrBusy) {
				t.Error("reentrant retirement allowed")
			}
		}
	}
	d, _ = fixture(t, []exchange{{"show\n", "<WLC>"}}, false, event)
	a := makeAP(t, d)
	var saved *connector.Operation
	operationCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e, closeErr := d.WithBorrowedOperation(operationCtx, func(ctx context.Context, o *connector.Operation) error {
		op = o
		saved = o
		out, err := readNativeAP(ctx, a, apRecord())
		if err != nil || !out.Skipped || out.Error.Code() != "session_busy" || !d.Connected() {
			t.Fatal("busy operation changed controller")
		}
		_, err = o.ExecuteCommand(ctx, connector.Command{Text: "show"}, connector.ExecuteOptions{})
		if err != nil {
			return err
		}
		cancel()
		if ctx.Err() == nil {
			t.Fatal("expected cancelled child context")
		}
		return o.Retire()
	})
	if !errors.Is(e, context.Canceled) || closeErr != nil || events != 1 || d.Connected() {
		t.Fatalf("retire %v %d", e, events)
	}
	if !errors.Is(saved.Retire(), connector.ErrScope) {
		t.Fatal("stale retire accepted")
	}
}
func TestNativeAPValidationNeverWrites(t *testing.T) {
	d, _ := fixture(t, nil, false, nil)
	a := makeAP(t, d)
	for _, ap := range []wireless.Record{nil, {"name": "bad", "operational_status": "offline"}, {"name": "bad", "operational_status": "online", "management_ip": "2001:db8::1"}, {"name": "bad", "operational_status": "online", "management_ip": "0.0.0.0"}} {
		out, e := readNativeAP(t.Context(), a, ap)
		if e != nil || !out.Skipped || out.Error == nil {
			t.Fatal(out, e)
		}
	}
}
func TestNativeBorrowDoesNotConnectOrRunArbitraryCommands(t *testing.T) {
	d, _ := fixture(t, nil, false, nil)
	s, _ := Borrow(d, selectedProfile())
	if _, e := s.Execute(t.Context(), "reboot"); e == nil {
		t.Fatal("unsafe command accepted")
	}
	_ = d.Close()
	r, _ := collect.NewReader(s, nil)
	captured, e := r.Read(t.Context(), "display wlan client verbose")
	if e != nil || !captured.Skipped() {
		t.Fatal(captured, e)
	}
}

func TestNativePreservationRejectsCallbackCancellationAndMultiCommand(t *testing.T) {
	rejection := "% Unrecognized command found at '^' position.\r\n<WLC>"
	for _, kind := range []string{"observer_error", "cancelled", "multi"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			steps := []exchange{{"query\n", rejection}}
			if kind == "multi" {
				steps = nil
			}
			d, _ := fixtureObserver(t, steps, false, func(_ context.Context, event connector.SessionEvent) error {
				if event.Name == "command_complete" {
					if kind == "observer_error" {
						return errors.New("private observer failure")
					}
					if kind == "cancelled" {
						cancel()
					}
				}
				return nil
			})
			var result connector.Result
			operationErr, closeErr := d.WithBorrowedOperation(ctx, func(ctx context.Context, op *connector.Operation) error {
				commands := []connector.Command{{Text: "query"}}
				if kind == "multi" {
					commands = append(commands, connector.Command{Text: "must not run"})
				}
				var err error
				result, err = op.ExecuteScript(ctx, connector.Script{Commands: commands}, connector.ExecuteOptions{SkipPrivilege: true, PreserveSessionOnRejection: true})
				return err
			})
			if operationErr == nil || closeErr != nil || d.Connected() || len(result.Steps()) != 0 {
				t.Fatalf("unsafe preservation: %v connected=%t steps=%d cleanup=%v", operationErr, d.Connected(), len(result.Steps()), closeErr)
			}
			if kind != "multi" && (len(result.Attempts()) != 1 || result.Attempts()[0].Response().Raw() != rejection) {
				t.Fatal("failed attempt lost")
			}
		})
	}
}

func TestNativeAPCapturedFactsSurviveWrongExitAndCloseFailure(t *testing.T) {
	steps := append(loginSteps(), exchange{"screen-length disable\n", "<AP>"}, exchange{selectedProfile().APQuery().Command() + "\n", "No LLDP neighbors\r\n<AP>"}, exchange{"quit\n", "[UNKNOWN]"})
	d, _ := fixture(t, steps, true, nil)
	out, e := readNativeAP(t.Context(), makeAP(t, d), apRecord())
	if e != nil || out.Result == nil || !out.Result.Success() || out.Error != nil || out.CleanupError == nil || out.CloseError == nil || d.Connected() {
		t.Fatalf("facts lost: %v %v %v %v", e, out.Error, out.CleanupError, out.CloseError)
	}
}
func TestNativeAPRejectedReadRestoresConfirmedController(t *testing.T) {
	steps := append(loginSteps(), exchange{"screen-length disable\n", "<AP>"}, exchange{selectedProfile().APQuery().Command() + "\n", "% Unrecognized command found at '^' position.\r\n<AP>"}, exchange{"quit\n", "<WLC>"})
	d, _ := fixture(t, steps, false, nil)
	out, e := readNativeAP(t.Context(), makeAP(t, d), apRecord())
	if e != nil || out.Result == nil || out.Result.Success() || out.Error.Code() != "device_error" || out.CleanupError != nil || !d.Connected() {
		t.Fatal(out.Error, e, out.CleanupError)
	}
}
func TestNativeOpenCloseErrorKeepsControllerFacts(t *testing.T) {
	d, _ := fixture(t, []exchange{{"screen-length disable\n", "<WLC>"}, {"display wlan client verbose\n", "Total number of clients : 0\r\n<WLC>"}}, true, nil)
	snapshot, e := collect.NewCollector(collect.CollectorOptions{}).Collect(t.Context(), collect.CollectOptions{Target: wireless.Record{"host": "fixture"}, Profile: selectedProfile(), ConnectorPlatform: "h3c_wireless", Dimensions: []wireless.Dimension{wireless.Clients}, Open: Open(d, selectedProfile())})
	if e != nil || !snapshot.Dimension(wireless.Clients).Complete() || snapshot.Dimension(wireless.Collection).Issues()[0]["code"] != "session_close_failed" {
		t.Fatal(snapshot, e)
	}
}
func TestNativeAPConstructorAndFactoryValidateCredentialsAndSession(t *testing.T) {
	for _, creds := range []collect.APCredentials{{}, {Password: "bad\n"}, {Password: "valid", Username: " "}} {
		if _, e := NewAPSession(nil, selectedProfile(), creds, nil); e == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
	if _, e := APFactory(nil)(t.Context(), nil, collect.APCredentials{}); e == nil {
		t.Fatal("missing native controller accepted")
	}
	if _, e := Borrow(nil, selectedProfile()); e == nil {
		t.Fatal("nil device accepted")
	}
}
func TestNativeBodyRemovesOnlyFirstEchoAndPreservesIndentation(t *testing.T) {
	command := "display wlan client verbose"
	d, _ := fixture(t, []exchange{{command + "\n", command + "\r\n  body\r\n" + command + "\r\n<WLC>"}}, false, nil)
	s, _ := Borrow(d, selectedProfile())
	r, _ := collect.NewReader(s, nil)
	captured, e := r.Read(t.Context(), command)
	if e != nil || captured.Body() != "  body\n"+command+"\n" {
		t.Fatalf("body=%q err=%v", captured.Body(), e)
	}
}

func TestNativeInvalidEncodingRetainsReceipt(t *testing.T) {
	command := "display wlan client verbose"
	output := "invalid\xff\r\n<WLC>"
	d, _ := fixture(t, []exchange{{command + "\n", output}}, false, nil)
	s, _ := Borrow(d, selectedProfile())
	r, _ := collect.NewReader(s, nil)
	captured, e := r.Read(t.Context(), command)
	if e != nil || captured.Success() || captured.Failure().Code() != "invalid_output_encoding" || captured.Evidence()[0]["bytes"] != len(output) || d.Connected() {
		t.Fatal(captured, e, captured.Evidence())
	}
}

func TestNativeDecodedBodyRetainsRawWireEvidence(t *testing.T) {
	command := "display wlan client verbose"
	wire := command + "\r\n  \xd6\xd0\xce\xc4\r\n<WLC>"
	d, _ := fixture(t, []exchange{{command + "\n", wire}}, false, nil)
	s, _ := Borrow(d, selectedProfile())
	r, _ := collect.NewReader(s, nil)
	capture, err := r.Read(t.Context(), command)
	evidence := capture.Evidence()
	if err != nil || !capture.Success() || capture.Body() != "  中文\n" || len(evidence) != 1 {
		t.Fatalf("decoded body %q, success=%t, err=%v", capture.Body(), capture.Success(), err)
	}
	if evidence[0]["bytes"] != len(wire) || evidence[0]["sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(wire))) {
		t.Fatal("wire bytes were replaced by decoded or accepted output", evidence)
	}
}

func TestNativeControllerReadFailurePreservesCloseDiagnostic(t *testing.T) {
	d, _ := fixture(t, []exchange{{"display wlan client verbose\n", "partial"}}, true, nil)
	s, _ := Borrow(d, selectedProfile())
	r, _ := collect.NewReader(s, nil)
	captured, e := r.Read(t.Context(), "display wlan client verbose")
	if e != nil || captured.Success() || s.CloseFailure() == nil || s.CloseFailure().Code() != "session_close_failed" || d.Connected() {
		t.Fatal(captured, e, s.CloseFailure())
	}
	diagnostic := s.CloseFailure()
	*diagnostic = wireless.FailureError{}
	if s.CloseFailure().Code() != "session_close_failed" {
		t.Fatal("retained close diagnostic was mutable")
	}
}

func TestNativeAdapterFormattersAndFactoryUseBorrowedController(t *testing.T) {
	d, _ := fixture(t, nil, false, nil)
	s, _ := Borrow(d, selectedProfile())
	reader, e := APFactory(nil)(t.Context(), s, collect.APCredentials{Password: "private credential"})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []any{s, reader} {
		if strings.Contains(fmt.Sprintf("%v %#v", v, v), "private") {
			t.Fatal("credential exposed")
		}
	}
}
func TestNativeRetiredOperationCannotReconnect(t *testing.T) {
	d, _ := fixture(t, nil, false, nil)
	e, closeErr := d.WithBorrowedOperation(t.Context(), func(ctx context.Context, op *connector.Operation) error {
		if e := op.Retire(); e != nil {
			return e
		}
		if !errors.Is(op.Retire(), connector.ErrScope) {
			t.Fatal("retired twice")
		}
		if _, e := op.ExecuteCommand(t.Context(), connector.Command{Text: "never"}, connector.ExecuteOptions{}); !errors.Is(e, connector.ErrScope) {
			t.Fatal("retired operation reconnected")
		}
		return nil
	})
	if e != nil || closeErr != nil {
		t.Fatal(e, closeErr)
	}
}

func TestReviewLateCancellationRetainsOwnedCloseFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, _ := fixture(t, []exchange{{"display wlan client verbose\n", "Total number of clients : 0\r\n<WLC>"}}, true, func(e connector.SessionEvent) {
		if e.Name == "operation_complete" {
			cancel()
		}
	})
	s, _ := Borrow(d, selectedProfile())
	r, _ := collect.NewReader(s, nil)
	captured, e := r.Read(ctx, "display wlan client verbose")
	if e != nil || captured.Success() || captured.Body() != "" || captured.Failure().Code() != "cancelled" || d.Connected() {
		t.Fatalf("cancellation result %v %v", captured, e)
	}
	if s.CloseFailure() == nil || s.CloseFailure().Code() != "session_close_failed" {
		t.Fatal("late cancellation lost owned close failure")
	}
}

func TestReviewOperationCallbackReturnRetainsFallbackCloseResult(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, _ := fixture(t, nil, true, nil)
	operationErr, closeErr := d.WithBorrowedOperation(ctx, func(ctx context.Context, op *connector.Operation) error {
		// Cancellation begins after the callback's final check, at callback return.
		if ctx.Err() != nil {
			t.Fatal("already cancelled")
		}
		defer cancel()
		return nil
	})
	if !errors.Is(operationErr, context.Canceled) || closeErr == nil || d.Connected() {
		t.Fatalf("fallback ownership %v, close retained=%t, connected=%t", operationErr, closeErr != nil, d.Connected())
	}
}
func TestReviewAPLateCancellationRetainsFallbackCloseResult(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	completions := 0
	steps := append(loginSteps(), exchange{"screen-length disable\n", "<AP>"}, exchange{selectedProfile().APQuery().Command() + "\n", "No LLDP neighbors\r\n<AP>"}, exchange{"quit\n", "<WLC>"})
	d, _ := fixture(t, steps, true, func(e connector.SessionEvent) {
		if e.Name == "operation_complete" {
			completions++
			if completions == 4 {
				cancel()
			}
		}
	})
	out, e := readNativeAP(ctx, makeAP(t, d), apRecord())
	if !errors.Is(e, context.Canceled) || out.Error == nil || out.CloseError == nil || out.Result == nil || !out.Result.Success() || d.Connected() {
		t.Fatalf("AP late outcome %v, primary=%v, close=%v", e, out.Error, out.CloseError)
	}
}

func TestReviewNativeOpenPreservesCancellationAndCloseIndependently(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, _ := fixture(t, []exchange{{"screen-length disable\n", "<WLC>"}, {"display wlan client verbose\n", "Total number of clients : 0\r\n<WLC>"}}, true, nil)
	nativeOpen := Open(d, selectedProfile())
	open := func(ctx context.Context, run func(collect.Session) error) error {
		return nativeOpen(ctx, func(s collect.Session) error { err := run(s); cancel(); return err })
	}
	snapshot, e := collect.NewCollector(collect.CollectorOptions{}).Collect(ctx, collect.CollectOptions{Target: wireless.Record{"host": "fixture"}, Profile: selectedProfile(), ConnectorPlatform: "h3c_wireless", Dimensions: []wireless.Dimension{wireless.Clients}, Open: open})
	if e != nil || !snapshot.Dimension(wireless.Clients).Complete() {
		t.Fatal(snapshot, e)
	}
	issues := snapshot.Dimension(wireless.Collection).Issues()
	if len(issues) != 2 || issues[0]["code"] != "cancelled" || issues[1]["code"] != "session_close_failed" {
		t.Fatalf("native opener diagnostics %v", issues)
	}
}

func TestNativeObserverCloseCancelsOperation(t *testing.T) {
	var device *connector.Device
	device, _ = fixture(t, nil, false, func(event connector.SessionEvent) {
		if event.Name == "command_start" {
			if err := device.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	err, _ := device.WithBorrowedOperation(t.Context(), func(ctx context.Context, op *connector.Operation) error {
		_, err := op.ExecuteCommand(ctx, connector.Command{Text: "never"}, connector.ExecuteOptions{})
		return err
	})
	if !errors.Is(err, context.Canceled) || device.Connected() {
		t.Fatalf("Close did not terminate admitted work: %v", err)
	}
}

func selectedProfile() *parser.Profile {
	profile, _ := vendors.NewBuiltinRegistry().Fetch("h3c", "")
	return profile
}

func readNativeAP(ctx context.Context, a *APSession, ap wireless.Record) (collect.APOutcome, error) {
	return a.Read(ctx, collect.APRequest{AccessPoint: ap, Query: a.profile.APQuery()})
}

func TestNativeCaptureRetainsOneMatchedPromptSeparator(t *testing.T) {
	command := "display wlan client verbose"
	wire := command + "\r\n  body\r\n" + command + "\r\n\r\n\r\n<WLC>"
	device, _ := fixture(t, []exchange{{command + "\n", wire}}, false, nil)
	result, err := device.ExecuteCommand(t.Context(), connector.Command{Text: command, Prompt: textPattern(`(?:\r?\n)+<WLC>$`)}, connector.ExecuteOptions{SkipPrivilege: true})
	if err != nil {
		t.Fatal(err)
	}
	step, err := result.SingleAccepted(command)
	if err != nil {
		t.Fatal(err)
	}
	body, err := step.Body()
	if err != nil || body != "  body\n"+command+"\n\n\n" {
		t.Fatalf("connector body = %q, err = %v", body, err)
	}
	got := receipt(command, result, nil)
	if got.Body() != "  body\n"+command+"\n" || got.Raw() != wire {
		t.Fatalf("wireless framing = %q, raw retained = %t", got.Body(), got.Raw() == wire)
	}
}
