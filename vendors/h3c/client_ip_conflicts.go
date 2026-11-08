package h3c

import (
	"math/big"
	"regexp"
	"time"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

var conflictHeader = regexp.MustCompile(`(?i)^IP\s+New-MAC/APID\s+Old-MAC/APID\s+Time$`)
var conflictRow = regexp.MustCompile(`^(\S+)\s+(\S+)/(\d+)\s+(\S+)/(\d+)\s+(\d{2}-\d{2} \d{2}:\d{2}:\d{2})$`)

func (p *parseState) parseConflicts(text string, complete bool) parser.Result {
	records, issues := []wireless.Record{}, []wireless.Record{}
	header := false
	for index, line := range parser.SourceLines(text) {
		row := parsetext.Strip(line)
		if row == "" || separatorLine.MatchString(row) {
			continue
		}
		if conflictHeader.MatchString(row) {
			header = true
			continue
		}
		m := conflictRow.FindStringSubmatch(row)
		valid := m != nil
		var record wireless.Record
		if valid {
			ip, e1 := parser.ParseIP(&m[1])
			mac1, e2 := parser.ParseMAC(&m[2])
			id1, e3 := parser.ParseIntegerExact(&m[3], nil)
			mac2, e4 := parser.ParseMAC(&m[4])
			id2, e5 := parser.ParseIntegerExact(&m[5], nil)
			_, e6 := time.Parse("2006-01-02 15:04:05", "2000-"+m[6])
			valid = e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil
			if valid {
				record = wireless.Record{"ip_address": *ip, "new_mac_address": *mac1, "new_ap_id": id1.String(), "old_mac_address": *mac2, "old_ap_id": id2.String(), "occurred_at": m[6]}
			}
		}
		if valid {
			records = append(records, record)
		} else {
			issues = append(issues, wireless.Record{"code": "invalid_row", "line": index + 1})
		}
	}
	var total *big.Int
	if header && len(records) == 0 && len(issues) == 0 {
		total = big.NewInt(0)
	}
	return parser.FieldsResult(records, issues, complete, total, []string{"ip_address", "new_mac_address", "new_ap_id", "old_mac_address", "old_ap_id", "occurred_at"})
}
