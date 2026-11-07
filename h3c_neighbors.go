package wireless

import (
	"math/big"
	"strings"
)

var neighborPort = rx(`(?i)^LLDP neighbor-information of port \d+\[(.+?)\]\s*:?$`)
var neighborIndex = rx(`(?i)^LLDP neighbor index\s*:`)
var neighborEmpty = rx(`(?i)^No (?:LLDP )?neighbors\s*$`)
var neighborSkip = rx(`(?i)^(?:LLDP agent\s+[^:]+:|(?:Total|Number of neighbors).*:\s*\d+)\s*$`)
var neighborField = rx(`^([^:]+):\s*(.*?)$`)

func (p *parseState) parseNeighbors(text string, complete bool) parseResult {
	records, issues := []Record{}, []Record{}
	var current Record
	var local any
	ports := []string{}
	seenPorts := map[string]bool{}
	emptyPorts := map[any]int{}
	emptyOrder := []any{}
	descriptionIndent := -1
	description, descriptionLine := "", 0
	continuations := map[int]bool{}
	storeDescription := func() {
		if descriptionIndent >= 0 && current != nil {
			p.assign(ensureMap(current, "details"), "system_description", description, &issues, descriptionLine)
			descriptionIndent = -1
			description = ""
		}
	}
	flush := func() {
		if current != nil {
			valid := true
			for _, k := range []string{"local_interface", "remote_chassis_id", "remote_interface"} {
				if strip(stringValue(current[k])) == "" {
					valid = false
				}
			}
			if valid {
				if strings.Contains(strings.ToLower(stringValue(asRecord(current["details"])["chassis_type"])), "mac address") {
					mac, e := ParseMAC(strptr(stringValue(current["remote_chassis_id"])))
					if e != nil {
						issues = append(issues, Record{"code": "invalid_chassis_id"})
						current = nil
						return
					}
					current["remote_chassis_id"] = *mac
				}
				records = append(records, current)
			} else {
				issues = append(issues, Record{"code": "incomplete_neighbor"})
			}
		}
		current = nil
	}
	for index, line := range sourceLines(text) {
		row := strip(line)
		if row == "" || separatorLine.MatchString(row) {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t\v\f"))
		if descriptionIndent >= 0 && indent > descriptionIndent {
			description += "\n" + row
			continuations[index+1] = true
			continue
		}
		storeDescription()
		if m := neighborPort.FindStringSubmatch(row); m != nil {
			flush()
			local = m[1]
			if !seenPorts[m[1]] {
				ports = append(ports, m[1])
				seenPorts[m[1]] = true
			}
		} else if neighborIndex.MatchString(row) {
			flush()
			current = Record{"local_interface": local}
		} else if neighborEmpty.MatchString(row) {
			if _, ok := emptyPorts[local]; !ok {
				emptyPorts[local] = index + 1
				emptyOrder = append(emptyOrder, local)
			}
		} else if neighborSkip.MatchString(row) {
			continue
		} else if m := neighborField.FindStringSubmatch(row); m != nil {
			label := strings.ToLower(strip(m[1]))
			raw := m[2]
			if current == nil {
				current = Record{"local_interface": local}
			}
			switch label {
			case "chassis id":
				p.assign(current, "remote_chassis_id", raw, &issues, index+1)
			case "port id":
				p.assign(current, "remote_interface", raw, &issues, index+1)
			case "system name":
				p.assign(current, "remote_name", optional(NormalizeValue(raw)), &issues, index+1)
			case "management address":
				v, e := ParseIP(NormalizeValue(raw))
				if e != nil {
					issues = append(issues, Record{"code": "invalid_management_address", "line": index + 1})
				} else {
					p.assign(current, "management_address", scalar(v), &issues, index+1)
				}
			case "port vlan id(pvid)":
				p.assign(ensureMap(current, "details"), "port_vlan_id", raw, &issues, index+1)
			default:
				if h3cLldpDetails[label] {
					if label == "system description" {
						description = raw
						descriptionLine = index + 1
						descriptionIndent = indent
					} else {
						p.assign(ensureMap(current, "details"), strings.ReplaceAll(label, " ", "_"), raw, &issues, index+1)
					}
				} else {
					issues = append(issues, Record{"code": "unrecognized_field", "line": index + 1})
				}
			}
		} else {
			issues = append(issues, Record{"code": "unrecognized_line", "line": index + 1})
		}
	}
	storeDescription()
	flush()
	populated := map[any]bool{}
	for _, r := range records {
		populated[r["local_interface"]] = true
	}
	for _, port := range ports {
		if !populated[port] {
			if _, empty := emptyPorts[port]; !empty {
				issues = append(issues, Record{"code": "missing_port_evidence", "local_interface": port})
			}
		}
	}
	for _, port := range emptyOrder {
		if (port == nil && len(records) > 0) || populated[port] {
			issues = append(issues, Record{"code": "conflicting_neighbor_count", "line": emptyPorts[port]})
		}
	}
	total := reportedTotal(text, "(?:LLDP )?neighbors", &issues, continuations, "")
	if total == nil && len(records) == 0 && len(emptyPorts) > 0 {
		all := true
		for _, port := range ports {
			if _, ok := emptyPorts[port]; !ok {
				all = false
			}
		}
		if all {
			total = big.NewInt(0)
		}
	}
	return fieldsResult(records, issues, complete, total, []string{"local_interface", "remote_chassis_id", "remote_interface"})
}
