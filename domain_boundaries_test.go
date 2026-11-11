package wireless_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
	h3c "github.com/gatework/go-wireless/vendors/h3c"
)

func TestJSONNumberGrammarAndPrecision(t *testing.T) {
	for _, raw := range []string{"NaN", "Inf", "+1", "01", ".5", "1.", "1e", "--1", " 1", "1 2"} {
		if _, e := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Clients, Status: wireless.Complete, Records: []wireless.Record{{"number": json.Number(raw)}}}); e == nil {
			t.Fatalf("accepted non JSON number %q", raw)
		}
	}
	large := strings.Repeat("9", 400)
	o, e := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Clients, Status: wireless.Complete, Records: []wireless.Record{{"number": json.Number(large)}}})
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(o)
	if e != nil || !strings.Contains(string(b), large) {
		t.Fatal("lost arbitrary precision number")
	}
	var restored wireless.Observation
	if e = json.Unmarshal(b, &restored); e != nil {
		t.Fatal(e)
	}
	if restored.Records()[0]["number"] != json.Number(large) {
		t.Fatal("coerced integer")
	}
	at := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	s, e := wireless.NewSnapshot(wireless.SnapshotOptions{Target: wireless.Record{"number": json.Number(large)}, Dimensions: map[wireless.Dimension]wireless.Observation{wireless.Clients: o}, ObservedAt: at})
	if e != nil {
		t.Fatal(e)
	}
	b, e = json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	var snapshot wireless.Snapshot
	if e = json.Unmarshal(b, &snapshot); e != nil {
		t.Fatal(e)
	}
	if snapshot.Target()["number"] != json.Number(large) {
		t.Fatal("coerced target integer")
	}
}
func TestInvalidDomainInputsAndSafeErrors(t *testing.T) {
	cycle := wireless.Record{}
	cycle["self"] = cycle
	for _, value := range []any{cycle, math.Inf(1), math.NaN(), func() {}, make(chan int), map[int]string{1: "PRIVATE"}} {
		_, e := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Clients, Status: wireless.Complete, Records: []wireless.Record{{"private": value}}})
		if e == nil {
			t.Fatal("accepted invalid evidence")
		}
		if strings.Contains(e.Error(), "PRIVATE") {
			t.Fatal("unsafe error")
		}
	}
	if _, e := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Clients, Status: "invalid"}); e == nil {
		t.Fatal("invalid status")
	}
	if _, e := wireless.NewSnapshot(wireless.SnapshotOptions{Target: wireless.Record{}, Dimensions: map[wireless.Dimension]wireless.Observation{}, ObservedAt: time.Now()}); e == nil {
		t.Fatal("empty snapshot")
	}
	if _, e := wireless.NewCoverage(wireless.CoverageOptions{Now: time.Now(), StaleAfter: math.Inf(1)}); e == nil {
		t.Fatal("infinite freshness")
	}
	if _, e := wireless.NewCoverage(wireless.CoverageOptions{Now: time.Time{}, StaleAfter: 1}); e == nil {
		t.Fatal("invalid clock")
	}
	for _, wire := range []string{`{"name":"clients","status":"complete","records":{}}`, `{"name":"clients","status":"complete","records":[null]}`, `{"name":"clients","status":"failed","records":[{}]}`, `{"name":"clients","status":"complete","started_at":123}`} {
		var o wireless.Observation
		if e := json.Unmarshal([]byte(wire), &o); e == nil {
			t.Fatal("accepted invalid wire observation")
		}
	}
}
func TestSnapshotProjectionAndCoverageOwnership(t *testing.T) {
	at := time.Date(2026, 10, 26, 8, 0, 0, 123456000, time.UTC)
	o, _ := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Clients, Status: wireless.Complete, StartedAt: &at})
	target := wireless.Record{"name": "PRIVATE", "nested": wireless.Record{"x": 1}}
	dims := map[wireless.Dimension]wireless.Observation{wireless.Clients: o}
	s, e := wireless.NewSnapshot(wireless.SnapshotOptions{target, dims, at})
	if e != nil {
		t.Fatal(e)
	}
	delete(dims, wireless.Clients)
	target["nested"].(wireless.Record)["x"] = 2
	duplicate := s.Target()
	duplicate["nested"].(wireless.Record)["x"] = 3
	if !s.Complete() || s.Target()["nested"].(wireless.Record)["x"] != 1 {
		t.Fatal("snapshot ownership")
	}
	if s.Dimension(wireless.LLDP).Status() != wireless.NotRequested {
		t.Fatal("missing dimension")
	}
	ts := o.StartedAt()
	*ts = at.Add(time.Hour)
	if !o.StartedAt().Equal(at) {
		t.Fatal("timestamp ownership")
	}
	projectionInput := wireless.Record{"clients": wireless.Record{"status": "complete", "private": "PRIVATE"}}
	projection, e := wireless.NewProjection(projectionInput)
	if e != nil {
		t.Fatal(e)
	}
	projectionInput["clients"].(wireless.Record)["status"] = "blocked"
	out := projection.Dimensions()
	out["clients"].(wireless.Record)["status"] = "failed"
	if !projection.Complete() {
		t.Fatal("projection ownership")
	}
	empty, _ := wireless.NewProjection(nil)
	if !empty.Complete() {
		t.Fatal("empty projection")
	}
	history := wireless.Record{"clients": wireless.Record{"latest": wireless.Record{"status": "complete", "observed_at": at.Format(time.RFC3339Nano)}}}
	coverage, e := wireless.NewCoverage(wireless.CoverageOptions{Observations: history, Dimensions: []wireless.Dimension{wireless.Clients}, Now: at, StaleAfter: 0})
	if e != nil {
		t.Fatal(e)
	}
	history["clients"].(wireless.Record)["latest"].(wireless.Record)["status"] = "failed"
	if coverage.ToMap()["state"] != "complete" {
		t.Fatal("coverage ownership")
	}
	for _, value := range []any{o, s, projection, coverage} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), "PRIVATE") {
				t.Fatal("unsafe formatting")
			}
		}
	}
	b, _ := json.Marshal(o)
	if !strings.Contains(string(b), `"records":[]`) || !strings.Contains(string(b), `"completed_at":null`) || !strings.Contains(string(b), ".123456Z") {
		t.Fatal(string(b))
	}
}
func TestConfigurationSourceCapturesAreOwnedAndRedacted(t *testing.T) {
	input := "wlan service-template GUEST\r\n ssid Guest Network\r\n preshared-key pass-phrase cipher PRIVATE\r\n service-template enable\r\n#\r\nreturn\r\n"
	source, e := h3c.ParseConfigurationSource(input, true)
	if e != nil {
		t.Fatal(e)
	}
	if !source.Observation().Complete() || source.Content() != input || source.UnparsedContent() != "" {
		t.Fatal("configuration source contract")
	}
	captures := source.CapturesFor([]any{"service_templates", 0})
	if len(captures) != 1 {
		t.Fatal("capture missing")
	}
	c := captures[0]
	if !c.Complete() || c.FirstLine() != 1 || c.LastLine() != 4 {
		t.Fatal("wrong source range")
	}
	data := c.Data().(wireless.Record)
	if data["authentication_type"] != "psk" {
		t.Fatal(data)
	}
	data["ssid"] = "Changed"
	path := c.Path()
	path[0] = "changed"
	if c.Data().(wireless.Record)["ssid"] != "Guest Network" || !reflect.DeepEqual(c.Path(), []any{"service_templates", 0}) {
		t.Fatal("capture ownership")
	}
	states := source.States()
	states[0] = "invalid"
	if source.States()[0] != "parsed" {
		t.Fatal("state ownership")
	}
	for _, v := range []any{source, c, source.Lines()[2]} {
		if strings.Contains(fmt.Sprintf("%#v", v), "PRIVATE") {
			t.Fatal("format leaks source")
		}
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), "PRIVATE") {
			t.Fatal("metadata JSON leaks source")
		}
	}
}
func TestIPBracketFamily(t *testing.T) {
	if _, e := parser.ParseIP(new("[192.0.2.1]")); e == nil {
		t.Fatal("accepted bracketed IPv4")
	}
	v, e := parser.ParseIP(new("[2001:0db8::1]"))
	if e != nil || v == nil || *v != "2001:db8::1" {
		t.Fatal("lost bracketed IPv6")
	}
}

