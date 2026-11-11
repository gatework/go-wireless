package h3c

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strings"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
	recordvalue "github.com/gatework/go-wireless/internal/record"
	fields "github.com/gatework/go-wireless/vendors/internal/fields"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

type assignment struct {
	value      any
	line       int
	conflicted bool
}
type parseState struct {
	assignments map[uintptr]map[string]*assignment
}

func newParseState() *parseState { return &parseState{map[uintptr]map[string]*assignment{}} }
func (p *parseState) assign(record wireless.Record, field string, value any, issues *[]wireless.Record, line int, identities ...string) {
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
			*issues = append(*issues, wireless.Record{"code": "conflicting_field", "line": line, "first_line": prev.line})
			prev.conflicted = true
			return
		}
	} else {
		fields[id] = &assignment{value: value, line: line}
	}
	record[field] = value
}
func ensureMap(record wireless.Record, key string) wireless.Record {
	if v, ok := record[key].(wireless.Record); ok {
		return v
	}
	v := wireless.Record{}
	record[key] = v
	return v
}
func keyName(s string) string { return strings.NewReplacer(" ", "_", "-", "_").Replace(s) }

var separatorLine = regexp.MustCompile(`^[-=]+$`)
var fieldPattern = regexp.MustCompile(`^([^:]+?)\s*:\s*(.*)$`)
var fieldIgnored = regexp.MustCompile(`^(?:total|number of aps|connected|remaining|maximum|current number|(?:local|server|sync) ap licenses|(?:current|max|bound) bss number)\b`)
var continuationPattern = regexp.MustCompile(`^(?:NSS\d+\s+[\d, ]+|TID \d+\s+(?:In|Out|Both)|[\d., ]+(?:Mbps)?)$`)

type parsedField struct {
	label        string
	raw          *string
	line, indent int
}

