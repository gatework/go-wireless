package wireless_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gatework/go-wireless/internal/record"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
	h3c "github.com/gatework/go-wireless/vendors/h3c"
)

func TestProfileQueriesAliasesAndCopies(t *testing.T) {
	p, e := vendors.NewBuiltinRegistry().Fetch(" H3C_WIRELESS ", " MODEL-A ")
	if e != nil {
		t.Fatal(e)
	}
	if p.Platform() != "h3c" || p.Model() != "model-a" || p.PreparationCommand() != "screen-length disable" {
		t.Fatal("profile identity")
	}
	hw, _ := vendors.NewBuiltinRegistry().Fetch("huawei", "")
	if hw.PreparationCommand() != "screen-length 0 temporary" {
		t.Fatal("Huawei preparation")
	}
	queries, e := p.Queries([]wireless.Dimension{wireless.Clients, wireless.Radios, wireless.AccessPoints, wireless.APGroups, wireless.ServiceTemplates})
	if e != nil {
		t.Fatal(e)
	}
	want := []parser.Query{{Command: "display wlan client verbose", Dimensions: []wireless.Dimension{wireless.Clients}}, {Command: "display wlan ap all verbose", Dimensions: []wireless.Dimension{wireless.Radios, wireless.AccessPoints}}, {Command: "display current-configuration", Dimensions: []wireless.Dimension{wireless.APGroups, wireless.ServiceTemplates}}}
	if !reflect.DeepEqual(queries, want) {
		t.Fatal(queries)
	}
	queries[1].Dimensions[0] = wireless.Clients
	commands := p.Commands()
	commands[wireless.Clients] = "evil"
	fresh, _ := p.Queries([]wireless.Dimension{wireless.Radios})
	if fresh[0].Dimensions[0] != wireless.Radios || p.Commands()[wireless.Clients] != "display wlan client verbose" {
		t.Fatal("registry ownership")
	}
	if len(vendors.NewBuiltinRegistry().Platforms()) != 3 || len(vendors.NewBuiltinRegistry().Dimensions()) != 13 {
		t.Fatal("registry dimensions")
	}
	for _, input := range []string{"", "unknown", "h3c\nPRIVATE"} {
		if _, e := vendors.NewBuiltinRegistry().Fetch(input, ""); e == nil {
			t.Fatal("invalid platform")
		}
	}
	if _, e := p.Queries([]wireless.Dimension{wireless.Clients, wireless.Clients}); e == nil {
		t.Fatal("duplicate query")
	}
	if _, e := p.Queries([]wireless.Dimension{wireless.Collection}); e == nil {
		t.Fatal("unsupported query")
	}
}

