package change_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	wireless "github.com/gatework/go-wireless"
	changes "github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/wlan"
)

// variants enumerates template states that exercise every eligibility and
// readback condition of the wireless_security change.
func variants() map[string]wireless.Record {
	lists := map[string]any{"absent": nil, "empty": []any{}, "ccmp": []any{"ccmp"}, "ccmp_tkip": []any{"ccmp", "tkip"}, "tkip_ccmp": []any{"tkip", "ccmp"}, "tkip": []any{"tkip"}}
	ies := map[string]any{"absent": nil, "empty": []any{}, "rsn": []any{"rsn"}, "rsn_wpa": []any{"rsn", "wpa"}, "wpa": []any{"wpa"}}
	enabled := map[string]any{"absent": nil, "true": true, "false": false}
	out := map[string]wireless.Record{"no_template": {"service_templates": []any{}}}
	for ln, l := range lists {
		for in, i := range ies {
			for en, e := range enabled {
				settings := wireless.Record{}
				if l != nil {
					settings["cipher_suites"] = l
				}
				if i != nil {
					settings["security_ies"] = i
				}
				t := wireless.Record{"name": "corp", "configuration": settings}
				if e != nil {
					t["enabled"] = e
				}
				out[fmt.Sprintf("c=%s/i=%s/e=%s", ln, in, en)] = wireless.Record{"service_templates": []any{t}}
			}
		}
	}
	out["other_name"] = wireless.Record{"service_templates": []any{wireless.Record{"name": "guest", "enabled": true, "configuration": wireless.Record{"cipher_suites": []any{"ccmp", "tkip"}, "security_ies": []any{"rsn", "wpa"}}}}}
	return out
}

func plannedChanges() map[string]changes.TemplateSecurity {
	return map[string]changes.TemplateSecurity{
		"both_enabled":  {Name: "corp", Enabled: true, CipherSuites: []string{"ccmp", "tkip"}, SecurityIEs: []string{"rsn", "wpa"}},
		"both_disabled": {Name: "corp", Enabled: false, CipherSuites: []string{"ccmp", "tkip"}, SecurityIEs: []string{"rsn", "wpa"}},
		"tkip_only":     {Name: "corp", Enabled: true, CipherSuites: []string{"ccmp", "tkip"}, SecurityIEs: []string{"rsn"}},
		"wpa_only":      {Name: "corp", Enabled: true, CipherSuites: []string{"ccmp"}, SecurityIEs: []string{"rsn", "wpa"}},
	}
}

func typedConfig(t *testing.T, cfg wireless.Record) *wlan.Configuration {
	t.Helper()
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var c wlan.Configuration
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func verdict(eligible, satisfied bool) string {
	code := []byte("00")
	if eligible {
		code[0] = '1'
	}
	if satisfied {
		code[1] = '1'
	}
	return string(code)
}

// TestTemplateSecurityMatchesFrozenLegacyVerdicts replays the legacy
// ChangeEligible/ChangeSatisfied verdicts frozen in
// testdata/typed/change-oracle.json for every planned change and variant.
func TestTemplateSecurityMatchesFrozenLegacyVerdicts(t *testing.T) {
	body, err := os.ReadFile("../testdata/typed/change-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle map[string]string
	if err := json.Unmarshal(body, &oracle); err != nil {
		t.Fatal(err)
	}
	compared := 0
	for pn, p := range plannedChanges() {
		for vn, cfg := range variants() {
			c := typedConfig(t, cfg)
			got := verdict(p.Eligible(c) == nil, p.Satisfied(c) == nil)
			want, ok := oracle[pn+"|"+vn]
			if !ok {
				t.Fatalf("%s %s: no frozen verdict", pn, vn)
			}
			if got != want {
				t.Errorf("%s %s: typed %s legacy %s (eligible, satisfied)", pn, vn, got, want)
			}
			compared++
		}
	}
	if compared != len(oracle) {
		t.Fatalf("compared %d of %d frozen verdicts", compared, len(oracle))
	}
	t.Logf("frozen legacy verdicts reproduced: %d", compared)
}

func TestDecodeTemplateSecurityRejectsUnknownState(t *testing.T) {
	valid := `{"kind":"wireless_security","name":"corp","enabled":true,"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}`
	if _, err := changes.DecodeTemplateSecurity(json.RawMessage(valid)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"kind":"wireless_security","name":"corp","enabled":null,"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}`,
		`{"kind":"wireless_security","name":"corp","cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}`,
		`{"kind":"wireless_security","name":12,"enabled":true,"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}`,
		`{"kind":"wireless_security","name":"corp","enabled":true,"cipher_suites":null,"security_ies":["rsn","wpa"]}`,
		`{"kind":"wireless_security","name":"corp","enabled":true,"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"],"commands":["reboot"]}`,
		`{"kind":"other","name":"corp","enabled":true,"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}`,
	} {
		var v any
		_ = json.Unmarshal([]byte(bad), &v)
		if _, err := changes.DecodeTemplateSecurity(v); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