func readFields(text string, issues *[]wireless.Record) []parsedField {
	fields := []parsedField{}
	for index, line := range parser.SourceLines(text) {
		row := parsetext.Strip(line)
		if row == "" || separatorLine.MatchString(row) {
			continue
		}
		if m := fieldPattern.FindStringSubmatch(row); m != nil {
			label := strings.Join(parsetext.SplitWords(strings.ToLower(m[1])), " ")
			if fieldIgnored.MatchString(label) {
				continue
			}
			fields = append(fields, parsedField{label, parser.NormalizeValue(m[2]), index + 1, len(line) - len(strings.TrimLeft(line, " \t\v\f\r\n"))})
		} else if strings.HasPrefix(row, ":") && len(fields) > 0 && fields[len(fields)-1].label == "poe status" && parsetext.Strip(row[1:]) != "" {
			s := parsetext.Strip(row[1:])
			prev := fields[len(fields)-1].raw
			if prev != nil {
				s = *prev + "\n" + s
			}
			fields[len(fields)-1].raw = &s
		} else if !continuationPattern.MatchString(row) {
			*issues = append(*issues, wireless.Record{"code": "unrecognized_line", "line": index + 1})
		}
	}
	return fields
}
func reportedTotal(text, noun string, issues *[]wireless.Record, continuations map[int]bool, auxiliary string) *big.Int {
	prefix := regexp.MustCompile(`(?i)^(?:Total\b|Number of (?:` + noun + `)\b|Current BSS number\b)`)
	pattern := regexp.MustCompile(`(?i)^(?:Total(?: number of)?(?:\s+(?:` + noun + `))?|Number of (?:` + noun + `)|Current BSS number)\s*:\s*(\d+)\s*$`)
	var aux *regexp.Regexp
	if auxiliary != "" {
		aux = regexp.MustCompile(`(?i)^Total(?: number of)? (?:` + auxiliary + `)\s*:\s*\d+\s*$`)
	}
	var total *big.Int
	different := false
	for i, line := range parser.SourceLines(text) {
		row := parsetext.Strip(line)
		if continuations[i+1] || !prefix.MatchString(row) {
			continue
		}
		if aux != nil && aux.MatchString(row) {
			continue
		}
		m := pattern.FindStringSubmatch(row)
		if m == nil {
			*issues = append(*issues, wireless.Record{"code": "invalid_total", "line": i + 1})
			continue
		}
		v, err := parser.ParseIntegerExact(&m[1], nil)
		if err != nil {
			*issues = append(*issues, wireless.Record{"code": "invalid_total", "line": i + 1})
			continue
		}
		if total == nil {
			total = v
		} else if total.Cmp(v) != 0 {
			different = true
		}
	}
	if different {
		*issues = append(*issues, wireless.Record{"code": "conflicting_total"})
	}
	return total
}
func (p *parseState) assignField(record wireless.Record, mapping map[string]string, label string, raw *string, issues *[]wireless.Record, line int, details wireless.Record) {
	if field, ok := mapping[label]; record != nil && ok {
		if field == "channel_width" {
			if label == "channel band-width" {
				label = "channel bandwidth"
			}
			d := ensureMap(record, "details")
			p.assign(d, strings.ReplaceAll(label, " ", "_"), recordvalue.Scalar(raw), issues, line)
			if label == "channel bandwidth" {
				if assignments := p.assignments[reflect.ValueOf(d).Pointer()]; assignments["operating_bandwidth"] != nil {
					return
				}
			}
		}
		v, err := convertField(field, raw, label)
		if err != nil {
			*issues = append(*issues, wireless.Record{"code": "invalid_field", "line": line})
			return
		}
		identity := field
		if field == "channel_width" {
			identity = label
		}
		p.assign(record, field, v, issues, line, identity)
	} else if details != nil {
		var v = recordvalue.Scalar(raw)
		if strings.HasSuffix(label, "url") {
			v = raw != nil && parsetext.Strip(*raw) != ""
		}
		p.assign(ensureMap(details, "details"), keyName(label), v, issues, line)
	} else {
		*issues = append(*issues, wireless.Record{"code": "unrecognized_field", "line": line})
	}
}
func convertField(field string, raw *string, label string) (any, error) {
	switch field {
	case "mac_address", "client_mac", "bssid":
		v, e := parser.ParseMAC(raw)
		return recordvalue.Scalar(v), e
	case "management_ip", "client_ip", "client_ipv6", "ip_address", "ipv6_address":
		v, e := parser.ParseIP(raw)
		if e == nil && v != nil && ((label == "ipv4 address" && strings.Contains(*v, ":")) || (label == "ipv6 address" && !strings.Contains(*v, ":"))) {
			return nil, fmt.Errorf("invalid wireless address family")
		}
		return recordvalue.Scalar(v), e
	case "ap_id", "radio_id":
		v, e := parser.ParseIntegerExact(raw, nil)
		if v == nil {
			return nil, e
		}
		return v.String(), e
	case "client_count":
		return fields.IntegerValue(raw)
	case "online_seconds", "uptime_seconds":
		return fields.DurationValue(raw)
	case "vlan", "service_vlan", "initial_vlan_id", "authorized_vlan_id":
		v, e := parser.ParseVLAN(raw)
		return recordvalue.Scalar(v), e
	case "channel":
		v, e := parser.ParseChannel(raw)
		return recordvalue.Scalar(v), e
	case "channel_width":
		return recordvalue.IntegerJSON(parser.ParseChannelWidthExact(raw)), nil
	case "band":
		return recordvalue.Scalar(parser.FrequencyBand(recordvalue.StringValue(recordvalue.Scalar(raw)))), nil
	case "noise_dbm":
		v, e := parser.ParseNumber(raw, "dBm")
		if v != nil && *v >= 0 {
			v = nil
		}
		return recordvalue.Scalar(v), e
	case "utilization":
		v, e := parser.ParseNumber(raw, "%")
		if v != nil && (*v < 0 || *v > 100) {
			return nil, fmt.Errorf("invalid wireless utilization")
		}
		if v == nil {
			return nil, e
		}
		return int64(math.Round(*v)), e
	default:
		return recordvalue.Scalar(raw), nil
	}
}
func (p *parseState) parseRecords(dim wireless.Dimension, text string, complete bool) parser.Result {
	mapping := h3cClient
	details := h3cClientDetails
	noun := "clients"
	identity := []string{"client_mac"}
	if dim == wireless.BSSes {
		mapping = h3cBss
		details = map[string]bool{}
		noun = "BSSs?"
		identity = []string{"bssid"}
	}
	if dim == wireless.Dot1xConnections || dim == wireless.MABConnections {
		mapping = h3cAuth
		details = h3cAuthDetails
		noun = "(?:connections|users|online users)"
		identity = []string{"mac_address", "interface", "bssid"}
	}
	issues := []wireless.Record{}
	records := []wireless.Record{}
	fields := readFields(text, &issues)
	start := ""
	if dim == wireless.BSSes {
		for _, f := range fields {
			if f.label == "bssid" || f.label == "ap name" {
				if f.label == "ap name" {
					start = "ap name"
				}
				break
			}
		}
	}
	var current wireless.Record
	for _, f := range fields {
		if (start != "" && f.label == start) || (start == "" && mapping[f.label] == identity[0]) {
			current = wireless.Record{}
			records = append(records, current)
		}
		if current == nil {
			issues = append(issues, wireless.Record{"code": "missing_identity", "line": f.line})
			continue
		}
		switch {
		case dim == wireless.Clients && f.label == "rx/tx rate":
			rx, tx, e := parser.ParseRatesMbps(f.raw)
			if e != nil {
				issues = append(issues, wireless.Record{"code": "invalid_field", "line": f.line})
			} else {
				p.assign(current, "rx_rate_mbps", recordvalue.Scalar(rx), &issues, f.line)
				p.assign(current, "tx_rate_mbps", recordvalue.Scalar(tx), &issues, f.line)
			}
		case dim == wireless.Clients && f.label == "rssi":
			signal, e := parser.ParseNumber(f.raw, "dBm")
			if e != nil {
				issues = append(issues, wireless.Record{"code": "invalid_field", "line": f.line})
				continue
			}
			var dbm, rssi any
			if signal != nil && *signal < 0 {
				dbm = *signal
			}
			if signal != nil && *signal > 0 {
				rounded, _ := new(big.Float).SetFloat64(math.Round(*signal)).Int(nil)
				rssi = recordvalue.IntegerJSON(rounded)
			}
			p.assign(current, "signal_dbm", dbm, &issues, f.line)
			p.assign(current, "rssi", rssi, &issues, f.line)
			if current["signal_dbm"] == nil {
				delete(current, "signal_dbm")
			}
			if current["rssi"] == nil {
				delete(current, "rssi")
			}
		case dim == wireless.BSSes && h3cBssSecurity[f.label]:
			p.assign(ensureMap(current, "security"), keyName(f.label), recordvalue.Scalar(f.raw), &issues, f.line)
		default:
			var d wireless.Record
			if details[f.label] {
				d = current
			}
			p.assignField(current, mapping, f.label, f.raw, &issues, f.line, d)
		}
	}
	valid := []wireless.Record{}
	for _, r := range records {
		ok := parsetext.Strip(recordvalue.StringValue(r[identity[0]])) != ""
		if dim == wireless.BSSes {
			ok = ok && parsetext.Strip(recordvalue.StringValue(r["ap_name"])) != "" && r["radio_id"] != nil
		}
		if !ok {
			issues = append(issues, wireless.Record{"code": "invalid_identity"})
		} else {
			valid = append(valid, r)
		}
	}
	total := reportedTotal(text, noun, &issues, nil, "")
	return parser.FieldsResult(valid, issues, complete, total, identity)
}
