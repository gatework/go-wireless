package h3c

import (
	"math/big"
	"regexp"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

var blockedSkip = regexp.MustCompile(`(?i)^(?:No blocked clients|MAC address\b.*|(?:Client )?block list|Blocked clients|Total.*:\s*\d+)$`)
var blockedRow = regexp.MustCompile(`(?i)^((?:[0-9a-f]{4}-){2}[0-9a-f]{4}|(?:[0-9a-f]{2}:){5}[0-9a-f]{2})\s*(.*?)$`)

// noBlockedClients is the output for an empty block list.
var noBlockedClients = regexp.MustCompile(`(?i)^No blocked clients\s*$`)

func (p *parseState) parseBlocked(complete bool) parser.Result {
	records, issues := []wireless.Record{}, []wireless.Record{}
	for sourceLine := range p.source.All() {
		index, line := sourceLine.Number-1, sourceLine.Text
		row := parsetext.Strip(line)
		if row == "" || separatorLine.MatchString(row) || blockedSkip.MatchString(row) {
			continue
		}
		if m := blockedRow.FindStringSubmatch(row); m != nil {
			mac, _ := parser.ParseMAC(&m[1])
			records = append(records, wireless.Record{"mac_address": *mac, "reason": m[2]})
		} else {
			issues = append(issues, wireless.Record{"code": "unrecognized_line", "line": index + 1})
		}
	}
	total := p.reportedTotal("(?:blocked clients|clients|entries)", &issues, nil)
	if noBlockedClients.MatchString(parsetext.Strip(p.source.Text())) {
		total = big.NewInt(0)
	}
	return parser.FieldsResult(records, issues, complete, total, []string{"mac_address"})
}
