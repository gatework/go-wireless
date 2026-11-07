package wireless

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strings"
)

type parseResult struct {
	status          Status
	records, issues []Record
}

func failedResult(code string) parseResult {
	return parseResult{status: Failed, records: []Record{}, issues: []Record{{"code": code}}}
}

type assignment struct {
	value      any
	line       int
	conflicted bool
}
type parseState struct {
	assignments map[uintptr]map[string]*assignment
}

func newParseState() *parseState { return &parseState{map[uintptr]map[string]*assignment{}} }
func (p *parseState) assign(record Record, field string, value any, issues *[]Record, line int, identities ...string) {
	id := field
	if len(identities) > 0 {
		id = identities[0]
	}
	key := reflect.ValueOf(record).Pointer()
	fields := p.assignments[key]
	if fields == nil {
		fields = map[string]*assignment{}
		p.assignments[key] = fields
	}
	if prev := fields[id]; prev != nil {
		if prev.conflicted || !reflect.DeepEqual(prev.value, value) {
			delete(record, field)
			*issues = append(*issues, Record{"code": "conflicting_field", "line": line, "first_line": prev.line})
			prev.conflicted = true
			return
		}
	} else {
		fields[id] = &assignment{value: value, line: line}
	}
	record[field] = value
}
func ensureMap(record Record, key string) Record {
	if v, ok := record[key].(Record); ok {
		return v
	}
	v := Record{}
	record[key] = v
	return v
}
func keyName(s string) string { return strings.NewReplacer(" ", "_", "-", "_").Replace(s) }
func uniqueIssues(issues []Record) []Record {
	seen := map[string]bool{}
	out := []Record{}
	for _, i := range issues {
		encoded, _ := json.Marshal(i)
		k := string(encoded)
		if !seen[k] {
			seen[k] = true
			out = append(out, i)
		}
	}
	return out
}
func fieldsResult(records []Record, issues []Record, complete bool, total *big.Int, identity []string) parseResult {
	if total != nil && total.Cmp(big.NewInt(int64(len(records)))) != 0 {
		issues = append(issues, Record{"code": "count_mismatch", "expected": integerJSON(total), "parsed": len(records)})
	}
	if !(complete && (total != nil || len(records) > 0)) {
		issues = append(issues, Record{"code": "missing_completeness_evidence"})
	}
	seen := map[string]bool{}
	unique := []Record{}
	for _, r := range records {
		values := make([]any, len(identity))
		for i, k := range identity {
			values[i] = r[k]
		}
		b, _ := json.Marshal(values)
		key := string(b)
		if seen[key] {
			issues = append(issues, Record{"code": "duplicate_identity"})
		} else {
			unique = append(unique, r)
			seen[key] = true
		}
	}
	issues = uniqueIssues(issues)
	status := Complete
	if len(issues) > 0 {
		status = Partial
		if len(unique) == 0 {
			status = Failed
		}
	}
	return parseResult{status, unique, issues}
}
func hasDuplicates(records []Record, identity []string) bool {
	seen := map[string]bool{}
	for _, r := range records {
		values := make([]any, len(identity))
		for i, k := range identity {
			values[i] = r[k]
		}
		b, _ := json.Marshal(values)
		if seen[string(b)] {
			return true
		}
		seen[string(b)] = true
	}
	return false
}

var separatorLine = rx(`^[-=]+$`)
var fieldPattern = rx(`^([^:]+?)\s*:\s*(.*)$`)
var fieldIgnored = rx(`^(?:total|number of aps|connected|remaining|maximum|current number|(?:local|server|sync) ap licenses|(?:current|max|bound) bss number)\b`)
var continuationPattern = rx(`^(?:NSS\d+\s+[\d, ]+|TID \d+\s+(?:In|Out|Both)|[\d., ]+(?:Mbps)?)$`)

type parsedField struct {
	label        string
	raw          *string
	line, indent int
}