func TestCollectionScopeOwnsDefaultsDependenciesAndAPSelection(t *testing.T) {
	profile, err := vendors.NewBuiltinRegistry().Fetch("h3c_wireless", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ConnectorPlatform() != "h3c_wireless" {
		t.Fatal("connector alias")
	}
	defaults, err := profile.CollectionScope(nil)
	if err != nil || defaults.APNeighbors() {
		t.Fatal("default AP access", err)
	}
	names := []wireless.Dimension{wireless.LLDP, wireless.APGroups, wireless.ServiceTemplates}
	scope, err := profile.CollectionScope(names)
	if err != nil || !scope.APNeighbors() || !reflect.DeepEqual(scope.Dimensions(), []wireless.Dimension{wireless.LLDP, wireless.APGroups, wireless.ServiceTemplates, wireless.AccessPoints, wireless.Configuration}) {
		t.Fatal(scope, err)
	}
	names[0] = wireless.Clients
	scope.Dimensions()[0] = wireless.Clients
	queries := scope.Queries()
	queries[0].Dimensions[0] = wireless.Clients
	if scope.Dimensions()[0] != wireless.LLDP || scope.Queries()[0].Dimensions[0] != wireless.APGroups {
		t.Fatal("scope aliases input or accessor")
	}
	if _, err := profile.CollectionScope([]wireless.Dimension{}); err == nil {
		t.Fatal("empty scope accepted")
	}
	huawei, _ := vendors.NewBuiltinRegistry().Fetch("huawei", "")
	unsupported, err := huawei.CollectionScope([]wireless.Dimension{wireless.LLDP})
	if err != nil || unsupported.APNeighbors() || !reflect.DeepEqual(unsupported.Unsupported(), []wireless.Dimension{wireless.LLDP}) || len(unsupported.Queries()) != 1 {
		t.Fatal("unsupported dimension erased prerequisite", err)
	}
}
func TestConfigViewsAndNeighborAPI(t *testing.T) {
	runtime, _ := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
	out, e := runtime.ParseMany([]wireless.Dimension{wireless.Configuration, wireless.APGroups, wireless.ServiceTemplates}, "wlan ap-group LAB\n ap AP\n#\nwlan service-template EAP\n ssid Corporate\nreturn\n", parser.ParseOptions{Complete: true})
	if e != nil {
		t.Fatal(e)
	}
	if !out[wireless.APGroups].Complete() || len(out[wireless.APGroups].Records()) != 1 || out[wireless.APGroups].Records()[0]["name"] != "LAB" || out[wireless.ServiceTemplates].Records()[0]["ssid"] != "Corporate" {
		t.Fatal("configuration projection")
	}
	neighbor, _ := parser.NewNeighborParser(selectedProfile("h3c"), "")
	o, e := neighbor.Parse("LLDP neighbor-information of port 1[GE1]:\nLLDP neighbor index: 1\nChassis ID: switch\nPort ID: GE2\n", "AP-1", true)
	if e != nil || !o.Complete() || o.Records()[0]["ap_name"] != "AP-1" {
		t.Fatal("neighbor association", o.Issues(), e)
	}
	for _, name := range []string{"", " \t"} {
		if _, e := neighbor.Parse("", name, true); e == nil {
			t.Fatal("invalid AP name")
		}
	}
	unsupported, _ := parser.NewNeighborParser(selectedProfile("huawei"), "")
	o, e = unsupported.Parse("", "AP", true)
	if e != nil || o.Status() != wireless.Unsupported {
		t.Fatal("unsupported LLDP")
	}
	config, _ := vendors.NewBuiltinRegistry().Fetch("huawei", "")
	if _, e := config.Parse(wireless.Configuration, "", true); e == nil {
		t.Fatal("unsupported configuration")
	}
	o, e = runtime.Parse(wireless.Clients, string([]byte{0xff}), parser.ParseOptions{Complete: true})
	if e != nil || o.Status() != wireless.Failed {
		t.Fatal("invalid encoding")
	}
	for _, input := range []string{"header login ^C\nUnknown command\n^C\n", "banner motd %Unknown command%\n"} {
		if parser.TerminalUnsupported(input) {
			t.Fatal("banner interpreted")
		}
	}
	if parser.TerminalUnsupported("SSID: Unknown command\n") || !parser.TerminalUnsupported("SSID: valid\r% Unknown command\r") {
		t.Fatal("command error boundary")
	}
}
func TestCoverageAllReasonBranches(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	fresh := now.Format(time.RFC3339Nano)
	old := now.Add(-3601 * time.Second).Format(time.RFC3339Nano)
	future := now.Add(time.Second).Format(time.RFC3339Nano)
	cases := []struct {
		latest, projection wireless.Record
		state, reason      string
	}{{wireless.Record{}, nil, "unknown", "not_requested"}, {wireless.Record{"status": "failed", "observed_at": fresh}, nil, "unknown", "collection_failed"}, {wireless.Record{"status": "complete", "observed_at": "invalid"}, nil, "unknown", "observation_time_unknown"}, {wireless.Record{"status": "complete", "observed_at": future}, nil, "unknown", "observation_time_unknown"}, {wireless.Record{"status": "complete", "observed_at": old}, nil, "stale", "evidence_stale"}, {wireless.Record{"status": "complete", "observed_at": fresh}, wireless.Record{"status": "complete", "observed_at": old}, "unknown", "projection_time_unconfirmed"}, {wireless.Record{"status": "complete", "completed_at": fresh}, wireless.Record{"status": "blocked", "observed_at": fresh}, "unknown", "projection_incomplete"}, {wireless.Record{"status": "complete", "observed_at": fresh}, wireless.Record{"status": "complete", "observed_at": fresh}, "complete", ""}}
	for _, test := range cases {
		history := wireless.Record{"clients": wireless.Record{"latest": test.latest, "last_complete": wireless.Record{"observed_at": old}}}
		var projection wireless.Record
		if test.projection != nil {
			projection = wireless.Record{"clients": test.projection}
		}
		c, e := wireless.NewCoverage(wireless.CoverageOptions{Observations: history, Projection: projection, Dimensions: []wireless.Dimension{wireless.Clients}, Now: now, StaleAfter: 3600})
		if e != nil {
			t.Fatal(e)
		}
		entry := c.ToMap()["dimensions"].(wireless.Record)["clients"].(wireless.Record)
		if c.ToMap()["state"] != test.state || record.StringValue(entry["reason"]) != test.reason {
			t.Fatalf("got %v", entry)
		}
		if _, e = json.Marshal(c); e != nil {
			t.Fatal(e)
		}
	}
	untracked, _ := wireless.NewCoverage(wireless.CoverageOptions{Now: now, StaleAfter: 1})
	if untracked.ToMap()["state"] != "untracked" {
		t.Fatal("inventory")
	}
	unknown, _ := wireless.NewCoverage(wireless.CoverageOptions{Observations: wireless.Record{}, Now: now, StaleAfter: 1})
	if unknown.ToMap()["state"] != "unknown" {
		t.Fatal("empty scope")
	}
}
func TestParserConcurrentReuse(t *testing.T) {
	p, _ := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, e := p.Parse(wireless.Clients, "Total clients: 1\nMAC address: 0200-0000-0001\nSSID: PRIVATE\n", parser.ParseOptions{Complete: true})
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
	source, _ := h3c.ParseConfigurationSource("wlan ap AP model MODEL\n future SECRET\nreturn\n", true)
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
	c, e := wireless.NewCoverage(wireless.CoverageOptions{Observations: wireless.Record{"clients": wireless.Record{"latest": wireless.Record{"status": "complete", "completed_at": false, "observed_at": now.Format(time.RFC3339Nano)}}}, Dimensions: []wireless.Dimension{wireless.Clients}, Now: now, StaleAfter: 1})
	if e != nil || c.ToMap()["state"] != "complete" {
		t.Fatal("Ruby false timestamp fallback", c.ToMap(), e)
	}
}

func TestProfileDimensionsPreserveDeclarationOrderAndOwnership(t *testing.T) {
	p, _ := vendors.NewBuiltinRegistry().Fetch("h3c", "")
	want := []wireless.Dimension{wireless.ClientIPConflicts, wireless.APModels, wireless.AccessPoints, wireless.Radios, wireless.BSSes, wireless.Clients, wireless.Configuration, wireless.APGroups, wireless.ServiceTemplates, wireless.Dot1xConnections, wireless.MABConnections, wireless.BlockedClients, wireless.LLDP}
	got := p.Dimensions()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profile declaration order %v", got)
	}
	got[0] = wireless.Collection
	if !reflect.DeepEqual(p.Dimensions(), want) {
		t.Fatal("profile dimension ownership")
	}
	huawei, _ := vendors.NewBuiltinRegistry().Fetch("huawei", "")
	if !reflect.DeepEqual(huawei.Dimensions(), []wireless.Dimension{wireless.AccessPoints, wireless.Clients}) {
		t.Fatal("Huawei declaration order")
	}
}

