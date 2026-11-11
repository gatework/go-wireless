package rules_test

import (
	"slices"
	"testing"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/evidence"
	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/wlan"
)

func run(t *testing.T, c *wlan.Controller, policy assess.Policy) []assess.Finding {
	t.Helper()
	a, err := rules.NewAssessor(policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Assess(t.Context(), []*wlan.Controller{c})
	if err != nil {
		t.Fatal(err)
	}
	return r.Controllers[0].Assessment.Findings
}

func by(fs []assess.Finding, id assess.RuleID) []assess.Finding {
	return slices.DeleteFunc(slices.Clone(fs), func(f assess.Finding) bool { return f.Rule != id })
}

func h3c(dims ...evidence.Dimension) *wlan.Controller {
	c := &wlan.Controller{Identity: wlan.Identity{Host: "192.0.2.10", Platform: "h3c_wireless"}, Coverage: evidence.Coverage{}}
	for _, d := range dims {
		c.Coverage[d] = evidence.StatusComplete
	}
	return c
}

func template(name string, enabled *bool, ciphers, ies []string) wlan.ServiceTemplate {
	return wlan.ServiceTemplate{Name: name, SSID: new(name), Enabled: enabled, Configuration: wlan.Settings{CipherSuites: ciphers, SecurityIEs: ies}}
}

// Parser-shaped reproduction of legacy recommendation case 0.
func TestSecurityAndBindingMatchLegacyCommands(t *testing.T) {
	c := h3c(wlan.DimConfiguration)
	c.Configuration = &wlan.Configuration{
		ServiceTemplates: []wlan.ServiceTemplate{
			template("corp0", new(true), []string{"ccmp", "tkip"}, []string{"rsn", "wpa"}),
			template("corp1", new(false), []string{"ccmp", "tkip"}, []string{"rsn", "wpa"}),
			template("corp2", nil, []string{"ccmp", "tkip"}, []string{"rsn", "wpa"}),
			template("blocked", new(true), []string{"tkip"}, []string{"wpa"}),
		},
		AccessPoints: map[string]wlan.Settings{"z": {Radios: map[string]wlan.Settings{"2": {ServiceTemplates: []wlan.TemplateBinding{{Name: "missing", VLANID: new(4)}, {Name: "corp1"}}}}}},
		APGroups:     []wlan.APGroup{{Name: "g", Configuration: wlan.Settings{Radios: map[string]wlan.Settings{"1": {ServiceTemplates: []wlan.TemplateBinding{{Name: "second"}}}}}}},
	}
	fs := run(t, c, assess.Policy{})
	security := map[string]*assess.Remediation{}
	for _, f := range by(fs, "wireless.legacy_wlan_security") {
		security[f.Subject.Key] = f.Remediation
	}
	want := map[string][]string{
		"corp0": {"system-view", "wlan service-template corp0", " undo service-template enable", " undo security-ie wpa", " undo cipher-suite tkip", " service-template enable", "quit", "return"},
		"corp1": {"system-view", "wlan service-template corp1", " undo service-template enable", " undo security-ie wpa", " undo cipher-suite tkip", "quit", "return"},
	}
	for name, commands := range want {
		r := security[name]
		if r == nil || r.Status != assess.RemediationCandidate || !slices.Equal(r.Commands, commands) || r.Change == nil {
			t.Errorf("%s: %+v", name, r)
		}
	}
	for _, name := range []string{"corp2", "blocked"} { // unknown enable state; no CCMP/RSN fallback
		if r := security[name]; r == nil || r.Status != assess.RemediationBlocked || len(r.Commands) != 0 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	binding := map[string]string{}
	for _, f := range by(fs, "wireless.template_binding") {
		binding[f.Subject.Key] = f.Detail
	}
	if binding["missing"] != "service_template_missing" || binding["second"] != "service_template_missing" || binding["corp1"] != "bound_service_template_disabled" || len(binding) != 3 {
		t.Fatalf("bindings: %v", binding)
	}
}

func TestRuntimeRules(t *testing.T) {
	c := h3c(wlan.DimAccessPoints, wlan.DimRadios, wlan.DimIPConflicts)
	c.AccessPoints = []wlan.AccessPoint{
		{Name: "ap1", Model: "WA6638", OperationalStatus: "R/M", OSVersion: "7.1.064", ClientCount: new(wlan.Number("80"))},
		{Name: "ap2", Model: "WA6638", OperationalStatus: "Run", OSVersion: "7.1.064"},
		{Name: "ap3", Model: "WA6638", OperationalStatus: "R", OSVersion: "7.1.070"},
		{Name: "ap4", Model: "WA6638", OperationalStatus: "Idle"},
	}
	c.Radios = []wlan.Radio{
		{APName: "ap1", RadioID: "1", Channel: new(6), ChannelWidth: new(wlan.Number("40")), Utilization: new(85), NoiseDBm: new(-70.0)},
		{APName: "ap1", RadioID: "2", Channel: new(36), ChannelWidth: new(wlan.Number("80")), AdminStatus: "Down"},
		{APName: "ap2", RadioID: "1", Channel: new(3), ChannelWidth: new(wlan.Number("20")), Utilization: new(30), NoiseDBm: new(-95.0)},
	}
	c.IPConflicts = []wlan.IPConflict{{IPAddress: "10.0.0.9"}, {IPAddress: "10.0.0.9"}}
	fs := run(t, c, assess.Policy{})
	keys := func(id assess.RuleID) []string {
		var out []string
		for _, f := range by(fs, id) {
			out = append(out, f.Subject.Key)
		}
		return out
	}
	for id, want := range map[assess.RuleID][]string{
		"wireless.ap_not_running":                {"ap4"},
		"wireless.ap_firmware_drift":             {"ap3"},
		"wireless.ap_client_load_high":           {"ap1"},
		"wireless.radio_24g_overlapping_channel": {"1"},
		"wireless.radio_24g_wide_channel":        {"1"},
		"wireless.radio_utilization_high":        {"1"},
		"wireless.radio_noise_high":              {"1"},
		"wireless.radio_admin_down":              {"2"},
		"wireless.client_ip_conflict_history":    {"10.0.0.9"},
	} {
		if got := keys(id); !slices.Equal(got, want) {
			t.Errorf("%s: got %v want %v", id, got, want)
		}
	}
	// Thresholds come from policy.
	fs = run(t, c, assess.Policy{Rules: map[assess.RuleID]assess.RulePolicy{
		"wireless.radio_utilization_high": {Params: assess.Params{"max_percent": 90}},
		"wireless.ap_client_load_high":    {Params: assess.Params{"max_clients": 100}},
	}})
	if len(by(fs, "wireless.radio_utilization_high"))+len(by(fs, "wireless.ap_client_load_high")) != 0 {
		t.Fatal("policy thresholds ignored")
	}
}

func TestHuaweiAPStateIsNormalized(t *testing.T) {
	c := &wlan.Controller{Identity: wlan.Identity{Host: "192.0.2.20", Platform: "huawei"}, Coverage: evidence.Coverage{wlan.DimAccessPoints: evidence.StatusComplete},
		AccessPoints: []wlan.AccessPoint{{Name: "a", OperationalStatus: "normal"}, {Name: "b", OperationalStatus: "fault"}}}
	fs := run(t, c, assess.Policy{})
	if got := by(fs, "wireless.ap_not_running"); len(got) != 1 || got[0].Subject.Key != "b" {
		t.Fatalf("%+v", got)
	}
	if len(by(fs, "wireless.platform_rules_limited")) != 1 {
		t.Fatal("non-H3C platform not flagged")
	}
}

func TestOpenSSIDAndBackupAC(t *testing.T) {
	c := h3c(wlan.DimConfiguration)
	c.Configuration = &wlan.Configuration{
		ServiceTemplates: []wlan.ServiceTemplate{
			template("guest", new(true), nil, nil),
			template("corp", new(true), []string{"ccmp"}, []string{"rsn"}),
			template("portal", new(true), nil, nil),
			template("off", new(false), nil, nil),
		},
		APGroups: []wlan.APGroup{{Name: "default-group"}},
	}
	c.Configuration.ServiceTemplates[2].Configuration.Portal = &wlan.Portal{Domain: "guest"}
	fs := run(t, c, assess.Policy{})
	if got := by(fs, "wireless.open_ssid"); len(got) != 1 || got[0].Subject.Key != "guest" {
		t.Fatalf("open ssid: %+v", got)
	}
	if len(by(fs, "wireless.ac_backup_missing")) != 1 {
		t.Fatal("missing backup AC not flagged")
	}
	c.Configuration.GlobalSettings.BackupACIP = "192.0.2.11"
	if len(by(run(t, c, assess.Policy{}), "wireless.ac_backup_missing")) != 0 {
		t.Fatal("backup AC ignored")
	}
}
