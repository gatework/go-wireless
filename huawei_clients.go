package wireless

import (
	"errors"
	"strings"
)

var radioWLAN = rx(`^\d+/\d+$`)

func parseHuaweiClient(c []string) (Record, error) {
	if len(c) != 15 || !radioWLAN.MatchString(c[3]) {
		return nil, errors.New("invalid station row")
	}
	mac, e1 := ParseMAC(&c[0])
	id, e2 := ParseIntegerExact(&c[1], nil)
	rx, tx, e3 := ParseRatesMbps(&c[6])
	signal, e4 := ParseNumber(NormalizeValue(c[7]), "dBm")
	vlan, e5 := ParseVLAN(&c[8])
	ip, e6 := ParseIP(NormalizeValue(c[9]))
	ipv6, e7 := ParseIP(NormalizeValue(c[12]))
	uptime, e8 := ParseDurationExact(NormalizeValue(c[14]))
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
	return Record{"client_mac": scalar(mac), "ap_id": idText, "ap_name": c[2], "radio_id": strings.Split(c[3], "/")[0], "band": scalar(FrequencyBand(c[4])), "protocol": c[5], "rx_rate_mbps": scalar(rx), "tx_rate_mbps": scalar(tx), "signal_dbm": scalar(signal), "vlan": scalar(vlan), "client_ip": scalar(ip), "ssid": c[10], "operational_status": c[11], "client_ipv6": scalar(ipv6), "uptime_seconds": integerJSON(uptime)}, nil
}
