// Package parser owns validated query declarations and command-output parsing.
// Vendor implementations supply specifications; this package does not discover vendors.
package parser

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"

	wireless "github.com/gatework/go-wireless"
)

// Query groups all evidence dimensions produced by one command.
type Query struct {
	Command    string
	Dimensions []wireless.Dimension
}

// ParseFunc interprets one response into the views declared for its command.
// Each call owns its state; Profile validates and detaches returned observations.
type ParseFunc func(string, bool) map[wireless.Dimension]Result

// QuerySpec binds command declarations to a response parser and newline policy.
type QuerySpec struct {
	Query
	Parse            ParseFunc
	PreserveNewlines bool
}

// ProfileSpec declares a platform capability. The registry copies its slices;
// callbacks must own their captured state and support concurrent calls.
type ProfileSpec struct {
	Platform, ConnectorPlatform, Preparation string
	Aliases                                  []string
	Queries                                  []QuerySpec
	APNeighbors                              bool
	APPreparation                            string
}

type profileCapability struct {
	platform, connector, preparation string
	aliases                          []string
	queries                          []Query
	parsers                          map[string]QuerySpec
	apNeighbors                      bool
	apPreparation                    string
}

// Registry is an immutable validated catalog with no implicit vendor discovery.
type Registry struct {
	entries map[string]*profileCapability
	ordered []*profileCapability
}

// Profile binds one platform/model to an immutable query capability. Fetch
// constructs valid profiles; the zero value cannot parse or admit collection.
type Profile struct {
	platform, model string
	capability      *profileCapability
}

// NewRegistry validates and owns a catalog. No global registration is required.
func NewRegistry(specs ...ProfileSpec) (*Registry, error) {
	r := &Registry{entries: map[string]*profileCapability{}}
	for _, spec := range specs {
		capability, err := newCapability(spec)
		if err != nil {
			return nil, err
		}
		for _, name := range append([]string{capability.platform}, capability.aliases...) {
			name = strings.ToLower(parsetext.Strip(name))
			if name == "" || strings.ContainsFunc(name, unicode.IsControl) || r.entries[name] != nil {
				return nil, errors.New("wireless parser: duplicate or invalid platform declaration")
			}
			r.entries[name] = capability
		}
		r.ordered = append(r.ordered, capability)
	}
	return r, nil
}

// newCapability validates one profile declaration and its queries.
func newCapability(spec ProfileSpec) (*profileCapability, error) {
	key := strings.ToLower(parsetext.Strip(spec.Platform))
	if key == "" || spec.ConnectorPlatform == "" || !wireless.ValidCommand(spec.Preparation) || len(spec.Queries) == 0 {
		return nil, errors.New("wireless parser: invalid profile declaration")
	}
	if spec.APNeighbors && !wireless.ValidCommand(spec.APPreparation) || !spec.APNeighbors && spec.APPreparation != "" {
		return nil, errors.New("wireless parser: invalid AP preparation declaration")
	}
	capability := &profileCapability{platform: key, connector: spec.ConnectorPlatform, preparation: spec.Preparation, aliases: slices.Clone(spec.Aliases), parsers: map[string]QuerySpec{}, apNeighbors: spec.APNeighbors, apPreparation: spec.APPreparation}
	dimensions := map[wireless.Dimension]bool{}
	for _, query := range spec.Queries {
		if err := capability.addQuery(query, dimensions); err != nil {
			return nil, err
		}
	}
	if spec.APNeighbors && !dimensions[wireless.LLDP] {
		return nil, errors.New("wireless parser: AP neighbors require a query declaration")
	}
	return capability, nil
}

// addQuery validates and adds one query; dimensions holds the dimensions
// earlier queries already declared.
func (c *profileCapability) addQuery(query QuerySpec, dimensions map[wireless.Dimension]bool) error {
	if !wireless.ValidCommand(query.Command) || query.Parse == nil || len(query.Dimensions) == 0 {
		return errors.New("wireless parser: invalid query declaration")
	}
	if slices.Contains(query.Dimensions, wireless.LLDP) && len(query.Dimensions) != 1 {
		return errors.New("wireless parser: AP query cannot share controller dimensions")
	}
	if _, exists := c.parsers[query.Command]; exists {
		return errors.New("wireless parser: duplicate command declaration")
	}
	for _, dimension := range query.Dimensions {
		if !dimension.Valid() || dimensions[dimension] {
			return errors.New("wireless parser: invalid or duplicate dimension declaration")
		}
		dimensions[dimension] = true
	}
	query.Dimensions = slices.Clone(query.Dimensions)
	c.queries = append(c.queries, Query{Command: query.Command, Dimensions: slices.Clone(query.Dimensions)})
	c.parsers[query.Command] = query
	return nil
}

func (r *Registry) Platforms() []string {
	out := []string{}
	for _, capability := range r.ordered {
		out = append(out, capability.platform)
	}
	for _, capability := range r.ordered {
		out = append(out, capability.aliases...)
	}
	return out
}
func (r *Registry) Dimensions() []wireless.Dimension {
	out := []wireless.Dimension{}
	seen := map[wireless.Dimension]bool{}
	for _, capability := range r.ordered {
		for _, query := range capability.queries {
			for _, name := range query.Dimensions {
				if !seen[name] {
					out = append(out, name)
					seen[name] = true
				}
			}
		}
	}
	return out
}
func (r *Registry) Fetch(platform, model string) (*Profile, error) {
	if !utf8.ValidString(model) {
		return nil, errors.New("invalid device model")
	}
	if r == nil {
		return nil, errors.New("wireless parser: registry required")
	}
	capability := r.entries[strings.ToLower(parsetext.Strip(platform))]
	if capability == nil {
		return nil, errors.New("unsupported parser platform")
	}
	return &Profile{platform: capability.platform, model: strings.ToLower(parsetext.Strip(model)), capability: capability}, nil
}

