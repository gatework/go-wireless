package change_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gatework/go-netops/change"
	"github.com/gatework/go-netops/evidence"
	changes "github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/wlan"
)

func cfg(enabled bool, ciphers, ies []string) *wlan.Configuration {
	return &wlan.Configuration{ServiceTemplates: []wlan.ServiceTemplate{{Name: "corp", Enabled: &enabled, Configuration: wlan.Settings{CipherSuites: ciphers, SecurityIEs: ies}}}}
}

func TestTemplateSecurityPlan(t *testing.T) {
	c := changes.TemplateSecurity{Name: "corp", Enabled: true, CipherSuites: []string{"ccmp", "tkip"}, SecurityIEs: []string{"rsn", "wpa"}}
	digest := evidence.Digest([]byte("config"))
	plan, err := change.NewPlan[*wlan.Configuration](change.Target{Host: "192.0.2.10", Platform: "h3c_wireless"}, digest, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CheckBefore(cfg(true, []string{"ccmp", "tkip"}, []string{"rsn", "wpa"}), digest); err != nil {
		t.Fatal(err)
	}
	if err := plan.CheckBefore(cfg(false, []string{"ccmp", "tkip"}, []string{"rsn", "wpa"}), digest); !errors.Is(err, change.ErrNotEligible) {
		t.Fatalf("changed enable state accepted: %v", err)
	}
	if err := plan.CheckAfter(cfg(true, []string{"ccmp"}, []string{"rsn"})); err != nil {
		t.Fatal(err)
	}
	if err := plan.CheckAfter(cfg(true, []string{"ccmp", "tkip"}, []string{"rsn"})); !errors.Is(err, change.ErrNotSatisfied) {
		t.Fatalf("incomplete readback accepted: %v", err)
	}
	body, _ := json.Marshal(plan)
	back, err := change.DecodePlan(body, func(kind change.Kind, raw json.RawMessage) (change.Change[*wlan.Configuration], error) {
		if kind != changes.KindTemplateSecurity {
			return nil, errors.New("kind not executable")
		}
		var v changes.TemplateSecurity
		return v, json.Unmarshal(raw, &v)
	})
	if err != nil || back.Commands()[1] != "wlan service-template corp" {
		t.Fatalf("%v %v", back, err)
	}
	if _, err := change.NewPlan[*wlan.Configuration](change.Target{Host: "192.0.2.10", Platform: "h3c_wireless"}, digest,
		changes.TemplateSecurity{Name: "corp", Enabled: true, CipherSuites: []string{"tkip"}, SecurityIEs: []string{"wpa"}}); err == nil {
		t.Fatal("plan without CCMP/RSN fallback accepted")
	}
}