func readFields(text string, issues *[]Record) []parsedField {
	fields := []parsedField{}
	for index, line := range sourceLines(text) {
		row := strip(line)
		if row == "" || separatorLine.MatchString(row) {
			continue
		}
		if m := fieldPattern.FindStringSubmatch(row); m != nil {
			label := strings.Join(splitWords(strings.ToLower(m[1])), " ")
			if fieldIgnored.MatchString(label) {
				continue
			}
			fields = append(fields, parsedField{label, NormalizeValue(m[2]), index + 1, len(line) - len(strings.TrimLeft(line, " \t\v\f\r\n"))})
		} else if strings.HasPrefix(row, ":") && len(fields) > 0 && fields[len(fields)-1].label == "poe status" && strip(row[1:]) != "" {
			s := strip(row[1:])
			prev := fields[len(fields)-1].raw
			if prev != nil {
				s = *prev + "\n" + s
			}
			fields[len(fields)-1].raw = &s
		} else if !continuationPattern.MatchString(row) {
			*issues = append(*issues, Record{"code": "unrecognized_line", "line": index + 1})
		}
	}
	return fields
}
func reportedTotal(text, noun string, issues *[]Record, continuations map[int]bool, auxiliary string) *big.Int {
	prefix := rx(`(?i)^(?:Total\b|Number of (?:` + noun + `)\b|Current BSS number\b)`)
	pattern := rx(`(?i)^(?:Total(?: number of)?(?:\s+(?:` + noun + `))?|Number of (?:` + noun + `)|Current BSS number)\s*:\s*(\d+)\s*$`)
	var aux *regexp.Regexp
	if auxiliary != "" {
		aux = rx(`(?i)^Total(?: number of)? (?:` + auxiliary + `)\s*:\s*\d+\s*$`)
	}
	var total *big.Int
	different := false
	for i, line := range sourceLines(text) {
		row := strip(line)
		if continuations[i+1] || !prefix.MatchString(row) {
			continue
		}
		if aux != nil && aux.MatchString(row) {
			continue
		}
		m := pattern.FindStringSubmatch(row)
		if m == nil {
			*issues = append(*issues, Record{"code": "invalid_total", "line": i + 1})
			continue
		}
		v, err := ParseIntegerExact(&m[1], nil)
		if err != nil {
			*issues = append(*issues, Record{"code": "invalid_total", "line": i + 1})
			continue
		}
		if total == nil {
			total = v
		} else if total.Cmp(v) != 0 {
			different = true
		}
	}
	if different {
		*issues = append(*issues, Record{"code": "conflicting_total"})
	}
	return total
}
func (p *parseState) assignField(record Record, mapping map[string]string, label string, raw *string, issues *[]Record, line int, details Record) {
	if field, ok := mapping[label]; record != nil && ok {
		if field == "channel_width" {
			if label == "channel band-width" {
				label = "channel bandwidth"
			}
			d := ensureMap(record, "details")
			p.assign(d, strings.ReplaceAll(label, " ", "_"), optional(raw), issues, line)
			if label == "channel bandwidth" {
				if assignments := p.assignments[reflect.ValueOf(d).Pointer()]; assignments["operating_bandwidth"] != nil {
					return
				}
			}
		}
		v, err := convertField(field, raw, label)
		if err != nil {
			*issues = append(*issues, Record{"code": "invalid_field", "line": line})
			return
		}
		identity := field
		if field == "channel_width" {
			identity = label
		}
		p.assign(record, field, v, issues, line, identity)
	} else if details != nil {
		var v any = optional(raw)
		if strings.HasSuffix(label, "url") {
			v = raw != nil && strip(*raw) != ""
		}
		p.assign(ensureMap(details, "details"), keyName(label), v, issues, line)
	} else {
		*issues = append(*issues, Record{"code": "unrecognized_field", "line": line})
	}
}
func convertField(field string, raw *string, label string) (any, error) {
	switch field {
	case "mac_address", "client_mac", "bssid":
		v, e := ParseMAC(raw)
		return scalar(v), e
	case "management_ip", "client_ip", "client_ipv6", "ip_address", "ipv6_address":
		v, e := ParseIP(raw)
		if e == nil && v != nil && ((label == "ipv4 address" && strings.Contains(*v, ":")) || (label == "ipv6 address" && !strings.Contains(*v, ":"))) {
			return nil, fmt.Errorf("invalid wireless address family")
		}
		return scalar(v), e
	case "ap_id", "radio_id":
		v, e := ParseIntegerExact(raw, nil)
		if v == nil {
			return nil, e
		}
		return v.String(), e
	case "client_count":
		return integerValue(raw)
	case "online_seconds", "uptime_seconds":
		return durationValue(raw)
	case "vlan", "service_vlan", "initial_vlan_id", "authorized_vlan_id":
		v, e := ParseVLAN(raw)
		return scalar(v), e
	case "channel":
		v, e := ParseChannel(raw)
		return scalar(v), e
	case "channel_width":
		return integerJSON(ParseChannelWidthExact(raw)), nil
	case "band":
		return scalar(FrequencyBand(stringValue(optional(raw)))), nil
	case "noise_dbm":
		v, e := ParseNumber(raw, "dBm")
		if v != nil && *v >= 0 {
			v = nil
		}
		return scalar(v), e
	case "utilization":
		v, e := ParseNumber(raw, "%")
		if v != nil && (*v < 0 || *v > 100) {
			return nil, fmt.Errorf("invalid wireless utilization")
		}
		if v == nil {
			return nil, e
		}
		return int64(math.Round(*v)), e
	default:
		return optional(raw), nil
	}
}
func (p *parseState) parseRecords(dim Dimension, text string, complete bool) parseResult {
	mapping := h3cClient
	details := h3cClientDetails
	noun := "clients"
	identity := []string{"client_mac"}
	if dim == BSSes {
		mapping = h3cBss
		details = map[string]bool{}
		noun = "BSSs?"
		identity = []string{"bssid"}
	}
	if dim == Dot1xConnections || dim == MABConnections {
		mapping = h3cAuth
		details = h3cAuthDetails
		noun = "(?:connections|users|online users)"
		identity = []string{"mac_address", "interface", "bssid"}
	}
	issues := []Record{}
	records := []Record{}
	fields := readFields(text, &issues)
	start := ""
	if dim == BSSes {
		for _, f := range fields {
			if f.label == "bssid" || f.label == "ap name" {
				if f.label == "ap name" {
					start = "ap name"
				}
				break
			}
		}
	}
	var current Record
	for _, f := range fields {
		if (start != "" && f.label == start) || (start == "" && mapping[f.label] == identity[0]) {
			current = Record{}
			records = append(records, current)
		}
		if current == nil {
			issues = append(issues, Record{"code": "missing_identity", "line": f.line})
			continue
		}
		switch {
		case dim == Clients && f.label == "rx/tx rate":
			rx, tx, e := ParseRatesMbps(f.raw)
			if e != nil {
				issues = append(issues, Record{"code": "invalid_field", "line": f.line})
			} else {
				p.assign(current, "rx_rate_mbps", scalar(rx), &issues, f.line)
				p.assign(current, "tx_rate_mbps", scalar(tx), &issues, f.line)
			}
		case dim == Clients && f.label == "rssi":
			signal, e := ParseNumber(f.raw, "dBm")
			if e != nil {
				issues = append(issues, Record{"code": "invalid_field", "line": f.line})
				continue
			}
			var dbm, rssi any
			if signal != nil && *signal < 0 {
				dbm = *signal
			}
			if signal != nil && *signal > 0 {
				rounded, _ := new(big.Float).SetFloat64(math.Round(*signal)).Int(nil)
				rssi = integerJSON(rounded)
			}
			p.assign(current, "signal_dbm", dbm, &issues, f.line)
			p.assign(current, "rssi", rssi, &issues, f.line)
			if current["signal_dbm"] == nil {
				delete(current, "signal_dbm")
			}
			if current["rssi"] == nil {
				delete(current, "rssi")
			}
		case dim == BSSes && h3cBssSecurity[f.label]:
			p.assign(ensureMap(current, "security"), keyName(f.label), optional(f.raw), &issues, f.line)
		default:
			var d Record
			if details[f.label] {
				d = current
			}
			p.assignField(current, mapping, f.label, f.raw, &issues, f.line, d)
		}
	}
	valid := []Record{}
	for _, r := range records {
		ok := strip(stringValue(r[identity[0]])) != ""
		if dim == BSSes {
			ok = ok && strip(stringValue(r["ap_name"])) != "" && r["radio_id"] != nil
		}
		if !ok {
			issues = append(issues, Record{"code": "invalid_identity"})
		} else {
			valid = append(valid, r)
		}
	}
	total := reportedTotal(text, noun, &issues, nil, "")
	return fieldsResult(valid, issues, complete, total, identity)
}
