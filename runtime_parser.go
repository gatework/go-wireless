package wireless

import (
	"errors"
	"unicode/utf8"
)

type ParseOptions struct {
	Platform string
	Complete bool
}
type RuntimeParser struct{ model string }

func NewRuntimeParser(model string) (*RuntimeParser, error) {
	if !utf8.ValidString(model) {
		return nil, errors.New("invalid device model")
	}
	return &RuntimeParser{model}, nil
}
func (p *RuntimeParser) Parse(name Dimension, content string, o ParseOptions) (Observation, error) {
	out, err := p.ParseMany([]Dimension{name}, content, o)
	return out[name], err
}
func (p *RuntimeParser) ParseMany(names []Dimension, content string, o ParseOptions) (map[Dimension]Observation, error) {
	if err := validateNames(names, false); err != nil {
		return nil, err
	}
	platform := o.Platform
	if platform == "" {
		platform = "h3c"
	}
	profile, err := NewProfile(platform, p.model)
	if err != nil {
		return nil, err
	}
	out := map[Dimension]Observation{}
	if _, err := NormalizeText(content, true); err != nil {
		for _, n := range names {
			out[n] = unavailable(n, Failed, "invalid_output")
		}
		return out, nil
	}
	if TerminalUnsupported(content) {
		for _, n := range names {
			out[n] = unavailable(n, Unsupported, "unsupported_command")
		}
		return out, nil
	}
	supported := []Dimension{}
	for _, n := range names {
		if _, ok := profile.Commands()[n]; ok {
			supported = append(supported, n)
		} else {
			out[n] = unavailable(n, Unsupported, "unsupported_platform_dimension")
		}
	}
	if len(supported) == 0 {
		return out, nil
	}
	parsed, err := profile.ParseMany(supported, content, o.Complete)
	if err != nil {
		return nil, err
	}
	for n, v := range parsed {
		out[n] = v
	}
	return out, nil
}
func (p *RuntimeParser) String() string   { return "<wireless.RuntimeParser>" }
func (p *RuntimeParser) GoString() string { return p.String() }
