package wireless

import "errors"

type NeighborParser struct{ profile *Profile }

func NewNeighborParser(platform, model string) (*NeighborParser, error) {
	if platform == "" {
		platform = "h3c"
	}
	p, e := NewProfile(platform, model)
	if e != nil {
		return nil, e
	}
	return &NeighborParser{p}, nil
}
func (p *NeighborParser) Parse(content, apName string, complete bool) (Observation, error) {
	if strip(apName) == "" {
		return Observation{}, errors.New("missing AP name")
	}
	if _, ok := p.profile.Commands()[LLDP]; !ok {
		return unavailable(LLDP, Unsupported, "unsupported_platform_dimension"), nil
	}
	o, err := p.profile.Parse(LLDP, content, complete)
	if err != nil {
		return Observation{}, err
	}
	records := o.Records()
	for _, r := range records {
		r["ap_name"] = apName
	}
	return NewObservation(ObservationOptions{Name: LLDP, Status: o.Status(), Records: records, Issues: o.Issues()})
}
func (p *NeighborParser) String() string   { return "<wireless.NeighborParser>" }
func (p *NeighborParser) GoString() string { return p.String() }
