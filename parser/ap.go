package parser

import wireless "github.com/gatework/go-wireless"

// APQuery binds one AP neighbor query to its validated profile. The zero value
// cannot authorize device I/O; command and preparation declarations are immutable.
type APQuery struct {
	capability *profileCapability
	command    string
}

// Valid reports whether the query was selected from a validated profile.
func (q APQuery) Valid() bool { return q.capability != nil }

// Command returns the LLDP query command run on the AP.
func (q APQuery) Command() string { return q.command }

// PreparationCommand returns the command that prepares the AP session (for
// example disabling paging).
func (q APQuery) PreparationCommand() string {
	if !q.Valid() {
		return ""
	}
	return q.capability.apPreparation
}
func (APQuery) String() string { return "wireless.APQuery" }

// GoString formats metadata only.
func (q APQuery) GoString() string { return q.String() }

// APQuery returns the declared AP neighbor query, or zero for an unsupported profile.
func (p *Profile) APQuery() APQuery {
	if !p.Valid() || !p.capability.apNeighbors {
		return APQuery{}
	}
	command, _ := p.Command(wireless.LLDP) // Registration requires this declaration.
	return APQuery{capability: p.capability, command: command}
}

// MatchesAPQuery verifies that a query belongs to this selected profile.
func (p *Profile) MatchesAPQuery(query APQuery) bool {
	return p.Valid() && query.Valid() && p.capability == query.capability
}
