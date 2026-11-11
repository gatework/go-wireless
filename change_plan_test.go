package wireless_test

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
)

func TestFingerprintRejectsInvalidValues(t *testing.T) {
	for _, v := range []any{math.Inf(1), math.NaN(), string([]byte{255}), wireless.Record{string([]byte{255}): 1}, wireless.Record{"name": string([]byte{0xb2, 0xe2})}, json.Number("01")} {
		if _, e := wireless.Fingerprint(v); e == nil {
			t.Fatalf("accepted %#v", v)
		}
	}
}

// TestFingerprintMatchesStoredDocument pins the property apply relies on: a
// live configuration record and the same record read back from its stored
// JSON fingerprint alike, including integral floats and large integers.
func TestFingerprintMatchesStoredDocument(t *testing.T) {
	live := wireless.Record{"service_templates": []any{wireless.Record{"name": "corp", "enabled": true, "power": 20.0, "ratio": 0.25, "id": json.Number("9007199254740993")}}}
	body, err := wireless.MarshalJSON(live)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := wireless.DecodeJSONDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	a, err := wireless.Fingerprint(live)
	if err != nil {
		t.Fatal(err)
	}
	b, err := wireless.Fingerprint(stored)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("live %s stored %s", a, b)
	}
	changed := wireless.Record{"service_templates": []any{wireless.Record{"name": "corp", "enabled": true, "power": 20.5, "ratio": 0.25, "id": json.Number("9007199254740993")}}}
	if c, _ := wireless.Fingerprint(changed); c == a {
		t.Fatal("a changed value kept the fingerprint")
	}
}

func TestTypedSecurityChange(t *testing.T) {
	c := change.TemplateSecurity{Name: "corp", Enabled: true, CipherSuites: []string{"ccmp", "tkip"}, SecurityIEs: []string{"rsn", "wpa"}}
	script, e := c.Render()
	if e != nil {
		t.Fatal(e)
	}
	if strings.Join(script.Commands, "\n") != "system-view\nwlan service-template corp\n undo service-template enable\n undo security-ie wpa\n undo cipher-suite tkip\n service-template enable\nquit\nreturn" {
		t.Fatal(script.Commands)
	}
	if len(script.Rollback) != 8 || script.Rollback[3] != " cipher-suite tkip" {
		t.Fatal(script.Rollback)
	}
	for _, bad := range []change.TemplateSecurity{
		{Name: "bad name", Enabled: true, CipherSuites: []string{"ccmp", "tkip"}, SecurityIEs: []string{"rsn"}},
		{Name: "corp", Enabled: true, CipherSuites: []string{"tkip"}, SecurityIEs: []string{"rsn"}},
		{Name: "corp", Enabled: true, CipherSuites: []string{"ccmp"}, SecurityIEs: []string{"rsn"}},
	} {
		if _, e := bad.Render(); e == nil {
			t.Fatalf("rendered %+v", bad)
		}
	}
	var unknown any
	_ = json.Unmarshal([]byte(`{"kind":"wireless_security","name":"corp","enabled":null,"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}`), &unknown)
	if _, e := change.DecodeTemplateSecurity(unknown); e == nil {
		t.Fatal("unknown enable accepted")
	}
}

func TestPlanValidationRejectsTamperedBoundaries(t *testing.T) {
	body, e := os.ReadFile("testdata/ruby-interop/security.plan.json")
	if e != nil {
		t.Fatal(e)
	}
	mutations := []func(wireless.Record){func(p wireless.Record) { p["schema_version"] = 2 }, func(p wireless.Record) { p["platform"] = "huawei" }, func(p wireless.Record) { p["device"].(wireless.Record)["host"] = "192.0.2.10/32" }, func(p wireless.Record) { p["device"].(wireless.Record)["host"] = "2001:0db8::1" }, func(p wireless.Record) { p["expected_config_sha256"] = "bad" }, func(p wireless.Record) { p["rollback"] = []string{"return"} }, func(p wireless.Record) { p["change"].(wireless.Record)["enabled"] = nil }, func(p wireless.Record) { p["change"].(wireless.Record)["name"] = "lab\nsave" }, func(p wireless.Record) { p["change"].(wireless.Record)["cipher_suites"] = []any{"tkip"} }, func(p wireless.Record) { p["change"].(wireless.Record)["security_ies"] = []any{"wpa"} }, func(p wireless.Record) { p["change"].(wireless.Record)["kind"] = "arbitrary" }}
	for i, mutate := range mutations {
		p, e := wireless.DecodeJSONDocument(body)
		if e != nil {
			t.Fatal(e)
		}
		mutate(p)
		if _, err := change.NewPlan(p); err == nil {
			t.Fatal("accepted mutation", i)
		}
	}
}
func TestJSONRejectsInvalidUnicodeSurrogates(t *testing.T) {
	for _, body := range []string{`{"s":"\ud800"}`, `{"s":"\udc00"}`, `{"s":"\ud800\u0041"}`} {
		if _, e := wireless.DecodeJSONDocument([]byte(body)); e == nil {
			t.Fatal("accepted invalid surrogate", body)
		}
	}
	if _, e := wireless.DecodeJSONDocument([]byte(`{"s":"\ud83d\ude00"}`)); e != nil {
		t.Fatal(e)
	}
}
