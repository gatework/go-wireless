package wireless

import (
	"math/big"
)

var blockedSkip = rx(`(?i)^(?:No blocked clients|MAC address\b.*|(?:Client )?block list|Blocked clients|Total.*:\s*\d+)$`)
var blockedRow = rx(`(?i)^((?:[0-9a-f]{4}-){2}[0-9a-f]{4}|(?:[0-9a-f]{2}:){5}[0-9a-f]{2})\s*(.*?)$`)

func (p *parseState) parseBlocked(text string, complete bool) parseResult {
	records, issues := []Record{}, []Record{}
	for index, line := range sourceLines(text) {
		row := strip(line)
		if row == "" || separatorLine.MatchString(row) || blockedSkip.MatchString(row) {
			continue
		}
		if m := blockedRow.FindStringSubmatch(row); m != nil {
			mac, _ := ParseMAC(&m[1])
			records = append(records, Record{"mac_address": *mac, "reason": m[2]})
		} else {
			issues = append(issues, Record{"code": "unrecognized_line", "line": index + 1})
		}
	}
	total := reportedTotal(text, "(?:blocked clients|clients|entries)", &issues, nil, "")
	if rx(`(?i)^No blocked clients\s*$`).MatchString(strip(text)) {
		total = big.NewInt(0)
	}
	return fieldsResult(records, issues, complete, total, []string{"mac_address"})
}
