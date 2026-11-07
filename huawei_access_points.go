package wireless

import (
	"errors"
	"math/big"
)

func parseHuaweiAP(columns []string) (Record, error) {
	if len(columns) != 9 {
		return nil, errors.New("invalid AP row")
	}
	id, e1 := ParseIntegerExact(&columns[0], nil)
	mac, e2 := ParseMAC(&columns[1])
	ip, e3 := ParseIP(NormalizeValue(columns[4]))
	clients, e4 := ParseIntegerExact(&columns[7], nil)
	uptime, e5 := ParseDurationExact(NormalizeValue(columns[8]))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
		return nil, errors.New("invalid AP row")
	}
	idText := ""
	if id != nil {
		idText = id.String()
	}
	return Record{"ap_id": idText, "mac_address": scalar(mac), "name": columns[2], "group_name": columns[3], "management_ip": scalar(ip), "model": columns[5], "operational_status": OnlineStatus(columns[6]), "client_count": integerJSON(clients), "uptime_seconds": integerJSON(uptime)}, nil
}

var huaweiTotal = rx(`(?i)^Total\s*:\s*(\d+)$`)
var huaweiAPHeader = rx(`(?i)^ID\s+MAC\s+Name\s+Group\s+IP\s+Type\s+State\s+STA\s+Uptime\b`)
var huaweiClientHeader = rx(`(?i)^STA MAC\s+AP ID\s+Ap name\s+Rf/WLAN\b`)
var huaweiSkip = rx(`(?i)^Total AP information:|^(?:nor|fault|idle|stdby)\s*:`)

func parseHuawei(dim Dimension, text string, complete bool) parseResult {
	records, issues := []Record{}, []Record{}
	header := false
	var total *big.Int
	for index, line := range sourceLines(text) {
		row := strip(line)
		if row == "" || separatorLine.MatchString(row) {
			continue
		}
		if m := huaweiTotal.FindStringSubmatch(row); m != nil {
			v, e := ParseIntegerExact(&m[1], nil)
			if e != nil {
				issues = append(issues, Record{"code": "invalid_row", "line": index + 1})
				continue
			}
			if total != nil && total.Cmp(v) != 0 {
				issues = append(issues, Record{"code": "conflicting_total", "line": index + 1})
			}
			if total == nil {
				total = v
			}
		} else if (dim == AccessPoints && huaweiAPHeader.MatchString(row)) || (dim == Clients && huaweiClientHeader.MatchString(row)) {
			header = true
		} else if huaweiSkip.MatchString(row) {
			continue
		} else if !header {
			issues = append(issues, Record{"code": "missing_header", "line": index + 1})
		} else {
			var record Record
			var err error
			if dim == AccessPoints {
				record, err = parseHuaweiAP(splitWords(row))
			} else {
				record, err = parseHuaweiClient(splitWords(row))
			}
			if err != nil {
				issues = append(issues, Record{"code": "invalid_row", "line": index + 1})
			} else {
				records = append(records, record)
			}
		}
	}
	if !header {
		issues = append(issues, Record{"code": "missing_header"})
	}
	if total == nil {
		issues = append(issues, Record{"code": "missing_total"})
	}
	identity := []string{"name"}
	if dim == Clients {
		identity = []string{"client_mac"}
	}
	return fieldsResult(records, issues, complete, total, identity)
}