// Valid reports whether the profile was selected from a validated registry.
func (p *Profile) Valid() bool { return p != nil && p.capability != nil }
func (p *Profile) Platform() string {
	if p == nil {
		return ""
	}
	return p.platform
}
func (p *Profile) Model() string {
	if p == nil {
		return ""
	}
	return p.model
}
func (p *Profile) ConnectorPlatform() string {
	if !p.Valid() {
		return ""
	}
	return p.capability.connector
}
func (p *Profile) PreparationCommand() string {
	if !p.Valid() {
		return ""
	}
	return p.capability.preparation
}
func (p *Profile) Dimensions() []wireless.Dimension {
	out := []wireless.Dimension{}
	for _, q := range p.declarations() {
		out = append(out, q.Dimensions...)
	}
	return out
}
func (p *Profile) Commands() map[wireless.Dimension]string {
	out := map[wireless.Dimension]string{}
	for _, q := range p.declarations() {
		for _, n := range q.Dimensions {
			out[n] = q.Command
		}
	}
	return out
}
func (p *Profile) Command(n wireless.Dimension) (string, error) {
	for _, q := range p.declarations() {
		if slices.Contains(q.Dimensions, n) {
			return q.Command, nil
		}
	}
	return "", errors.New("unsupported parser dimension")
}
func (p *Profile) Queries(names []wireless.Dimension) ([]Query, error) {
	if !p.Valid() {
		return nil, errors.New("wireless parser: profile required")
	}
	if err := ValidateNames(names, true); err != nil {
		return nil, err
	}
	queries := []Query{}
	positions := map[string]int{}
	for _, n := range names {
		cmd, err := p.Command(n)
		if err != nil {
			return nil, err
		}
		pos, exists := positions[cmd]
		if !exists {
			pos = len(queries)
			positions[cmd] = pos
			queries = append(queries, Query{Command: cmd, Dimensions: []wireless.Dimension{}})
		}
		queries[pos].Dimensions = append(queries[pos].Dimensions, n)
	}
	return queries, nil
}
func ValidateNames(names []wireless.Dimension, allowEmpty bool) error {
	if !allowEmpty && len(names) == 0 {
		return errors.New("dimensions must be nonempty unique names")
	}
	seen := map[wireless.Dimension]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			return errors.New("dimensions must be nonempty unique names")
		}
		seen[n] = true
	}
	return nil
}
func (p *Profile) String() string   { return fmt.Sprintf("<wireless.Profile platform=%s>", p.platform) }
func (p *Profile) GoString() string { return p.String() }

// Parse validates and owns one requested view from exactly one command output.
func (p *Profile) Parse(name wireless.Dimension, content string, complete bool) (wireless.Observation, error) {
	out, err := p.ParseMany([]wireless.Dimension{name}, content, complete)
	return out[name], err
}

func (p *Profile) ParseMany(names []wireless.Dimension, content string, complete bool) (map[wireless.Dimension]wireless.Observation, error) {
	if p == nil || p.capability == nil {
		return nil, errors.New("wireless parser: profile required")
	}
	queries, err := p.Queries(names)
	if err != nil {
		return nil, err
	}
	if len(queries) != 1 {
		return nil, errors.New("one CLI output must belong to one command")
	}
	spec := p.capability.parsers[queries[0].Command]
	text, err := NormalizeText(content, spec.PreserveNewlines)
	var result Result
	var views map[wireless.Dimension]Result
	if err != nil {
		result = FailedResult("invalid_output")
	} else if TerminalUnsupported(text) {
		result = Result{Status: wireless.Unsupported, Issues: []wireless.Record{{"code": "unsupported_command"}}}
	} else if !spec.PreserveNewlines && TerminalIssue(text) != nil {
		result = Result{Status: wireless.Partial, Issues: []wireless.Record{TerminalIssue(text)}}
	} else {
		views = spec.Parse(text, complete)
	}
	out := map[wireless.Dimension]wireless.Observation{}
	for _, name := range names {
		r := result
		if views != nil {
			r = views[name]
		}
		out[name] = ResultObservation(name, r)
	}
	return out, nil
}

// MatchesConnector checks the explicit parser/transport pairing, including declared aliases.
func (p *Profile) MatchesConnector(platform string) bool {
	if p == nil || p.capability == nil {
		return false
	}
	key := strings.ToLower(parsetext.Strip(platform))
	return key == p.capability.connector || key == p.capability.platform || slices.Contains(p.capability.aliases, key)
}
func Unavailable(name wireless.Dimension, status wireless.Status, code string) wireless.Observation {
	o, _ := wireless.NewObservation(wireless.ObservationOptions{Name: name, Status: status, Issues: []wireless.Record{{"code": code}}})
	return o
}
func ResultObservation(name wireless.Dimension, result Result) wireless.Observation {
	o, err := wireless.NewObservation(wireless.ObservationOptions{Name: name, Status: result.Status, Records: result.Records, Issues: result.Issues})
	if err != nil {
		return Unavailable(name, wireless.Failed, "invalid_parser_result")
	}
	return o
}

func (p *Profile) declarations() []Query {
	if !p.Valid() {
		return nil
	}
	return p.capability.queries
}
