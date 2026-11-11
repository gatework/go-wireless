package huawei

import (
	"errors"
	"math/big"
	"regexp"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
	recordvalue "github.com/gatework/go-wireless/internal/record"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

func parseHuaweiAP(columns []string) (wireless.Record, error) {
	if len(columns) != 9 {
		return nil, errors.New("invalid AP row")
	}
	id, e1 := parser.ParseIntegerExact(&columns[0], nil)
	mac, e2 := parser.ParseMAC(&columns[1])
	ip, e3 := parser.ParseIP(parser.NormalizeValue(columns[4]))
	clients, e4 := parser.ParseIntegerExact(&columns[7], nil)
	uptime, e5 := parser.ParseDurationExact(parser.NormalizeValue(columns[8]))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
		return nil, errors.New("invalid AP row")
	}
	idText := ""
	if id != nil {
		idText = id.String()
	}
	return wireless.Record{"ap_id": idText, "mac_address": recordvalue.Scalar(mac), "name": columns[2], "group_name": columns[3], "management_ip": recordvalue.Scalar(ip), "model": columns[5], "operational_status": parser.OnlineStatus(columns[6]), "client_count": recordvalue.IntegerJSON(clients), "uptime_seconds": recordvalue.IntegerJSON(uptime)}, nil
}

var huaweiTotal = regexp.MustCompile(`(?i)^Total\s*:\s*(\d+)$`)
var huaweiAPHeader = regexp.MustCompile(`(?i)^ID\s+MAC\s+Name\s+Group\s+IP\s+Type\s+State\s+STA\s+Uptime\b`)
var huaweiClientHeader = regexp.MustCompile(`(?i)^STA MAC\s+AP ID\s+Ap name\s+Rf/WLAN\b`)
var huaweiSkip = regexp.MustCompile(`(?i)^Total AP information:|^(?:nor|fault|idle|stdby)\s*:`)

func parseHuawei(dim wireless.Dimension, text string, complete bool) parser.Result {
	t := &huaweiTable{dim: dim, records: []wireless.Record{}, issues: []wireless.Record{}}
	for index, line := range parser.SourceLines(text) {
		t.line(index, parsetext.Strip(line))
	}
	if !t.header {
		t.issues = append(t.issues, wireless.Record{"code": "missing_header"})
	}
	if t.total == nil {
		t.issues = append(t.issues, wireless.Record{"code": "missing_total"})
	}
	identity := []string{"name"}
	if dim == wireless.Clients {
		identity = []string{"client_mac"}
	}
	return parser.FieldsResult(t.records, t.issues, complete, t.total, identity)
}

// huaweiTable is the state of one Huawei AP or station table parse.
type huaweiTable struct {
	dim             wireless.Dimension
	records, issues []wireless.Record
	header          bool
	total           *big.Int
}

func (t *huaweiTable) issue(code string, index int) {
	t.issues = append(t.issues, wireless.Record{"code": code, "line": index + 1})
}

// line consumes one stripped table line (index is 0-based).
func (t *huaweiTable) line(index int, row string) {
	if row == "" || parsetext.IsSeparator(row) {
		return
	}
	if m := huaweiTotal.FindStringSubmatch(row); m != nil {
		t.reportedTotal(index, m[1])
		return
	}
	switch {
	case (t.dim == wireless.AccessPoints && huaweiAPHeader.MatchString(row)) || (t.dim == wireless.Clients && huaweiClientHeader.MatchString(row)):
		t.header = true
	case huaweiSkip.MatchString(row):
	case !t.header:
		t.issue("missing_header", index)
	default:
		t.row(index, row)
	}
}

// reportedTotal records the table's total; a second, different total
// conflicts.
func (t *huaweiTable) reportedTotal(index int, raw string) {
	v, e := parser.ParseIntegerExact(&raw, nil)
	if e != nil {
		t.issue("invalid_row", index)
		return
	}
	if t.total != nil && t.total.Cmp(v) != 0 {
		t.issue("conflicting_total", index)
	}
	if t.total == nil {
		t.total = v
	}
}

// row parses one data row.
func (t *huaweiTable) row(index int, row string) {
	parse := parseHuaweiClient
	if t.dim == wireless.AccessPoints {
		parse = parseHuaweiAP
	}
	record, err := parse(parsetext.SplitWords(row))
	if err != nil {
		t.issue("invalid_row", index)
		return
	}
	t.records = append(t.records, record)
}