func TestAPQueryOwnsDeclarationAndRequiresSelectedCapability(t *testing.T) {
	spec := h3c.Profile()
	spec.APPreparation = "prepare selected ap"
	registry, err := parser.NewRegistry(spec)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := registry.Fetch("h3c", "")
	alias, _ := registry.Fetch("h3c_wireless", "another-model")
	query := profile.APQuery()
	spec.APPreparation = "changed preparation"
	for i := range spec.Queries {
		spec.Queries[i].Command = "changed query"
	}
	if !query.Valid() || query.Command() != "display lldp neighbor-information verbose" || query.PreparationCommand() != "prepare selected ap" || !alias.MatchesAPQuery(query) {
		t.Fatal("query lost its registered declaration", query)
	}
	if profile.MatchesAPQuery(parser.APQuery{}) || selectedProfile("h3c").MatchesAPQuery(query) || selectedProfile("huawei").APQuery().Valid() {
		t.Fatal("query authorized an unrelated capability")
	}
	for _, profile := range []*parser.Profile{nil, {}} {
		if profile.APQuery().Valid() || profile.MatchesAPQuery(query) {
			t.Fatal("invalid profile authorized AP work")
		}
	}
}

func TestAPProfileRequiresPreparationAndNeighborDeclaration(t *testing.T) {
	for _, mutate := range []func(*parser.ProfileSpec){
		func(s *parser.ProfileSpec) { s.APPreparation = "" },
		func(s *parser.ProfileSpec) { s.APPreparation = "prepare\ninvalid" },
		func(s *parser.ProfileSpec) { s.APNeighbors = false },
		func(s *parser.ProfileSpec) { s.Queries = s.Queries[:len(s.Queries)-1] },
		func(s *parser.ProfileSpec) {
			s.Queries[len(s.Queries)-1].Dimensions = append(s.Queries[len(s.Queries)-1].Dimensions, wireless.Collection)
		},
	} {
		spec := h3c.Profile()
		mutate(&spec)
		if _, err := parser.NewRegistry(spec); err == nil {
			t.Fatal("invalid AP declaration registered")
		}
	}
}