func TestTypedStatusesAndUnicodeSSIDWhitespace(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	coverage, e := wireless.NewCoverage(wireless.CoverageOptions{Observations: wireless.Record{"clients": wireless.Record{"latest": wireless.Record{"status": wireless.Complete, "observed_at": now.Format(time.RFC3339Nano)}}}, Dimensions: []wireless.Dimension{wireless.Clients}, Now: now, StaleAfter: 1})
	if e != nil || coverage.ToMap()["state"] != "complete" {
		t.Fatal("typed status is a JSON string", coverage.ToMap(), e)
	}
	c, _ := vendors.NewBuiltinRegistry().Fetch("h3c", "")
	o, e := c.Parse(wireless.Configuration, "wlan service-template T\n ssid \u00a0Corporate\u00a0\nreturn\n", true)
	if e != nil || !o.Complete() {
		t.Fatal(o.Issues(), e)
	}
	if o.Records()[0]["service_templates"].([]any)[0].(wireless.Record)["ssid"] != "\u00a0Corporate\u00a0" {
		t.Fatal("trimmed meaningful Unicode SSID whitespace")
	}
}

func TestEvidenceCopyNativeAndNamedMapsPreserveExactValues(t *testing.T) {
	type namedRecord map[string]any
	for _, named := range []bool{false, true} {
		original := wireless.Record{
			"integer": json.Number("9007199254740993"), "float": 1.0,
			"empty": []any{}, "null": []any(nil),
			"nested": wireless.Record{"value": "original"},
		}
		var input any = original
		if named {
			input = namedRecord(original)
		}
		copied, err := wireless.CopyValue(input)
		if err != nil {
			t.Fatal(err)
		}
		original["nested"].(wireless.Record)["value"] = "changed"
		body, err := wireless.MarshalJSON(copied)
		want := `{"empty":[],"float":1.0,"integer":9007199254740993,"nested":{"value":"original"},"null":null}`
		if err != nil || string(body) != want {
			t.Fatalf("named=%t copy=%s error=%v", named, body, err)
		}
	}
}
