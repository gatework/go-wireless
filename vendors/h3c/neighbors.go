package h3c

import (
	"math/big"
	"regexp"
	"strings"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/parser"
)

var neighborPort = regexp.MustCompile(`(?i)^LLDP neighbor-information of port \d+\[(.+?)\]\s*:?$`)
var neighborIndex = regexp.MustCompile(`(?i)^LLDP neighbor index\s*:`)
var neighborEmpty = regexp.MustCompile(`(?i)^No (?:LLDP )?neighbors\s*$`)
var neighborSkip = regexp.MustCompile(`(?i)^(?:LLDP agent\s+[^:]+:|(?:Total|Number of neighbors).*:\s*\d+)\s*$`)
var neighborField = regexp.MustCompile(`^([^:]+):\s*(.*?)$`)

func (p *parseState) parseNeighbors(text string, complete bool) parser.Result {
	n := &neighborScan{state: p, records: []wireless.Record{}, issues: []wireless.Record{}, ports: []string{}, seenPorts: map[string]bool{},
		emptyPorts: map[any]int{}, descriptionIndent: -1, continuations: map[int]bool{}}
	for index, line := range parser.SourceLines(text) {
		n.line(index, line)
	}
	n.storeDescription()
	n.flush()
	n.portIssues()
	total := n.total(text)
	return parser.FieldsResult(n.records, n.issues, complete, total, []string{"local_interface", "remote_chassis_id", "remote_interface"})
}

// neighborScan is the state of one LLDP neighbor parse.
type neighborScan struct {
	state             *parseState
	records, issues   []wireless.Record
	current           wireless.Record
	local             any // the port of the current neighbor section
	ports             []string
	seenPorts         map[string]bool
	emptyPorts        map[any]int // port -> line of its "No neighbors" row
	emptyOrder        []any
	descriptionIndent int // indent of an open system description, or -1
	description       string
	descriptionLine   int
	continuations     map[int]bool // description continuation lines
}

func (n *neighborScan) assign(record wireless.Record, key string, value any, line int) {
	n.state.assign(record, key, value, &n.issues, line)
}

func (n *neighborScan) issue(code string, line int) {
	n.issues = append(n.issues, wireless.Record{"code": code, "line": line})
}

// line consumes one output line (index is 0-based).
func (n *neighborScan) line(index int, line string) {
	row := parsetext.Strip(line)
	if row == "" || separatorLine.MatchString(row) {
		return
	}
	indent := len(line) - len(strings.TrimLeft(line, " \t\v\f"))
	if n.descriptionIndent >= 0 && indent > n.descriptionIndent {
		n.description += "\n" + row
		n.continuations[index+1] = true
		return
	}
	n.storeDescription()
	if m := neighborPort.FindStringSubmatch(row); m != nil {
		n.flush()
		n.local = m[1]
		if !n.seenPorts[m[1]] {
			n.ports = append(n.ports, m[1])
			n.seenPorts[m[1]] = true
		}
	} else if neighborIndex.MatchString(row) {
		n.flush()
		n.current = wireless.Record{"local_interface": n.local}
	} else if neighborEmpty.MatchString(row) {
		if _, ok := n.emptyPorts[n.local]; !ok {
			n.emptyPorts[n.local] = index + 1
			n.emptyOrder = append(n.emptyOrder, n.local)
		}
	} else if neighborSkip.MatchString(row) {
		return
	} else if m := neighborField.FindStringSubmatch(row); m != nil {
		n.field(index, indent, strings.ToLower(parsetext.Strip(m[1])), m[2])
	} else {
		n.issue("unrecognized_line", index+1)
	}
}

// field records one "label: value" row of the current neighbor.
func (n *neighborScan) field(index, indent int, label, raw string) {
	if n.current == nil {
		n.current = wireless.Record{"local_interface": n.local}
	}
	switch label {
	case "chassis id":
		n.assign(n.current, "remote_chassis_id", raw, index+1)
	case "port id":
		n.assign(n.current, "remote_interface", raw, index+1)
	case "system name":
		n.assign(n.current, "remote_name", record.Scalar(parser.NormalizeValue(raw)), index+1)
	case "management address":
		v, e := parser.ParseIP(parser.NormalizeValue(raw))
		if e != nil {
			n.issue("invalid_management_address", index+1)
		} else {
			n.assign(n.current, "management_address", record.Scalar(v), index+1)
		}
	case "port vlan id(pvid)":
		n.assign(ensureMap(n.current, "details"), "port_vlan_id", raw, index+1)
	case "system description":
		// The description may continue on more-indented lines.
		n.description, n.descriptionLine, n.descriptionIndent = raw, index+1, indent
	default:
		if h3cLldpDetails[label] {
			n.assign(ensureMap(n.current, "details"), strings.ReplaceAll(label, " ", "_"), raw, index+1)
		} else {
			n.issue("unrecognized_field", index+1)
		}
	}
}

// storeDescription records an open system description.
func (n *neighborScan) storeDescription() {
	if n.descriptionIndent >= 0 && n.current != nil {
		n.assign(ensureMap(n.current, "details"), "system_description", n.description, n.descriptionLine)
		n.descriptionIndent = -1
		n.description = ""
	}
}

// flush validates and stores the current neighbor.
func (n *neighborScan) flush() {
	defer func() { n.current = nil }()
	if n.current == nil {
		return
	}
	for _, k := range []string{"local_interface", "remote_chassis_id", "remote_interface"} {
		if parsetext.Strip(record.StringValue(n.current[k])) == "" {
			n.issues = append(n.issues, wireless.Record{"code": "incomplete_neighbor"})
			return
		}
	}
	if strings.Contains(strings.ToLower(record.StringValue(record.Map(n.current["details"])["chassis_type"])), "mac address") {
		mac, e := parser.ParseMAC(new(record.StringValue(n.current["remote_chassis_id"])))
		if e != nil {
			n.issues = append(n.issues, wireless.Record{"code": "invalid_chassis_id"})
			return
		}
		n.current["remote_chassis_id"] = *mac
	}
	n.records = append(n.records, n.current)
}

// portIssues reports ports without evidence and "No neighbors" rows that
// contradict parsed neighbors.
func (n *neighborScan) portIssues() {
	populated := map[any]bool{}
	for _, r := range n.records {
		populated[r["local_interface"]] = true
	}
	for _, port := range n.ports {
		if _, empty := n.emptyPorts[port]; !populated[port] && !empty {
			n.issues = append(n.issues, wireless.Record{"code": "missing_port_evidence", "local_interface": port})
		}
	}
	for _, port := range n.emptyOrder {
		if (port == nil && len(n.records) > 0) || populated[port] {
			n.issue("conflicting_neighbor_count", n.emptyPorts[port])
		}
	}
}

// total is the reported neighbor count, or zero when every port reported
// "No neighbors" and none was parsed.
func (n *neighborScan) total(text string) *big.Int {
	total := reportedTotal(text, "(?:LLDP )?neighbors", &n.issues, n.continuations, "")
	if total != nil || len(n.records) > 0 || len(n.emptyPorts) == 0 {
		return total
	}
	for _, port := range n.ports {
		if _, ok := n.emptyPorts[port]; !ok {
			return total
		}
	}
	return big.NewInt(0)
}
