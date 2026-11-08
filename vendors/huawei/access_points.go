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
	records, issues := []wireless.Record{}, []wireless.Record{}
	header := false
	var total *big.Int
	for index, line := range parser.SourceLines(text) {
		row := parsetext.Strip(line)
		if row == "" || parsetext.IsSeparator(row) {
			continue
		}
		if m := huaweiTotal.FindStringSubmatch(row); m != nil {
			v, e := parser.ParseIntegerExact(&m[1], nil)
			if e != nil {
				issues = append(issues, wireless.Record{"code": "invalid_row", "line": index + 1})
				continue
			}
			if total != nil && total.Cmp(v) != 0 {
				issues = append(issues, wireless.Record{"code": "conflicting_total", "line": index + 1})
			}
			if total == nil {
				total = v
			}
		} else if (dim == wireless.AccessPoints && huaweiAPHeader.MatchString(row)) || (dim == wireless.Clients && huaweiClientHeader.MatchString(row)) {
			header = true
		} else if huaweiSkip.MatchString(row) {
			continue
		} else if !header {
			issues = append(issues, wireless.Record{"code": "missing_header", "line": index + 1})
		} else {
			var record wireless.Record
			var err error
			if dim == wireless.AccessPoints {
				record, err = parseHuaweiAP(parsetext.SplitWords(row))
			} else {
				record, err = parseHuaweiClient(parsetext.SplitWords(row))
			}
			if err != nil {
				issues = append(issues, wireless.Record{"code": "invalid_row", "line": index + 1})
			} else {
				records = append(records, record)
			}
		}
	}
	if !header {
		issues = append(issues, wireless.Record{"code": "missing_header"})
	}
	if total == nil {
		issues = append(issues, wireless.Record{"code": "missing_total"})
	}
	identity := []string{"name"}
	if dim == wireless.Clients {
		identity = []string{"client_mac"}
	}
	return parser.FieldsResult(records, issues, complete, total, identity)
}
