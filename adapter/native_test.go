package adapter

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

	expect "github.com/gatework/go-expect"
	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-net-connector/vendors"
	"github.com/gatework/go-wireless"
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
func fixture(t *testing.T, steps []exchange, closeFail bool, event func(connector.Event), loginPrompt ...string) (*connector.Device, *closeTransport) {
	t.Helper()
	client, server := net.Pipe()
	transport := &closeTransport{Conn: client, fail: closeFail}
	done := make(chan error, 1)
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
		if e != io.EOF && e != io.ErrClosedPipe {
			done <- e
			return
		}
		done <- nil
	}()
	p, e := vendors.Profile("h3c_wireless")
	if e != nil {
		t.Fatal(e)
	}
	d, e := connector.New(p, connector.ConnectionConfig{Host: "fixture.invalid", CommandTimeout: 150 * time.Millisecond, WriteTimeout: time.Second, OnEvent: event}, connector.WithTransportFactory(func(context.Context, connector.ConnectionConfig) (io.ReadWriteCloser, error) { return transport, nil }))
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Connect(t.Context()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = d.Close()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(time.Second):
			t.Error("fixture did not join")
		}
	})
	return d, transport
}

func TestNativeControllerNULPaddedLoginPromptDoesNotPoisonCommands(t *testing.T) {
	d, _ := fixture(t, []exchange{
		{"screen-length disable\n", "screen-length disable\r\n<WLC>"},
		{"display wlan client verbose\n", "Total number of clients : 0\r\n\x00<WLC>"},
	}, false, nil, "\r\n\x00<WLC>")
	s, _ := Borrow(d)
	r, _ := wireless.NewReader(s, nil)
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
	a, e := NewAPSession(d, wireless.APCredentials{Username: "ap-user", Password: "ap-secret"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func loginSteps() []exchange {
	return []exchange{{"telnet 192.0.2.7\n", "Username:"}, {"ap-user\n", "Password:"}, {"ap-secret\n", "\r\n<AP>"}}
}
func TestNativeAPDialoguePaginationAndExactReturn(t *testing.T) {
	steps := append(loginSteps(), exchange{"screen-length disable\n", "screen-length disable\r\n<AP>"}, exchange{wireless.LLDPCommand + "\n", wireless.LLDPCommand + "\r\nNo LLDP neighbors\r\n---- More ----"}, exchange{" ", "\r\n<AP>"}, exchange{"quit\n", "quit\r\n<WLC>"})
	d, _ := fixture(t, steps, false, nil)
	out, e := makeAP(t, d).Read(t.Context(), apRecord())
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
		s, _ := Borrow(d)
		r, _ := wireless.NewReader(s, nil)
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
			out, e := makeAP(t, d).Read(t.Context(), apRecord())
			if e != nil || out.Error == nil || out.CleanupError != nil || !d.Connected() {
				t.Fatalf("%v %v %v", e, out.Error, out.CleanupError)
			}
		})
	}
}
func TestNativeAPUnknownPromptClosesWithoutQuitAndPreservesPartial(t *testing.T) {
	steps := append(loginSteps(), exchange{"screen-length disable\n", "<AP>"}, exchange{wireless.LLDPCommand + "\n", "partial secret raw\r\n[UNKNOWN]"})
	d, tr := fixture(t, steps, true, nil)
	a := makeAP(t, d)
	out, e := a.Read(t.Context(), apRecord())
	if e != nil || out.Error == nil || out.CleanupError == nil || out.CloseError == nil || out.Result == nil || out.Result.Success() || d.Connected() {
		t.Fatalf("failure diagnostics %v %v %v %v", e, out.Error, out.CleanupError, out.CloseError)
	}
	if out.Result.Evidence()[0]["bytes"] != len("partial secret raw\r\n[UNKNOWN]") {
		t.Fatalf("partial receipt: %v", out.Result.Evidence())
	}
	if tr.closes.Load() != 2 {
		t.Fatalf("unexpected native failure-close attempts %d", tr.closes.Load())
	}
	again, _ := a.Read(t.Context(), apRecord())
	if !again.Skipped || again.Error.Code() != "controller_session_lost" {
		t.Fatal("reused uncertain controller")
	}
}
func TestNativeAPCancellationRetiresInsideLease(t *testing.T) {
	d, tr := fixture(t, []exchange{{"telnet 192.0.2.7\n", "Password:"}, {"ap-secret\n", "waiting"}}, false, nil)
	a := makeAP(t, d)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	out, e := a.Read(ctx, apRecord())
	if !errors.Is(e, context.DeadlineExceeded) || out.CleanupError == nil || d.Connected() || tr.closes.Load() != 1 {
		t.Fatalf("cancellation %v %v closes=%d", e, out.CleanupError, tr.closes.Load())
	}
}
func TestNativeLeaseBusyReentryStaleAndCancellationRetire(t *testing.T) {
	var d *connector.Device
	var op *connector.Operation
	events := 0
	event := func(e connector.Event) {
		if e.Name == "command_start" && op != nil {
			events++
			if !errors.Is(op.Retire(), connector.ErrBusy) {
				t.Error("reentrant retirement allowed")
			}
			if !errors.Is(d.Close(), connector.ErrBusy) {
				t.Error("lease bypassed")
			}
		}
	}
	d, _ = fixture(t, []exchange{{"show\n", "<WLC>"}}, false, event)
	a := makeAP(t, d)
	var saved *connector.Operation
	e := d.WithOperation(t.Context(), func(ctx context.Context, o *connector.Operation) error {
		op = o
		saved = o
		out, err := a.Read(t.Context(), apRecord())
		if err != nil || !out.Skipped || out.Error.Code() != "session_busy" || !d.Connected() {
			t.Fatal("busy operation changed controller")
		}
		_, err = o.ExecuteBorrowedCommand(t.Context(), connector.Command{Text: "show"}, connector.ExecuteOptions{})
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = o.ExecuteBorrowedCommand(ctx, connector.Command{Text: "never"}, connector.ExecuteOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		return o.Retire()
	})
	if e != nil || events != 1 || d.Connected() {
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
		out, e := a.Read(t.Context(), ap)
		if e != nil || !out.Skipped || out.Error == nil {
			t.Fatal(out, e)
		}
	}
}
func TestNativeBorrowDoesNotConnectOrRunArbitraryCommands(t *testing.T) {
	d, _ := fixture(t, nil, false, nil)
	s, _ := Borrow(d)
	if _, e := s.Execute(t.Context(), "reboot"); e == nil {
		t.Fatal("unsafe command accepted")
	}
	_ = d.Close()
	r, _ := wireless.NewReader(s, nil)
	cap, e := r.Read(t.Context(), "display wlan client verbose")
	if e != nil || !cap.Skipped() {
		t.Fatal(cap, e)
	}
}

func TestNativePreservationRejectsCallbackCancellationAndMultiCommand(t *testing.T) {
	rejection := "% Unrecognized command found at '^' position.\r\n<WLC>"
	for _, kind := range []string{"receipt_error", "cancelled", "multi"} {
		t.Run(kind, func(t *testing.T) {
			d, _ := fixture(t, []exchange{{"query\n", rejection}}, false, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			opts := connector.ExecuteOptions{Borrowed: true, PreserveSessionOnRejection: true, CapturePartialOutput: true}
			commands := []connector.Command{{Text: "query"}}
			if kind == "receipt_error" {
				opts.OnReceipt = func(context.Context, connector.StepResult) error { return errors.New("private callback failure") }
			}
			if kind == "cancelled" {
				opts.OnReceipt = func(context.Context, connector.StepResult) error { cancel(); return nil }
			}
			if kind == "multi" {
				commands = append(commands, connector.Command{Text: "must not run"})
			}
			result, e := d.ExecuteScript(ctx, connector.Script{Commands: commands}, opts)
			if e == nil || d.Connected() || len(result.Steps()) != 1 {
				t.Fatalf("unsafe preservation: %v connected=%t steps=%d", e, d.Connected(), len(result.Steps()))
			}
		})
	}
}
func TestNativeAPCapturedFactsSurviveWrongExitAndCloseFailure(t *testing.T) {
	steps := append(loginSteps(), exchange{"screen-length disable\n", "<AP>"}, exchange{wireless.LLDPCommand + "\n", "No LLDP neighbors\r\n<AP>"}, exchange{"quit\n", "[UNKNOWN]"})
	d, _ := fixture(t, steps, true, nil)
	out, e := makeAP(t, d).Read(t.Context(), apRecord())
	if e != nil || out.Result == nil || !out.Result.Success() || out.Error != nil || out.CleanupError == nil || out.CloseError == nil || d.Connected() {
		t.Fatalf("facts lost: %v %v %v %v", e, out.Error, out.CleanupError, out.CloseError)
	}
}
func TestNativeAPRejectedReadRestoresConfirmedController(t *testing.T) {
	steps := append(loginSteps(), exchange{"screen-length disable\n", "<AP>"}, exchange{wireless.LLDPCommand + "\n", "% Unrecognized command found at '^' position.\r\n<AP>"}, exchange{"quit\n", "<WLC>"})
	d, _ := fixture(t, steps, false, nil)
	out, e := makeAP(t, d).Read(t.Context(), apRecord())
	if e != nil || out.Result == nil || out.Result.Success() || out.Error.Code() != "device_error" || out.CleanupError != nil || !d.Connected() {
		t.Fatal(out.Error, e, out.CleanupError)
	}
}
func TestNativeOpenCloseErrorKeepsControllerFacts(t *testing.T) {
	d, _ := fixture(t, []exchange{{"screen-length disable\n", "<WLC>"}, {"display wlan client verbose\n", "Total number of clients : 0\r\n<WLC>"}}, true, nil)
	snapshot, e := wireless.NewCollector(wireless.CollectorOptions{}).Collect(t.Context(), wireless.CollectOptions{Target: wireless.Record{"host": "fixture"}, Platform: "h3c", ConnectorPlatform: "h3c_wireless", Dimensions: []wireless.Dimension{wireless.Clients}, Open: Open(d)})
	if e != nil || !snapshot.Dimension(wireless.Clients).Complete() || snapshot.Dimension(wireless.Collection).Issues()[0]["code"] != "session_close_failed" {
		t.Fatal(snapshot, e)
	}
}
func TestNativeAPConstructorAndFactoryValidateCredentialsAndSession(t *testing.T) {
	for _, creds := range []wireless.APCredentials{{}, {Password: "bad\n"}, {Password: "valid", Username: " "}} {
		if _, e := NewAPSession(nil, creds, nil); e == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
	if _, e := APFactory(nil)(t.Context(), nil, wireless.APCredentials{}); e == nil {
		t.Fatal("missing native controller accepted")
	}
	if _, e := Borrow(nil); e == nil {
		t.Fatal("nil device accepted")
	}
}
func TestNativeBodyRemovesOnlyFirstEchoAndPreservesIndentation(t *testing.T) {
	command := "display wlan client verbose"
	d, _ := fixture(t, []exchange{{command + "\n", command + "\r\n  body\r\n" + command + "\r\n<WLC>"}}, false, nil)
	s, _ := Borrow(d)
	r, _ := wireless.NewReader(s, nil)
	cap, e := r.Read(t.Context(), command)
	if e != nil || cap.Body() != "  body\n"+command+"\n" {
		t.Fatalf("body=%q err=%v", cap.Body(), e)
	}
}

func TestNativeInvalidEncodingRetainsReceipt(t *testing.T) {
	command := "display wlan client verbose"
	output := "invalid\xff\r\n<WLC>"
	d, _ := fixture(t, []exchange{{command + "\n", output}}, false, nil)
	s, _ := Borrow(d)
	r, _ := wireless.NewReader(s, nil)
	cap, e := r.Read(t.Context(), command)
	if e != nil || cap.Success() || cap.Failure().Code() != "invalid_output_encoding" || cap.Evidence()[0]["bytes"] != len(output) || d.Connected() {
		t.Fatal(cap, e, cap.Evidence())
	}
}

func TestNativeControllerReadFailurePreservesCloseDiagnostic(t *testing.T) {
	d, _ := fixture(t, []exchange{{"display wlan client verbose\n", "partial"}}, true, nil)
	s, _ := Borrow(d)
	r, _ := wireless.NewReader(s, nil)
	cap, e := r.Read(t.Context(), "display wlan client verbose")
	if e != nil || cap.Success() || s.CloseFailure() == nil || s.CloseFailure().Code() != "session_close_failed" || d.Connected() {
		t.Fatal(cap, e, s.CloseFailure())
	}
	diagnostic := s.CloseFailure()
	*diagnostic = wireless.Failure{}
	if s.CloseFailure().Code() != "session_close_failed" {
		t.Fatal("retained close diagnostic was mutable")
	}
}

func TestNativeAdapterFormattersAndFactoryUseBorrowedController(t *testing.T) {
	d, _ := fixture(t, nil, false, nil)
	s, _ := Borrow(d)
	reader, e := APFactory(nil)(t.Context(), s, wireless.APCredentials{Password: "private credential"})
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
	e := d.WithOperation(t.Context(), func(ctx context.Context, op *connector.Operation) error {
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
	if e != nil {
		t.Fatal(e)
	}
}

func TestReviewLateCancellationRetainsOwnedCloseFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, _ := fixture(t, []exchange{{"display wlan client verbose\n", "Total number of clients : 0\r\n<WLC>"}}, true, func(e connector.Event) {
		if e.Name == "operation_complete" {
			cancel()
		}
	})
	s, _ := Borrow(d)
	r, _ := wireless.NewReader(s, nil)
	cap, e := r.Read(ctx, "display wlan client verbose")
	if e != nil || cap.Success() || cap.Failure().Code() != "cancelled" || d.Connected() {
		t.Fatalf("cancellation result %v %v", cap, e)
	}
	if s.CloseFailure() == nil || s.CloseFailure().Code() != "session_close_failed" {
		t.Fatal("late cancellation lost owned close failure")
	}
}

func TestReviewOperationCallbackReturnRetainsFallbackCloseResult(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, _ := fixture(t, nil, true, nil)
	operationErr, closeErr := d.WithOperationCleanup(ctx, func(ctx context.Context, op *connector.Operation) error {
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
	steps := append(loginSteps(), exchange{"screen-length disable\n", "<AP>"}, exchange{wireless.LLDPCommand + "\n", "No LLDP neighbors\r\n<AP>"}, exchange{"quit\n", "<WLC>"})
	d, _ := fixture(t, steps, true, func(e connector.Event) {
		if e.Name == "operation_complete" {
			completions++
			if completions == 4 {
				cancel()
			}
		}
	})
	out, e := makeAP(t, d).Read(ctx, apRecord())
	if !errors.Is(e, context.Canceled) || out.Error == nil || out.CloseError == nil || out.Result == nil || !out.Result.Success() || d.Connected() {
		t.Fatalf("AP late outcome %v, primary=%v, close=%v", e, out.Error, out.CloseError)
	}
}

func TestReviewNativeOpenPreservesCancellationAndCloseIndependently(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, _ := fixture(t, []exchange{{"screen-length disable\n", "<WLC>"}, {"display wlan client verbose\n", "Total number of clients : 0\r\n<WLC>"}}, true, nil)
	nativeOpen := Open(d)
	open := func(ctx context.Context, run func(wireless.Session) error) error {
		return nativeOpen(ctx, func(s wireless.Session) error { err := run(s); cancel(); return err })
	}
	snapshot, e := wireless.NewCollector(wireless.CollectorOptions{}).Collect(ctx, wireless.CollectOptions{Target: wireless.Record{"host": "fixture"}, Platform: "h3c", ConnectorPlatform: "h3c_wireless", Dimensions: []wireless.Dimension{wireless.Clients}, Open: open})
	if e != nil || !snapshot.Dimension(wireless.Clients).Complete() {
		t.Fatal(snapshot, e)
	}
	issues := snapshot.Dimension(wireless.Collection).Issues()
	if len(issues) != 2 || issues[0]["code"] != "cancelled" || issues[1]["code"] != "session_close_failed" {
		t.Fatalf("native opener diagnostics %v", issues)
	}
}
