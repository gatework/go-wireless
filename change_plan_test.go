package wireless_test

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
)

func TestCanonicalRubyProbes(t *testing.T) {
	b, _ := os.ReadFile("testdata/ruby-canonical-probes.json")
	var cases []struct {
		Input                  string `json:"input_json"`
		Canonical, Fingerprint string
	}
	if e := json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		var v any
		d := json.NewDecoder(strings.NewReader(c.Input))
		d.UseNumber()
		if e := d.Decode(&v); e != nil {
			t.Fatal(e)
		}
		got, e := wireless.CanonicalJSON(v)
		if e != nil || string(got) != c.Canonical {
			t.Fatalf("%s: %s %v", c.Input, got, e)
		}
		h, e := wireless.Fingerprint(v)
		if e != nil || h != c.Fingerprint {
			t.Fatal(h, e)
		}
	}
}
func TestRubyFPConvCorpus(t *testing.T) {
	f, e := os.Open("testdata/ruby-fpconv.json.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	g, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	b, _ := io.ReadAll(g)
	var c struct {
		Cases []struct{ Bits, Canonical string }
	}
	if e = json.Unmarshal(b, &c); e != nil {
		t.Fatal(e)
	}
	for _, v := range c.Cases {
		bits, _ := strconv.ParseUint(v.Bits, 16, 64)
		got, e := wireless.CanonicalJSON(math.Float64frombits(bits))
		if e != nil || string(got) != v.Canonical {
			t.Fatalf("bits=%s got=%s want=%s err=%v", v.Bits, got, v.Canonical, e)
		}
	}
}
func TestCanonicalRejectsInvalidValues(t *testing.T) {
	for _, v := range []any{math.Inf(1), math.NaN(), string([]byte{255}), wireless.Record{string([]byte{255}): 1}, json.Number("01"), json.Number("1e999")} {
		if _, e := wireless.CanonicalJSON(v); e == nil {
			t.Fatalf("accepted %T", v)
		}
	}
}
func TestTypedSecurityChange(t *testing.T) {
	c := wireless.Record{"kind": "wireless_security", "name": "corp", "enabled": true, "cipher_suites": []any{"ccmp", "tkip"}, "security_ies": []any{"rsn", "wpa"}}
	cmd, roll, e := change.RenderChange(c)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Join(cmd, "\n") != "system-view\nwlan service-template corp\n undo service-template enable\n undo security-ie wpa\n undo cipher-suite tkip\n service-template enable\nquit\nreturn" {
		t.Fatal(cmd)
	}
	if len(roll) != 8 || roll[3] != " cipher-suite tkip" {
		t.Fatal(roll)
	}
	c["enabled"] = nil
	if _, _, e = change.RenderChange(c); e == nil {
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
		p, _, e := wireless.DecodeJSONDocument(body)
		if e != nil {
			t.Fatal(e)
		}
		mutate(p)
		if _, err := change.NewChangePlan(p); err == nil {
			t.Fatal("accepted mutation", i)
		}
	}
}
func TestJSONRejectsInvalidUnicodeSurrogates(t *testing.T) {
	for _, body := range []string{`{"s":"\ud800"}`, `{"s":"\udc00"}`, `{"s":"\ud800\u0041"}`} {
		if _, _, e := wireless.DecodeJSONDocument([]byte(body)); e == nil {
			t.Fatal("accepted invalid surrogate", body)
		}
	}
	if _, _, e := wireless.DecodeJSONDocument([]byte(`{"s":"\ud83d\ude00"}`)); e != nil {
		t.Fatal(e)
	}
}
