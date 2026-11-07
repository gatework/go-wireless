package wireless

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type Query struct {
	Command    string
	Dimensions []Dimension
}
type Profile struct{ platform, model string }

var h3cDeclarations = []Query{
	{"display wlan statistics client-ip-conflict", []Dimension{ClientIPConflicts}}, {"display wlan ap-model all", []Dimension{APModels}}, {"display wlan ap all verbose", []Dimension{AccessPoints, Radios}}, {"display wlan bss all verbose", []Dimension{BSSes}}, {"display wlan client verbose", []Dimension{Clients}}, {"display current-configuration", []Dimension{Configuration, APGroups, ServiceTemplates}}, {"display dot1x connection", []Dimension{Dot1xConnections}}, {"display mac-authentication connection", []Dimension{MABConnections}}, {"display wlan client-security block-mac", []Dimension{BlockedClients}}, {"display lldp neighbor-information verbose", []Dimension{LLDP}},
}
var huaweiDeclarations = []Query{{"display ap all", []Dimension{AccessPoints}}, {"display station all", []Dimension{Clients}}}

func SupportedPlatforms() []string { return []string{"h3c", "huawei", "h3c_wireless"} }
func SupportedDimensions() []Dimension {
	out := []Dimension{}
	seen := map[Dimension]bool{}
	for _, q := range h3cDeclarations {
		for _, n := range q.Dimensions {
			if !seen[n] {
				out = append(out, n)
				seen[n] = true
			}
		}
	}
	return out
}
func supportedDimension(n Dimension) bool {
	for _, d := range SupportedDimensions() {
		if n == d {
			return true
		}
	}
	return false
}
func NewProfile(platform, model string) (*Profile, error) {
	key := strings.ToLower(strip(platform))
	if key == "h3c_wireless" {
		key = "h3c"
	}
	if key != "h3c" && key != "huawei" {
		return nil, errors.New("unsupported parser platform")
	}
	if !utf8.ValidString(model) {
		return nil, errors.New("invalid device model")
	}
	return &Profile{key, strings.ToLower(strip(model))}, nil
}
func (p *Profile) Platform() string { return p.platform }
func (p *Profile) Model() string    { return p.model }
func (p *Profile) PreparationCommand() string {
	if p.platform == "huawei" {
		return "screen-length 0 temporary"
	}
	return "screen-length disable"
}
func (p *Profile) declarations() []Query {
	if p.platform == "huawei" {
		return huaweiDeclarations
	}
	return h3cDeclarations
}

// Dimensions returns the profile declaration order for default collection scopes.
// The result includes LLDP; a collector must exclude it unless explicitly requested.
func (p *Profile) Dimensions() []Dimension {
	dimensions := []Dimension{}
	seen := map[Dimension]bool{}
	for _, query := range p.declarations() {
		for _, name := range query.Dimensions {
			if !seen[name] {
				dimensions = append(dimensions, name)
				seen[name] = true
			}
		}
	}
	return dimensions
}
func (p *Profile) Commands() map[Dimension]string {
	m := map[Dimension]string{}
	for _, q := range p.declarations() {
		for _, n := range q.Dimensions {
			m[n] = q.Command
		}
	}
	return m
}
func (p *Profile) Command(n Dimension) (string, error) {
	s, ok := p.Commands()[n]
	if !ok {
		return "", errors.New("unsupported parser dimension")
	}
	return s, nil
}
func validateNames(names []Dimension, allowEmpty bool) error {
	if !allowEmpty && len(names) == 0 {
		return errors.New("dimensions must be nonempty unique names")
	}
	seen := map[Dimension]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			return errors.New("dimensions must be nonempty unique names")
		}
		seen[n] = true
	}
	return nil
}
func (p *Profile) Queries(names []Dimension) ([]Query, error) {
	if err := validateNames(names, true); err != nil {
		return nil, err
	}
	queries := []Query{}
	positions := map[string]int{}
	for _, n := range names {
		cmd, err := p.Command(n)
		if err != nil {
			return nil, err
		}
		pos, ok := positions[cmd]
		if !ok {
			pos = len(queries)
			queries = append(queries, Query{Command: cmd, Dimensions: []Dimension{}})
			positions[cmd] = pos
		}
		queries[pos].Dimensions = append(queries[pos].Dimensions, n)
	}
	return queries, nil
}
func (p *Profile) String() string   { return fmt.Sprintf("<wireless.Profile platform=%s>", p.platform) }
func (p *Profile) GoString() string { return p.String() }

