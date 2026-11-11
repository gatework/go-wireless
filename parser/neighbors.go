package parser

import (
	"errors"
	"unicode/utf8"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"

	wireless "github.com/gatework/go-wireless"
)

// NeighborParser parses LLDP neighbor output read on an AP.
type NeighborParser struct{ profile *Profile }

// NewNeighborParser returns the neighbor parser of profile for an AP model.
func NewNeighborParser(profile *Profile, model string) (*NeighborParser, error) {
	if !profile.Valid() {
		return nil, errors.New("wireless parser: profile required")
	}
	if !utf8.ValidString(model) {
		return nil, errors.New("invalid device model")
	}
	owned := *profile
	return &NeighborParser{&owned}, nil
}

// Parse parses the LLDP output of the AP named apName; complete declares that
// the output was captured completely.
func (p *NeighborParser) Parse(content, apName string, complete bool) (wireless.Observation, error) {
	if p == nil || !p.profile.Valid() {
		return wireless.Observation{}, errors.New("wireless parser: profile required")
	}
	if parsetext.Strip(apName) == "" {
		return wireless.Observation{}, errors.New("missing AP name")
	}
	if _, ok := p.profile.Commands()[wireless.LLDP]; !ok {
		return Unavailable(wireless.LLDP, wireless.Unsupported, "unsupported_platform_dimension"), nil
	}
	o, err := p.profile.Parse(wireless.LLDP, content, complete)
	if err != nil {
		return wireless.Observation{}, err
	}
	records := o.Records()
	for _, r := range records {
		r["ap_name"] = apName
	}
	return wireless.NewObservation(wireless.ObservationOptions{Name: wireless.LLDP, Status: o.Status(), Records: records, Issues: o.Issues()})
}
func (p *NeighborParser) String() string { return "<wireless.NeighborParser>" }

// GoString formats metadata only.
func (p *NeighborParser) GoString() string { return p.String() }
