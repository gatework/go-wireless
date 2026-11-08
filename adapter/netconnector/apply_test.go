package netconnector

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	changes "github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/collect"
)

func TestNativeApplyOneLeaseCompleteReadback(t *testing.T) {
	body, e := os.ReadFile("../../testdata/ruby-interop/source.cfg")
	if e != nil {
		t.Fatal(e)
	}
	after := strings.ReplaceAll(strings.ReplaceAll(string(body), " cipher-suite tkip\n", ""), " security-ie wpa\n", "")
	steps := []exchange{{"screen-length disable\n", "screen-length disable\r\n<WLC>"}, {"display current-configuration\n", "display current-configuration\r\n" + string(body) + "\r\n<WLC>"}, {"system-view\n", "system-view\r\n[WLC]"}, {"wlan service-template lab\n", "wlan service-template lab\r\n[WLC-wlan-st-lab]"}, {"undo service-template enable\n", "\r\n[WLC-wlan-st-lab]"}, {"undo security-ie wpa\n", "\r\n[WLC-wlan-st-lab]"}, {"undo cipher-suite tkip\n", "\r\n[WLC-wlan-st-lab]"}, {"service-template enable\n", "\r\n[WLC-wlan-st-lab]"}, {"quit\n", "\r\n[WLC]"}, {"return\n", "\r\n<WLC>"}, {"screen-length disable\n", "\r\n<WLC>"}, {"display current-configuration\n", after + "\r\n<WLC>"}}
	d, _ := fixture(t, steps, false, nil)
	var retained changes.ApplySession
	opErr, closeErr := Apply(t.Context(), d, func(ctx context.Context, session changes.ApplySession) error {
		retained = session
		if _, e := d.ExecuteCommand(t.Context(), connector.Command{Text: "unexpected"}, connector.ExecuteOptions{}); !errors.Is(e, connector.ErrBusy) {
			t.Error("operation lease not owned", e)
		}
		before, e := session.ReadConfiguration(ctx)
		if e != nil {
			return e
		}
		hash, e := wireless.Fingerprint(before)
		if e != nil || hash != "294aea47c2657702a541a4d791b3376d5a96c3a9bcadbda1a606d64545d5d573" {
			t.Fatal(hash, e)
		}
		change := wireless.Record{"kind": "wireless_security", "name": "lab", "enabled": true, "cipher_suites": []string{"ccmp", "tkip"}, "security_ies": []string{"rsn", "wpa"}}
		commands, _, _ := changes.RenderChange(change)
		for _, cmd := range commands {
			if e = session.Write(ctx, cmd); e != nil {
				return e
			}
		}
		facts, e := session.ReadConfiguration(ctx)
		if e != nil {
			return e
		}
		if !changes.ChangeSatisfied(facts, change) {
			t.Fatal("readback mismatch", facts)
		}
		return nil
	})
	if opErr != nil || closeErr != nil {
		t.Fatal(opErr, closeErr)
	}
	if d.Connected() {
		t.Fatal("not retired")
	}
	if e := retained.Write(context.Background(), "return"); e == nil {
		t.Fatal("retained session accepted")
	}
}
func TestNativeApplyRejectedOrPartialReadRetires(t *testing.T) {
	for _, body := range []string{"% Unrecognized command found at '^' position.\r\n<WLC>", "wlan service-template x\n cipher-suite ccmp\n unsupported-syntax\n#\n<WLC>"} {
		t.Run(body[:6], func(t *testing.T) {
			steps := []exchange{{"screen-length disable\n", "<WLC>"}, {"display current-configuration\n", body}}
			d, _ := fixture(t, steps, false, nil)
			opErr, closeErr := Apply(t.Context(), d, func(ctx context.Context, session changes.ApplySession) error {
				_, e := session.ReadConfiguration(ctx)
				return e
			})
			if opErr == nil || closeErr != nil || d.Connected() {
				t.Fatal(opErr, closeErr, d.Connected())
			}
		})
	}
}

func TestNativeOpenersDoNotCloseAnotherOperationsLease(t *testing.T) {
	for _, mode := range []string{"collect", "apply"} {
		t.Run(mode, func(t *testing.T) {
			device, transport := fixture(t, nil, false, nil)
			operationErr, cleanupErr := device.WithBorrowedOperation(t.Context(), func(ctx context.Context, op *connector.Operation) error {
				var err, closeErr error
				if mode == "collect" {
					err = Open(device)(ctx, func(collect.Session) error {
						t.Error("busy opener invoked callback")
						return nil
					})
				} else {
					err, closeErr = Apply(ctx, device, func(context.Context, changes.ApplySession) error {
						t.Error("busy opener invoked callback")
						return nil
					})
				}
				failure, ok := errors.AsType[*wireless.Failure](err)
				if !ok || failure.Code() != "session_busy" || closeErr != nil {
					t.Fatalf("busy result: %v, cleanup=%v", err, closeErr)
				}
				if ctx.Err() != nil || !op.Connected() || transport.closes.Load() != 0 {
					t.Fatal("opener retired another operation's session")
				}
				return nil
			})
			if operationErr != nil || cleanupErr != nil || !device.Connected() {
				t.Fatal(operationErr, cleanupErr)
			}
		})
	}
}
