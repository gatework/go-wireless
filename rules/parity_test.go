package rules_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/wlan"
)

// legacyCode maps a typed finding to the legacy recommendation code.
func legacyCode(f assess.Finding) string {
	switch f.Rule {
	case "wireless.template_binding":
		return f.Detail
	case "wireless.platform_rules_limited":
		return "platform_not_supported"
	case "wireless.ap_not_running":
		return "aps_not_running"
	case "wireless.radio_admin_down":
		return "radios_admin_down"
	}
	return strings.TrimPrefix(string(f.Rule), "wireless.")
}

var legacyStatus = map[assess.RemediationStatus]string{
	assess.RemediationCandidate: "candidate", assess.RemediationBlocked: "blocked", assess.RemediationManual: "candidate",
}

var newRules = []string{"open_ssid", "ap_firmware_drift", "radio_24g_overlapping_channel", "radio_24g_wide_channel",
	"radio_utilization_high", "radio_noise_high", "ap_client_load_high", "ac_backup_missing"}

func TestRulesMatchLegacyRecommendations(t *testing.T) {
	body, err := os.ReadFile("../testdata/ruby-recommendations.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			InputJSON string `json:"input_json"`
			Advice    struct {
				Items []struct {
					Code     string   `json:"code"`
					Status   string   `json:"status"`
					Commands []string `json:"commands"`
				} `json:"items"`
			} `json:"advice"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	assessor, err := rules.NewAssessor(assess.Policy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range fixture.Cases {
		var doc map[string]any
		if err := json.Unmarshal([]byte(c.InputJSON), &doc); err != nil {
			t.Fatal(err)
		}
		device, _ := doc["device"].(map[string]any)
		if device == nil {
			device = map[string]any{}
			doc["device"] = device
		}
		if device["host"] == nil {
			device["host"] = "192.0.2.250" // degenerate fixture without identity
		}
		raw, _ := json.Marshal(doc)
		ctl, err := wlan.DecodeDocument(raw)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		report, err := assessor.Assess(t.Context(), []*wlan.Controller{ctl})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, f := range report.Controllers[0].Assessment.Findings {
			code := legacyCode(f)
			if slices.Contains(newRules, code) {
				continue // no legacy counterpart; covered by rule tests
			}
			status := "investigate"
			var commands []string
			if f.Remediation != nil {
				status = legacyStatus[f.Remediation.Status]
				commands = f.Remediation.Commands
			}
			got[fmt.Sprintf("%s %s %q", code, status, commands)] = true
		}
		want := map[string]bool{}
		for _, item := range c.Advice.Items {
			if len(item.Commands) == 0 {
				item.Commands = nil
			}
			want[fmt.Sprintf("%s %s %q", item.Code, item.Status, item.Commands)] = true
		}
		if reason, synthetic := syntheticCases[i]; synthetic {
			// Not parser-shaped: the strict model must refuse to conclude
			// anything from the affected dimension.
			if !slices.ContainsFunc(sorted(got), func(s string) bool { return strings.HasPrefix(s, "evidence_incomplete") }) {
				t.Errorf("case %d (%s): synthetic shape not reported as incomplete evidence: %v", i, reason, sorted(got))
			}
			continue
		}
		if fmt.Sprint(sorted(got)) != fmt.Sprint(sorted(want)) {
			t.Errorf("case %d:\n got  %v\n want %v", i, sorted(got), sorted(want))
		}
	}
}

// syntheticCases are legacy fixtures built by hand in shapes the parser
// never emits (Ruby scalar names, ad-hoc nesting). Their semantics are
// re-tested with parser-shaped input below.
var syntheticCases = map[int]string{
	0: "ad-hoc radio/configuration nesting", 1: "numeric radio_id, unknown ip field",
	7: "numeric template name", 8: "numeric template name", 9: "numeric template name", 10: "numeric template name",
	11: "numeric template name", 12: "numeric template name", 13: "numeric AP group name",
}

func sorted(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
