package wireless

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProfileQueriesAliasesAndCopies(t *testing.T) {
	p, e := NewProfile(" H3C_WIRELESS ", " MODEL-A ")
	if e != nil {
		t.Fatal(e)
	}
	if p.Platform() != "h3c" || p.Model() != "model-a" || p.PreparationCommand() != "screen-length disable" {
		t.Fatal("profile identity")
	}
	hw, _ := NewProfile("huawei", "")
	if hw.PreparationCommand() != "screen-length 0 temporary" {
		t.Fatal("Huawei preparation")
	}
	queries, e := p.Queries([]Dimension{Clients, Radios, AccessPoints, APGroups, ServiceTemplates})
	if e != nil {
		t.Fatal(e)
	}
	want := []Query{{"display wlan client verbose", []Dimension{Clients}}, {"display wlan ap all verbose", []Dimension{Radios, AccessPoints}}, {"display current-configuration", []Dimension{APGroups, ServiceTemplates}}}
	if !reflect.DeepEqual(queries, want) {
		t.Fatal(queries)
	}
	queries[1].Dimensions[0] = Clients
	commands := p.Commands()
	commands[Clients] = "evil"
	fresh, _ := p.Queries([]Dimension{Radios})
	if fresh[0].Dimensions[0] != Radios || p.Commands()[Clients] != "display wlan client verbose" {
		t.Fatal("registry ownership")
	}
	if len(SupportedPlatforms()) != 3 || len(SupportedDimensions()) != 13 {
		t.Fatal("registry dimensions")
	}
	for _, input := range []string{"", "unknown", "h3c\nPRIVATE"} {
		if _, e := NewProfile(input, ""); e == nil {
			t.Fatal("invalid platform")
		}
	}
	if _, e := p.Queries([]Dimension{Clients, Clients}); e == nil {
		t.Fatal("duplicate query")
	}
	if _, e := p.Queries([]Dimension{Collection}); e == nil {
		t.Fatal("unsupported query")
	}
}
func TestConfigViewsAndNeighborAPI(t *testing.T) {
	runtime, _ := NewRuntimeParser("")
	out, e := runtime.ParseMany([]Dimension{Configuration, APGroups, ServiceTemplates}, "wlan ap-group LAB\n ap AP\n#\nwlan service-template EAP\n ssid Corporate\nreturn\n", ParseOptions{Complete: true})
	if e != nil {
		t.Fatal(e)
	}
	if !out[APGroups].Complete() || len(out[APGroups].Records()) != 1 || out[APGroups].Records()[0]["name"] != "LAB" || out[ServiceTemplates].Records()[0]["ssid"] != "Corporate" {
		t.Fatal("configuration projection")
	}
	neighbor, _ := NewNeighborParser("h3c", "")
	o, e := neighbor.Parse("LLDP neighbor-information of port 1[GE1]:\nLLDP neighbor index: 1\nChassis ID: switch\nPort ID: GE2\n", "AP-1", true)
	if e != nil || !o.Complete() || o.Records()[0]["ap_name"] != "AP-1" {
		t.Fatal("neighbor association", o.Issues(), e)
	}
	for _, name := range []string{"", " \t"} {
		if _, e := neighbor.Parse("", name, true); e == nil {
			t.Fatal("invalid AP name")
		}
	}
	unsupported, _ := NewNeighborParser("huawei", "")
	o, e = unsupported.Parse("", "AP", true)
	if e != nil || o.Status() != Unsupported {
		t.Fatal("unsupported LLDP")
	}
	config, _ := NewConfig("huawei", "")
	if _, e := config.Parse("", true); e == nil {
		t.Fatal("unsupported configuration")
	}
	o, e = runtime.Parse(Clients, string([]byte{0xff}), ParseOptions{Complete: true})
	if e != nil || o.Status() != Failed {
		t.Fatal("invalid encoding")
	}
	for _, input := range []string{"header login ^C\nUnknown command\n^C\n", "banner motd %Unknown command%\n"} {
		if TerminalUnsupported(input) {
			t.Fatal("banner interpreted")
		}
	}
	if TerminalUnsupported("SSID: Unknown command\n") || !TerminalUnsupported("SSID: valid\r% Unknown command\r") {
		t.Fatal("command error boundary")
	}
}
func TestCoverageAllReasonBranches(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	fresh := now.Format(time.RFC3339Nano)
	old := now.Add(-3601 * time.Second).Format(time.RFC3339Nano)
	future := now.Add(time.Second).Format(time.RFC3339Nano)
	cases := []struct {
		latest, projection Record
		state, reason      string
	}{{Record{}, nil, "unknown", "not_requested"}, {Record{"status": "failed", "observed_at": fresh}, nil, "unknown", "collection_failed"}, {Record{"status": "complete", "observed_at": "invalid"}, nil, "unknown", "observation_time_unknown"}, {Record{"status": "complete", "observed_at": future}, nil, "unknown", "observation_time_unknown"}, {Record{"status": "complete", "observed_at": old}, nil, "stale", "evidence_stale"}, {Record{"status": "complete", "observed_at": fresh}, Record{"status": "complete", "observed_at": old}, "unknown", "projection_time_unconfirmed"}, {Record{"status": "complete", "completed_at": fresh}, Record{"status": "blocked", "observed_at": fresh}, "unknown", "projection_incomplete"}, {Record{"status": "complete", "observed_at": fresh}, Record{"status": "complete", "observed_at": fresh}, "complete", ""}}
	for _, test := range cases {
		history := Record{"clients": Record{"latest": test.latest, "last_complete": Record{"observed_at": old}}}
		var projection Record
		if test.projection != nil {
			projection = Record{"clients": test.projection}
		}
		c, e := NewCoverage(CoverageOptions{Observations: history, Projection: projection, Dimensions: []Dimension{Clients}, Now: now, StaleAfter: 3600})
		if e != nil {
			t.Fatal(e)
		}
		entry := c.ToMap()["dimensions"].(Record)["clients"].(Record)
		if c.ToMap()["state"] != test.state || stringValue(entry["reason"]) != test.reason {
			t.Fatalf("got %v", entry)
		}
		if _, e = json.Marshal(c); e != nil {
			t.Fatal(e)
		}
	}
	untracked, _ := NewCoverage(CoverageOptions{Now: now, StaleAfter: 1})
	if untracked.ToMap()["state"] != "untracked" {
		t.Fatal("inventory")
	}
	unknown, _ := NewCoverage(CoverageOptions{Observations: Record{}, Now: now, StaleAfter: 1})
	if unknown.ToMap()["state"] != "unknown" {
		t.Fatal("empty scope")
	}
}
func TestParserConcurrentReuse(t *testing.T) {
	p, _ := NewRuntimeParser("")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, e := p.Parse(Clients, "Total clients: 1\nMAC address: 0200-0000-0001\nSSID: PRIVATE\n", ParseOptions{Complete: true})
			if e != nil || !o.Complete() {
				t.Error("concurrent parse")
			}
			rows := o.Records()
			rows[0]["ssid"] = "changed"
			if o.Records()[0]["ssid"] != "PRIVATE" {
				t.Error("concurrent ownership")
			}
		}()
	}
	wg.Wait()
}
func TestSourceLineAndObservationStates(t *testing.T) {
	source, _ := ParseConfigurationSource("wlan ap AP model MODEL\n future SECRET\nreturn\n", true)
	line := source.Lines()[1]
	if line.Index() != 1 || line.Number() != 2 || line.Indent() != 1 || line.Text() != "future SECRET" || line.Raw() != " future SECRET\n" {
		t.Fatal("source line")
	}
	if state, e := source.State(1); e != nil || state != "unsupported" {
		t.Fatal(state, e)
	}
	if _, e := source.State(-1); e == nil {
		t.Fatal("invalid source index")
	}
	if len(source.Issues()) != 0 {
		t.Fatal("document issues separate from parser issues")
	}
	o := source.Observation()
	if !o.Partial() || !o.Available() || o.CompletedAt() != nil {
		t.Fatal("observation states")
	}
	b, _ := json.Marshal(o)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("unknown configuration leaked")
	}
}

