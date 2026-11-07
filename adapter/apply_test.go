package adapter

import (
	"context"
	"errors"
	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	"os"
	"strings"
	"testing"
)

func TestNativeApplyOneLeaseCompleteReadback(t *testing.T) {
	body, e := os.ReadFile("../testdata/ruby-interop/source.cfg")
	if e != nil {
		t.Fatal(e)
	}
	after := strings.ReplaceAll(strings.ReplaceAll(string(body), " cipher-suite tkip\n", ""), " security-ie wpa\n", "")
	steps := []exchange{{"screen-length disable\n", "screen-length disable\r\n<WLC>"}, {"display current-configuration\n", "display current-configuration\r\n" + string(body) + "\r\n<WLC>"}, {"system-view\n", "system-view\r\n[WLC]"}, {"wlan service-template lab\n", "wlan service-template lab\r\n[WLC-wlan-st-lab]"}, {"undo service-template enable\n", "\r\n[WLC-wlan-st-lab]"}, {"undo security-ie wpa\n", "\r\n[WLC-wlan-st-lab]"}, {"undo cipher-suite tkip\n", "\r\n[WLC-wlan-st-lab]"}, {"service-template enable\n", "\r\n[WLC-wlan-st-lab]"}, {"quit\n", "\r\n[WLC]"}, {"return\n", "\r\n<WLC>"}, {"screen-length disable\n", "\r\n<WLC>"}, {"display current-configuration\n", after + "\r\n<WLC>"}}
	d, _ := fixture(t, steps, false, nil)
	var retained wireless.ApplySession
	opErr, closeErr := Apply(t.Context(), d, func(session wireless.ApplySession) error {
		retained = session
		if _, e := d.ExecuteCommand(t.Context(), connector.Command{Text: "unexpected"}, connector.ExecuteOptions{}); !errors.Is(e, connector.ErrBusy) {
			t.Error("operation lease not owned", e)
		}
		before, e := session.ReadConfiguration(t.Context())
		if e != nil {
			return e
		}
		hash, e := wireless.Fingerprint(before)
		if e != nil || hash != "294aea47c2657702a541a4d791b3376d5a96c3a9bcadbda1a606d64545d5d573" {
			t.Fatal(hash, e)
		}
		change := wireless.Record{"kind": "wireless_security", "name": "lab", "enabled": true, "cipher_suites": []string{"ccmp", "tkip"}, "security_ies": []string{"rsn", "wpa"}}
		commands, _, _ := wireless.RenderChange(change)
		for _, cmd := range commands {
			if e = session.Write(t.Context(), cmd); e != nil {
				return e
			}
		}
		facts, e := session.ReadConfiguration(t.Context())
		if e != nil {
			return e
		}
		if !wireless.ChangeSatisfied(facts, change) {
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
			opErr, closeErr := Apply(t.Context(), d, func(session wireless.ApplySession) error { _, e := session.ReadConfiguration(t.Context()); return e })
			if opErr == nil || closeErr != nil || d.Connected() {
				t.Fatal(opErr, closeErr, d.Connected())
			}
		})
	}
}