func (p *Profile) Parse(name Dimension, content string, complete bool) (Observation, error) {
	m, e := p.ParseMany([]Dimension{name}, content, complete)
	return m[name], e
}
func (p *Profile) ParseMany(names []Dimension, content string, complete bool) (map[Dimension]Observation, error) {
	if err := validateNames(names, false); err != nil {
		return nil, err
	}
	queries, err := p.Queries(names)
	if err != nil {
		return nil, err
	}
	if len(queries) != 1 {
		return nil, errors.New("one CLI output must belong to one command")
	}
	cmd := queries[0].Command
	text, err := NormalizeText(content, cmd == "display current-configuration")
	var result parseResult
	var views map[Dimension]parseResult
	if err != nil {
		result = failedResult("invalid_output")
	} else if TerminalUnsupported(text) {
		result = parseResult{status: Unsupported, issues: []Record{{"code": "unsupported_command"}}}
	} else if cmd != "display current-configuration" && TerminalIssue(text) != nil {
		result = parseResult{status: Partial, issues: []Record{TerminalIssue(text)}}
	} else {
		state := newParseState()
		switch cmd {
		case "display wlan ap all verbose":
			views = state.parseAPs(text, complete)
		case "display wlan bss all verbose":
			result = state.parseRecords(BSSes, text, complete)
		case "display wlan client verbose":
			result = state.parseRecords(Clients, text, complete)
		case "display dot1x connection":
			result = state.parseRecords(Dot1xConnections, text, complete)
		case "display mac-authentication connection":
			result = state.parseRecords(MABConnections, text, complete)
		case "display wlan ap-model all":
			result = state.parseModels(text, complete)
		case "display wlan client-security block-mac":
			result = state.parseBlocked(text, complete)
		case "display wlan statistics client-ip-conflict":
			result = state.parseConflicts(text, complete)
		case "display lldp neighbor-information verbose":
			result = state.parseNeighbors(text, complete)
		case "display current-configuration":
			result = parseConfiguration(text, complete)
		case "display ap all":
			result = parseHuawei(AccessPoints, text, complete)
		case "display station all":
			result = parseHuawei(Clients, text, complete)
		}
	}
	out := map[Dimension]Observation{}
	for _, n := range names {
		r := result
		if views != nil {
			r = views[n]
		}
		if (n == APGroups || n == ServiceTemplates) && len(r.records) > 0 {
			r.records = recordList(r.records[0][string(n)])
		}
		out[n] = resultObservation(n, r)
	}
	return out, nil
}
func unavailable(n Dimension, s Status, code string) Observation {
	o, _ := NewObservation(ObservationOptions{Name: n, Status: s, Issues: []Record{{"code": code}}})
	return o
}
func resultObservation(n Dimension, r parseResult) Observation {
	o, e := NewObservation(ObservationOptions{Name: n, Status: r.status, Records: r.records, Issues: r.issues})
	if e != nil {
		return unavailable(n, Failed, "invalid_parser_result")
	}
	return o
}

func recordList(v any) []Record {
	if a, ok := v.([]Record); ok {
		return a
	}
	if a, ok := v.([]any); ok {
		rows := []Record{}
		for _, r := range a {
			if m, ok := r.(Record); ok {
				rows = append(rows, m)
			}
		}
		return rows
	}
	return []Record{}
}