func TestCoverageFalseTimestampFallsBackLikeRuby(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	c, e := NewCoverage(CoverageOptions{Observations: Record{"clients": Record{"latest": Record{"status": "complete", "completed_at": false, "observed_at": now.Format(time.RFC3339Nano)}}}, Dimensions: []Dimension{Clients}, Now: now, StaleAfter: 1})
	if e != nil || c.ToMap()["state"] != "complete" {
		t.Fatal("Ruby false timestamp fallback", c.ToMap(), e)
	}
}

func TestProfileDimensionsPreserveDeclarationOrderAndOwnership(t *testing.T) {
	p, _ := NewProfile("h3c", "")
	want := []Dimension{ClientIPConflicts, APModels, AccessPoints, Radios, BSSes, Clients, Configuration, APGroups, ServiceTemplates, Dot1xConnections, MABConnections, BlockedClients, LLDP}
	got := p.Dimensions()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profile declaration order %v", got)
	}
	got[0] = Collection
	if !reflect.DeepEqual(p.Dimensions(), want) {
		t.Fatal("profile dimension ownership")
	}
	huawei, _ := NewProfile("huawei", "")
	if !reflect.DeepEqual(huawei.Dimensions(), []Dimension{AccessPoints, Clients}) {
		t.Fatal("Huawei declaration order")
	}
}
