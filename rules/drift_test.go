package rules_test

import (
	"slices"
	"testing"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/evidence"
	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/wlan"
)

func keyed(fs []assess.Finding) map[assess.RuleID][]string {
	out := map[assess.RuleID][]string{}
	for _, f := range fs {
		out[f.Rule] = append(out[f.Rule], f.Subject.Key+" "+f.Detail)
	}
	return out
}

func controllerWith(aps []wlan.AccessPoint, templates ...wlan.ServiceTemplate) *wlan.Controller {
	c := h3c(wlan.DimConfiguration, wlan.DimAccessPoints, wlan.DimLLDP)
	c.Configuration = &wlan.Configuration{ServiceTemplates: templates}
	c.AccessPoints = aps
	return c
}

func TestWirelessBaseline(t *testing.T) {
	corp := template("corp", ptr(true), []string{"ccmp"}, []string{"rsn"})
	corp.VLANID = ptr(20)
	c := controllerWith([]wlan.AccessPoint{{Name: "ap1", Model: "WA6638", OperationalStatus: "R/M", OSVersion: "7.1.064"}}, corp)
	b := wlan.Baseline{SchemaVersion: 1,
		Defaults: wlan.Expectation{
			ServiceTemplates: map[string]wlan.TemplateExpectation{"corp": {VLAN: ptr(10), CipherSuites: []string{"ccmp", "gcmp256"}}, "guest": {}},
			APFirmware:       map[string]string{"WA6638": "7.1.070"},
			BackupAC:         ptr("192.0.2.11"),
		},
	}
	a, _ := rules.NewAssessor(assess.Policy{}, nil)
	r, err := a.AssessWith(t.Context(), []*wlan.Controller{c}, rules.Inputs{Baseline: &b})
	if err != nil {
		t.Fatal(err)
	}
	got := keyed(r.Baseline[0].Assessment.Findings)
	if !slices.Equal(got["wireless.baseline_service_template"], []string{"corp vlan mismatch", "corp cipher_suites missing", "guest service_template missing"}) {
		t.Errorf("templates: %v", got["wireless.baseline_service_template"])
	}
	if len(got["wireless.baseline_ap_firmware"]) != 1 || len(got["wireless.baseline_backup_ac"]) != 1 {
		t.Errorf("firmware/backup: %v", got)
	}
	// A derived baseline reproduces the current state, so it reports nothing.
	derived := rules.DeriveBaseline([]*wlan.Controller{c})
	body, _ := derived.Encode()
	back, err := wlan.DecodeBaseline(body)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = a.AssessWith(t.Context(), []*wlan.Controller{c}, rules.Inputs{Baseline: &back})
	if n := len(r.Baseline[0].Assessment.Findings); n != 0 {
		t.Fatalf("derived baseline drifted: %v", r.Baseline[0].Assessment.Findings)
	}
	if _, err := wlan.DecodeBaseline([]byte("schema_version: 1\ndefaults: {backup_ac: nope}\n")); err == nil {
		t.Fatal("invalid backup_ac accepted")
	}
}

func TestWirelessChanges(t *testing.T) {
	corp := template("corp", ptr(true), []string{"ccmp"}, []string{"rsn"})
	before := controllerWith([]wlan.AccessPoint{
		{Name: "ap1", OperationalStatus: "R/M", OSVersion: "7.1.064"},
		{Name: "ap2", OperationalStatus: "R/M"},
		{Name: "ap3", OperationalStatus: "R/M"},
	}, corp, template("guest", ptr(true), nil, nil))
	before.LLDP = []wlan.LLDPNeighbor{{APName: "ap1", LocalInterface: "GE1/0/1", RemoteName: "sw1", RemoteInterface: "GE1/0/10"}}
	after := controllerWith([]wlan.AccessPoint{
		{Name: "ap1", OperationalStatus: "R/M", OSVersion: "7.1.070"},
		{Name: "ap2", OperationalStatus: "Idle"},
	}, corp)
	after.LLDP = []wlan.LLDPNeighbor{{APName: "ap1", LocalInterface: "GE1/0/1", RemoteName: "sw2", RemoteInterface: "GE1/0/3"}}
	a, _ := rules.NewAssessor(assess.Policy{}, nil)
	r, err := a.AssessWith(t.Context(), []*wlan.Controller{after}, rules.Inputs{Previous: []*wlan.Controller{before}})
	if err != nil {
		t.Fatal(err)
	}
	got := keyed(r.Changes[0].Assessment.Findings)
	for id, want := range map[assess.RuleID]int{"wireless.change_ap_disappeared": 1, "wireless.change_ap_down": 1, "wireless.change_ap_uplink": 1,
		"wireless.change_ap_firmware": 1, "wireless.change_service_template": 1} {
		if len(got[id]) != want {
			t.Errorf("%s: %v", id, got[id])
		}
	}
	after.Coverage[wlan.DimLLDP] = evidence.StatusPartial
	r, _ = a.AssessWith(t.Context(), []*wlan.Controller{after}, rules.Inputs{Previous: []*wlan.Controller{before}})
	if len(keyed(r.Changes[0].Assessment.Findings)["wireless.change_ap_uplink"]) != 0 {
		t.Fatal("uplink change inferred from partial LLDP evidence")
	}
}
