package parser

import (
	"errors"
	"maps"
	"unicode/utf8"

	wireless "github.com/gatework/go-wireless"
)

// ParseOptions selects the platform and declares complete capture.
type ParseOptions struct {
	Platform string
	Complete bool
}

// RuntimeParser parses controller command output for any registered platform.
type RuntimeParser struct {
	registry *Registry
	model    string
}

// NewRuntimeParser returns a parser over registry for a device model.
func NewRuntimeParser(registry *Registry, model string) (*RuntimeParser, error) {
	if registry == nil {
		return nil, errors.New("wireless parser: registry required")
	}
	if !utf8.ValidString(model) {
		return nil, errors.New("invalid device model")
	}
	return &RuntimeParser{registry: registry, model: model}, nil
}

// Parse parses content as the output for one dimension.
func (p *RuntimeParser) Parse(name wireless.Dimension, content string, o ParseOptions) (wireless.Observation, error) {
	out, err := p.ParseMany([]wireless.Dimension{name}, content, o)
	return out[name], err
}

// ParseMany parses content as the output of one query that yields several
// dimensions.
func (p *RuntimeParser) ParseMany(names []wireless.Dimension, content string, o ParseOptions) (map[wireless.Dimension]wireless.Observation, error) {
	if p == nil || p.registry == nil {
		return nil, errors.New("wireless parser: registry required")
	}
	if err := ValidateNames(names, false); err != nil {
		return nil, err
	}
	platform := o.Platform
	if platform == "" {
		platform = "h3c"
	}
	profile, err := p.registry.Fetch(platform, p.model)
	if err != nil {
		return nil, err
	}
	out := map[wireless.Dimension]wireless.Observation{}
	if _, err := NormalizeText(content, true); err != nil {
		for _, n := range names {
			out[n] = Unavailable(n, wireless.Failed, "invalid_output")
		}
		return out, nil
	}
	if TerminalUnsupported(content) {
		for _, n := range names {
			out[n] = Unavailable(n, wireless.Unsupported, "unsupported_command")
		}
		return out, nil
	}
	supported := []wireless.Dimension{}
	for _, n := range names {
		if _, err := profile.Command(n); err == nil {
			supported = append(supported, n)
		} else {
			out[n] = Unavailable(n, wireless.Unsupported, "unsupported_platform_dimension")
		}
	}
	if len(supported) == 0 {
		return out, nil
	}
	parsed, err := profile.ParseMany(supported, content, o.Complete)
	if err != nil {
		return nil, err
	}
	maps.Copy(out, parsed)
	return out, nil
}
func (p *RuntimeParser) String() string { return "<wireless.RuntimeParser>" }

// GoString formats metadata only.
func (p *RuntimeParser) GoString() string { return p.String() }
