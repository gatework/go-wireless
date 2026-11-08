package h3c

import (
	"errors"
	"math/big"
	"regexp"
	"strings"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
	recordvalue "github.com/gatework/go-wireless/internal/record"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

func (p *configurationParser) applySetting(scope *configurationScope, block *sourceBlock) (bool, error) {
	line := p.source.lines[block.index].text
	owner, kind, record := scope.record, scope.kind, scope.settings()
	words := parsetext.SplitWords(line)
	enabled := !strings.HasPrefix(line, "undo ")
	var capture []string
	matches := func(pattern string) bool {
		capture = regexp.MustCompile(pattern).FindStringSubmatch(line)
		return capture != nil
	}
	assign := func(key string, value any) { p.assign(record, key, value, block) }
	last := words[len(words)-1]
	switch {
	case matches(`^(?:undo )?region-code-lock enable$`):
		if kind != "global_configuration" {
			return false, nil
		}
		assign("region_code_lock", enabled)
	case matches(`^nas-id (\S+)$`):
		if kind != "global_configuration" {
			return false, nil
		}
		assign("nas_id", capture[1])
	case matches(`^(?:undo )?rrop anti-bmc network (ipv4-and-ipv6-simple) enable$`):
		if kind != "ap_groups" {
			return false, nil
		}
		p.assign(ensureMap(record, "rrop_anti_bmc"), "network", wireless.Record{"mode": capture[1], "enabled": enabled}, block)
	case matches(`^rrop anti-bmc default-action (deny|permit)$`):
		if kind != "ap_groups" {
			return false, nil
		}
		p.assign(ensureMap(record, "rrop_anti_bmc"), "default_action", capture[1], block)
	case matches(`^serial-id (\S+)$`):
		if kind != "access_points" && kind != "ap_groups" {
			return false, nil
		}
		if kind == "ap_groups" {
			appendValue(owner, "serial_ids", last)
		} else {
			assign("serial_id", last)
		}
	case matches(`^ap (\S+)$`):
		if kind != "ap_groups" {
			return false, nil
		}
		appendValue(owner, "ap_names", last)
	case matches(`^vlan (\d+)$`):
		v, e := parser.ParseVLAN(&last)
		if e != nil {
			return false, e
		}
		if kind == "ap_groups" {
			appendValue(owner, "vlan_ids", recordvalue.Scalar(v))
		} else {
			p.assign(owner, "vlan_id", recordvalue.Scalar(v), block)
		}
	case matches(`^ssid (.+)$`):
		if kind != "service_templates" {
			return false, nil
		}
		p.assign(owner, "ssid", capture[1], block)
	case matches(`^(?:undo )?service-template enable$`):
		if kind != "service_templates" {
			return false, nil
		}
		p.assign(owner, "enabled", enabled, block)
	case matches(`^service-template (\S+)(?: vlan (\d+))?$`):
		if kind != "radio" {
			return false, nil
		}
		var raw *string
		if capture[2] != "" {
			raw = &capture[2]
		}
		v, e := parser.ParseVLAN(raw)
		if e != nil {
			return false, e
		}
		if e = p.bindTemplate(record, capture[1], recordvalue.Scalar(v)); e != nil {
			return false, e
		}
	case matches(`(?i)^preshared-key\s+.+$`):
		if kind != "service_templates" {
			return false, nil
		}
		assign("has_preshared_key", true)
	case matches(`^akm mode (.+)$`):
		if kind != "service_templates" {
			return false, nil
		}
		values := parsetext.SplitWords(strings.ReplaceAll(strings.ToLower(capture[1]), "802.1x", "dot1x"))
		assign("akm_modes", values)
	case matches(`^(cipher-suite|security-ie) (.+)$`):
		if kind != "service_templates" {
			return false, nil
		}
		key := "security_ies"
		if words[0] == "cipher-suite" {
			key = "cipher_suites"
		}
		for _, word := range words[1:] {
			word = strings.ToLower(word)
			if !arrayIncludes(record[key], word) {
				appendValue(record, key, word)
			}
		}
	case matches(`^client-security authentication-mode (.+)$`):
		mode := strings.ToLower(capture[1])
		valid := false
		for _, value := range []string{"dot1x", "mac", "mac-and-dot1x", "dot1x-then-mac", "mac-then-dot1x", "oui-then-dot1x"} {
			if mode == value {
				valid = true
			}
		}
		if !valid {
			return false, errors.New("invalid client authentication mode")
		}
		assign("client_security_authentication_mode", mode)
	case matches(`^client-security (ignore-authentication|ignore-authorization)$`):
		assign(keyName(last), true)
	case matches(`^(dot1x|mac-authentication) domain (\S+)$`):
		assign(keyName(words[0])+"_domain", last)
	case matches(`^portal (enable method|domain|bas-ip|apply web-server|apply mac-trigger-server) (\S+)$`):
		var value any = capture[2]
		if strings.Contains(capture[2], "://") {
			value = true
		}
		p.assign(ensureMap(record, "portal"), keyName(capture[1]), value, block)
	case matches(`^(?:undo )?(dot1x|mac-authentication)$`):
		assign(keyName(capture[1])+"_enabled", enabled)
	case matches(`^dot1x authentication-method (chap|eap|pap)$`):
		assign("dot1x_authentication_method", capture[1])
	case matches(`^mac-authentication user-name-format fixed(?: account \S+)?(?: password (simple|cipher) \S+)?$`):
		p.assignMany(record, "mac_username_setting", wireless.Record{"mac_authentication_user_name_format": "fixed", "mac_authentication_has_password": capture[1] != ""}, block)
	case matches(`^mac-authentication user-name-format (mac-address(?: (?:with-hyphen|without-hyphen))?(?: (?:lowercase|uppercase))?)$`):
		p.assignMany(record, "mac_username_setting", wireless.Record{"mac_authentication_user_name_format": capture[1]}, block)
	case matches(`^dot1x timer (supp-timeout|tx-period) (\d+)$`):
		v, e := parser.ParseIntegerExact(&last, nil)
		if e != nil {
			return false, e
		}
		p.assign(ensureMap(record, "dot1x_timers"), keyName(words[2]), recordvalue.IntegerJSON(v), block)
	case matches(`^wlan (?:client-security )?static-(black|white)list mac-address (\S+)(?: (.+))?$`):
		mac, e := parser.ParseMAC(&capture[2])
		if e != nil {
			return false, e
		}
		key := "whitelist"
		if capture[1] == "black" {
			key = "blacklist"
		}
		var reason any
		if capture[3] != "" {
			reason = capture[3]
		}
		p.data[key] = append(p.data[key].([]wireless.Record), wireless.Record{"mac_address": recordvalue.Scalar(mac), "reason": reason})
	case matches(`^(?:undo )?(?:wlan )?(auto-ap|auto-persistent|band-navigation|client inspect|radio-load-balance band-navigation) enable$`):
		assign(keyName(capture[1]), enabled)
	case matches(`^(?:undo )?(beacon ssid-hide|bss transition-management enable)$`):
		key := "btm_enabled"
		if strings.Contains(line, "ssid-hide") {
			key = "ssid_hidden"
		}
		assign(key, enabled)
	case matches(`^(client keep-alive|client-rate-limit|firmware-upgrade|radio) (enable|disable)$`):
		if words[0] == "radio" && kind != "radio" {
			return false, nil
		}
		key := map[string]string{"client keep-alive": "client_keep_alive", "client-rate-limit": "client_rate_limit_enabled", "firmware-upgrade": "firmware_upgrade", "radio": "enabled"}[capture[1]]
		assign(key, last == "enable")
	case matches(`^(client keep-alive interval|client max-count|client cache aging-time|bss transition-management disassociation forced timer|statistics-interval|priority|dot1x retry|wlan tcp mss) (\d+)$`):
		key := map[string]string{"client keep-alive interval": "client_keep_alive_interval", "client max-count": "client_max_count", "client cache aging-time": "client_cache_aging_time", "bss transition-management disassociation forced timer": "btm_disassociation_timer", "statistics-interval": "statistics_interval", "priority": "priority", "dot1x retry": "dot1x_retry", "wlan tcp mss": "tcp_mss"}[capture[1]]
		v, e := parser.ParseIntegerExact(&last, nil)
		if e != nil {
			return false, e
		}
		assign(key, recordvalue.IntegerJSON(v))
	case matches(`^(?:description|map-configuration) (.+)$`):
		assign(keyName(words[0]), strings.Join(words[1:], " "))
	case matches(`^backup-ac ip (\S+)$`):
		v, e := parser.ParseIP(&last)
		if e != nil {
			return false, e
		}
		assign("backup_ac_ip", recordvalue.Scalar(v))
	case matches(`^channel band-width (.+)$`):
		if kind != "radio" {
			return false, nil
		}
		assign("channel_bandwidth", capture[1])
	case matches(`^channel auto unlock$`):
		if kind != "radio" {
			return false, nil
		}
		p.assignMany(record, "channel_setting", wireless.Record{"channel": "auto", "channel_number": nil, "channel_unlocked": true}, block)
	case matches(`^channel (.+)$`):
		if kind != "radio" {
			return false, nil
		}
		v, e := parser.ParseChannel(&capture[1])
		if e != nil {
			return false, e
		}
		p.assignMany(record, "channel_setting", wireless.Record{"channel": capture[1], "channel_number": recordvalue.Scalar(v)}, block)
	case matches(`^max-power (\d+(?:\.\d+)?)$`):
		if kind != "radio" {
			return false, nil
		}
		v, e := parser.ParseNumber(&last, "")
		if e != nil {
			return false, e
		}
		assign("max_power", recordvalue.Scalar(v))
	case matches(`^client-rate-limit (inbound|outbound) mode (\S+) cir (\d+)$`):
		v, e := parser.ParseIntegerExact(&last, nil)
		if e != nil {
			return false, e
		}
		p.assign(ensureMap(record, "client_rate_limit"), words[1], wireless.Record{"mode": words[3], "cir_bps": recordvalue.IntegerJSON(new(big.Int).Mul(v, big.NewInt(1000)))}, block)
	case matches(`^rate (mandatory|supported|disabled|multicast) (.+)$`):
		var value any
		if words[1] == "multicast" {
			value = strings.Join(words[2:], " ")
		} else {
			values := []any{}
			for _, word := range words[2:] {
				v, e := parser.ParseNumber(&word, "")
				if e != nil {
					return false, e
				}
				values = append(values, recordvalue.Scalar(v))
			}
			value = values
		}
		p.assign(ensureMap(record, "rates"), words[1], value, block)
	case matches(`^option client (reject|reconnect) enable rssi (-?\d+(?:\.\d+)?)$`):
		v, e := parser.ParseNumber(&last, "")
		if e != nil {
			return false, e
		}
		p.assign(ensureMap(record, "client_options"), words[2], recordvalue.Scalar(v), block)
	case matches(`^sacp anti-sticky enable(?: rssi (-?\d+(?:\.\d+)?))? interval (\d+)$`):
		var raw *string
		if capture[1] != "" {
			raw = &capture[1]
		}
		signal, e := parser.ParseNumber(raw, "")
		if e != nil {
			return false, e
		}
		interval, e := parser.ParseIntegerExact(&capture[2], nil)
		if e != nil {
			return false, e
		}
		assign("anti_sticky", wireless.Record{"enabled": true, "rssi": recordvalue.Scalar(signal), "interval": recordvalue.IntegerJSON(interval)})
	case matches(`^packet-filter name (\S+) (inbound|outbound)$`):
		p.assign(ensureMap(record, "packet_filters"), last, words[2], block)
	case matches(`^(?:client forwarding-location|wlan image-load filepath|wlan fast-forwarding mode|led-mode) (\S+)$`):
		keyName := words[0]
		if words[0] == "wlan" {
			keyName = strings.Join(words[:2], " ")
		}
		key := map[string]string{"client": "forwarding_location", "wlan image-load": "image_load_filepath", "wlan fast-forwarding": "fast_forwarding_mode", "led-mode": "led_mode"}[keyName]
		assign(key, last)
	case matches(`^(local|member) ip (\S+)$`):
		v, e := parser.ParseIP(&last)
		if e != nil {
			return false, e
		}
		if words[0] == "local" {
			assign("local_ip", recordvalue.Scalar(v))
		} else {
			appendValue(record, "member_ips", recordvalue.Scalar(v))
		}
	case matches(`^client-mac (\S+)$`):
		v, e := parser.ParseMAC(&last)
		if e != nil {
			return false, e
		}
		appendValue(record, "mac_addresses", recordvalue.Scalar(v))
	case line == "wlan tunnel-preempt enable":
		assign("tunnel_preempt", true)
	case line == "broadcast-probe reply disable":
		assign("broadcast_probe_reply", false)
	case line == "radar-detect disable":
		assign("radar_detect_disabled", true)
	case line == "calibrate-channel self-decisive enable all":
		assign("calibrate_channel", true)
	case line == "calibrate-power self-decisive enable all":
		assign("calibrate_power", true)
	case line == "ap-license-synchronization enable":
		assign("license_synchronization", true)
	default:
		return false, nil
	}
	return true, nil
}
