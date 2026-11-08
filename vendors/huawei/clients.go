package huawei

import (
	"errors"
	"regexp"
	"strings"

	"github.com/gatework/go-wireless/internal/record"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

var radioWLAN = regexp.MustCompile(`^\d+/\d+$`)

func parseHuaweiClient(c []string) (wireless.Record, error) {
	if len(c) != 15 || !radioWLAN.MatchString(c[3]) {
		return nil, errors.New("invalid station row")
	}
	mac, e1 := parser.ParseMAC(&c[0])
	id, e2 := parser.ParseIntegerExact(&c[1], nil)
	rx, tx, e3 := parser.ParseRatesMbps(&c[6])
	signal, e4 := parser.ParseNumber(parser.NormalizeValue(c[7]), "dBm")
	vlan, e5 := parser.ParseVLAN(&c[8])
	ip, e6 := parser.ParseIP(parser.NormalizeValue(c[9]))
	ipv6, e7 := parser.ParseIP(parser.NormalizeValue(c[12]))
	uptime, e8 := parser.ParseDurationExact(parser.NormalizeValue(c[14]))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil || e7 != nil || e8 != nil || (ipv6 != nil && !strings.Contains(*ipv6, ":")) {
		return nil, errors.New("invalid station row")
	}
	if signal != nil && *signal == 0 {
		signal = nil
	}
	idText := ""
	if id != nil {
		idText = id.String()
	}
	return wireless.Record{"client_mac": record.Scalar(mac), "ap_id": idText, "ap_name": c[2], "radio_id": strings.Split(c[3], "/")[0], "band": record.Scalar(parser.FrequencyBand(c[4])), "protocol": c[5], "rx_rate_mbps": record.Scalar(rx), "tx_rate_mbps": record.Scalar(tx), "signal_dbm": record.Scalar(signal), "vlan": record.Scalar(vlan), "client_ip": record.Scalar(ip), "ssid": c[10], "operational_status": c[11], "client_ipv6": record.Scalar(ipv6), "uptime_seconds": record.IntegerJSON(uptime)}, nil
}
