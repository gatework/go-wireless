package h3c

import (
	"errors"
	"math/big"
	"regexp"
	"slices"
	"strings"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
	recordvalue "github.com/gatework/go-wireless/internal/record"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

// settingLine is one configuration line inside a scope, with what the
// setting rules need to record it.
type settingLine struct {
	parser  *configurationParser
	block   *sourceBlock
	line    string
	words   []string
	last    string
	capture []string
	enabled bool // false for an `undo` line
	kind    string
	owner   wireless.Record // the scope's own record
	record  wireless.Record // where the scope keeps its settings
}

func (l *settingLine) assign(key string, value any) { l.parser.assign(l.record, key, value, l.block) }

func (l *settingLine) assignTo(record wireless.Record, key string, value any) {
	l.parser.assign(record, key, value, l.block)
}

// setting recognizes one configuration line form. A line matching pattern in
// a scope outside kinds (when kinds is set) is not a setting. apply reports
// whether the line was recorded.
type setting struct {
	pattern *regexp.Regexp
	kinds   []string
	apply   func(l *settingLine) (bool, error)
}

func rule(pattern string, kinds []string, apply func(l *settingLine) (bool, error)) setting {
	return setting{pattern: regexp.MustCompile(pattern), kinds: kinds, apply: apply}
}

// exact recognizes one literal line.
func exact(line string, apply func(l *settingLine) (bool, error)) setting {
	return rule(`^`+regexp.QuoteMeta(line)+`$`, nil, apply)
}

// recorded adapts a setting that always records its line.
func recorded(fn func(l *settingLine)) func(*settingLine) (bool, error) {
	return func(l *settingLine) (bool, error) { fn(l); return true, nil }
}

// parsed adapts a setting whose value parsing can fail.
func parsed(fn func(l *settingLine) error) func(*settingLine) (bool, error) {
	return func(l *settingLine) (bool, error) {
		if err := fn(l); err != nil {
			return false, err
		}
		return true, nil
	}
}

// optional returns s, or nil when s is empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var (
	global          = []string{"global_configuration"}
	apGroups        = []string{"ap_groups"}
	serviceTemplate = []string{"service_templates"}
	radios          = []string{"radio"}
)

var clientAuthenticationModes = []string{"dot1x", "mac", "mac-and-dot1x", "dot1x-then-mac", "mac-then-dot1x", "oui-then-dot1x"}

// settings are tried in order; the first matching pattern decides.
var settings = []setting{
	rule(`^(?:undo )?region-code-lock enable$`, global, recorded(func(l *settingLine) { l.assign("region_code_lock", l.enabled) })),
	rule(`^nas-id (\S+)$`, global, recorded(func(l *settingLine) { l.assign("nas_id", l.capture[1]) })),
	rule(`^(?:undo )?rrop anti-bmc network (ipv4-and-ipv6-simple) enable$`, apGroups, recorded(func(l *settingLine) {
		l.assignTo(ensureMap(l.record, "rrop_anti_bmc"), "network", wireless.Record{"mode": l.capture[1], "enabled": l.enabled})
	})),
	rule(`^rrop anti-bmc default-action (deny|permit)$`, apGroups, recorded(func(l *settingLine) {
		l.assignTo(ensureMap(l.record, "rrop_anti_bmc"), "default_action", l.capture[1])
	})),
	rule(`^serial-id (\S+)$`, []string{"access_points", "ap_groups"}, recorded(func(l *settingLine) {
		if l.kind == "ap_groups" {
			appendValue(l.owner, "serial_ids", l.last)
		} else {
			l.assign("serial_id", l.last)
		}
	})),
	rule(`^ap (\S+)$`, apGroups, recorded(func(l *settingLine) { appendValue(l.owner, "ap_names", l.last) })),
	rule(`^vlan (\d+)$`, nil, parsed(func(l *settingLine) error {
		v, err := parser.ParseVLAN(&l.last)
		if err != nil {
			return err
		}
		if l.kind == "ap_groups" {
			appendValue(l.owner, "vlan_ids", recordvalue.Scalar(v))
		} else {
			l.assignTo(l.owner, "vlan_id", recordvalue.Scalar(v))
		}
		return nil
	})),
	rule(`^ssid (.+)$`, serviceTemplate, recorded(func(l *settingLine) { l.assignTo(l.owner, "ssid", l.capture[1]) })),
	rule(`^(?:undo )?service-template enable$`, serviceTemplate, recorded(func(l *settingLine) { l.assignTo(l.owner, "enabled", l.enabled) })),
	rule(`^service-template (\S+)(?: vlan (\d+))?$`, radios, parsed(func(l *settingLine) error {
		v, err := parser.ParseVLAN(optional(l.capture[2]))
		if err != nil {
			return err
		}
		return l.parser.bindTemplate(l.record, l.capture[1], recordvalue.Scalar(v))
	})),
	rule(`(?i)^preshared-key\s+.+$`, serviceTemplate, recorded(func(l *settingLine) { l.assign("has_preshared_key", true) })),
	rule(`^akm mode (.+)$`, serviceTemplate, recorded(func(l *settingLine) {
		l.assign("akm_modes", parsetext.SplitWords(strings.ReplaceAll(strings.ToLower(l.capture[1]), "802.1x", "dot1x")))
	})),
	rule(`^(cipher-suite|security-ie) (.+)$`, serviceTemplate, recorded(func(l *settingLine) {
		key := "security_ies"
		if l.words[0] == "cipher-suite" {
			key = "cipher_suites"
		}
		for _, word := range l.words[1:] {
			word = strings.ToLower(word)
			if !arrayIncludes(l.record[key], word) {
				appendValue(l.record, key, word)
			}
		}
	})),
	rule(`^client-security authentication-mode (.+)$`, nil, parsed(func(l *settingLine) error {
		mode := strings.ToLower(l.capture[1])
		if !slices.Contains(clientAuthenticationModes, mode) {
			return errors.New("invalid client authentication mode")
		}
		l.assign("client_security_authentication_mode", mode)
		return nil
	})),
	rule(`^client-security (ignore-authentication|ignore-authorization)$`, nil, recorded(func(l *settingLine) { l.assign(keyName(l.last), true) })),
	rule(`^(dot1x|mac-authentication) domain (\S+)$`, nil, recorded(func(l *settingLine) { l.assign(keyName(l.words[0])+"_domain", l.last) })),
	rule(`^portal (enable method|domain|bas-ip|apply web-server|apply mac-trigger-server) (\S+)$`, nil, recorded(func(l *settingLine) {
		var value any = l.capture[2]
		if strings.Contains(l.capture[2], "://") {
			value = true // a server URL may carry credentials; record only its presence
		}
		l.assignTo(ensureMap(l.record, "portal"), keyName(l.capture[1]), value)
	})),
	rule(`^(?:undo )?(dot1x|mac-authentication)$`, nil, recorded(func(l *settingLine) { l.assign(keyName(l.capture[1])+"_enabled", l.enabled) })),
	rule(`^dot1x authentication-method (chap|eap|pap)$`, nil, recorded(func(l *settingLine) { l.assign("dot1x_authentication_method", l.capture[1]) })),
	rule(`^mac-authentication user-name-format fixed(?: account \S+)?(?: password (simple|cipher) \S+)?$`, nil, recorded(func(l *settingLine) {
		l.parser.assignMany(l.record, "mac_username_setting", wireless.Record{"mac_authentication_user_name_format": "fixed", "mac_authentication_has_password": l.capture[1] != ""}, l.block)
	})),
	rule(`^mac-authentication user-name-format (mac-address(?: (?:with-hyphen|without-hyphen))?(?: (?:lowercase|uppercase))?)$`, nil, recorded(func(l *settingLine) {
		l.parser.assignMany(l.record, "mac_username_setting", wireless.Record{"mac_authentication_user_name_format": l.capture[1]}, l.block)
	})),
	rule(`^dot1x timer (supp-timeout|tx-period) (\d+)$`, nil, parsed(func(l *settingLine) error {
		v, err := parser.ParseIntegerExact(&l.last, nil)
		if err != nil {
			return err
		}
		l.assignTo(ensureMap(l.record, "dot1x_timers"), keyName(l.words[2]), recordvalue.IntegerJSON(v))
		return nil
	})),
	rule(`^wlan (?:client-security )?static-(black|white)list mac-address (\S+)(?: (.+))?$`, nil, parsed(staticMACList)),
	rule(`^(?:undo )?(?:wlan )?(auto-ap|auto-persistent|band-navigation|client inspect|radio-load-balance band-navigation) enable$`, nil, recorded(func(l *settingLine) {
		l.assign(keyName(l.capture[1]), l.enabled)
	})),
	rule(`^(?:undo )?(beacon ssid-hide|bss transition-management enable)$`, nil, recorded(func(l *settingLine) {
		key := "btm_enabled"
		if strings.Contains(l.line, "ssid-hide") {
			key = "ssid_hidden"
		}
		l.assign(key, l.enabled)
	})),
	rule(`^(client keep-alive|client-rate-limit|firmware-upgrade|radio) (enable|disable)$`, nil, func(l *settingLine) (bool, error) {
		if l.words[0] == "radio" && l.kind != "radio" {
			return false, nil
		}
		key := map[string]string{"client keep-alive": "client_keep_alive", "client-rate-limit": "client_rate_limit_enabled", "firmware-upgrade": "firmware_upgrade", "radio": "enabled"}[l.capture[1]]
		l.assign(key, l.last == "enable")
		return true, nil
	}),
	rule(`^(client keep-alive interval|client max-count|client cache aging-time|bss transition-management disassociation forced timer|statistics-interval|priority|dot1x retry|wlan tcp mss) (\d+)$`, nil, parsed(func(l *settingLine) error {
		key := map[string]string{"client keep-alive interval": "client_keep_alive_interval", "client max-count": "client_max_count", "client cache aging-time": "client_cache_aging_time", "bss transition-management disassociation forced timer": "btm_disassociation_timer", "statistics-interval": "statistics_interval", "priority": "priority", "dot1x retry": "dot1x_retry", "wlan tcp mss": "tcp_mss"}[l.capture[1]]
		v, err := parser.ParseIntegerExact(&l.last, nil)
		if err != nil {
			return err
		}
		l.assign(key, recordvalue.IntegerJSON(v))
		return nil
	})),
	rule(`^(?:description|map-configuration) (.+)$`, nil, recorded(func(l *settingLine) { l.assign(keyName(l.words[0]), strings.Join(l.words[1:], " ")) })),
	rule(`^backup-ac ip (\S+)$`, nil, parsed(func(l *settingLine) error {
		v, err := parser.ParseIP(&l.last)
		if err != nil {
			return err
		}
		l.assign("backup_ac_ip", recordvalue.Scalar(v))
		return nil
	})),
	rule(`^channel band-width (.+)$`, radios, recorded(func(l *settingLine) { l.assign("channel_bandwidth", l.capture[1]) })),
	rule(`^channel auto unlock$`, radios, recorded(func(l *settingLine) {
		l.parser.assignMany(l.record, "channel_setting", wireless.Record{"channel": "auto", "channel_number": nil, "channel_unlocked": true}, l.block)
	})),
	rule(`^channel (.+)$`, radios, parsed(func(l *settingLine) error {
		v, err := parser.ParseChannel(&l.capture[1])
		if err != nil {
			return err
		}
		l.parser.assignMany(l.record, "channel_setting", wireless.Record{"channel": l.capture[1], "channel_number": recordvalue.Scalar(v)}, l.block)
		return nil
	})),
	rule(`^max-power (\d+(?:\.\d+)?)$`, radios, parsed(func(l *settingLine) error {
		v, err := parser.ParseNumber(&l.last, "")
		if err != nil {
			return err
		}
		l.assign("max_power", recordvalue.Scalar(v))
		return nil
	})),
	rule(`^client-rate-limit (inbound|outbound) mode (\S+) cir (\d+)$`, nil, parsed(func(l *settingLine) error {
		v, err := parser.ParseIntegerExact(&l.last, nil)
		if err != nil {
			return err
		}
		l.assignTo(ensureMap(l.record, "client_rate_limit"), l.words[1], wireless.Record{"mode": l.words[3], "cir_bps": recordvalue.IntegerJSON(new(big.Int).Mul(v, big.NewInt(1000)))})
		return nil
	})),
	rule(`^rate (mandatory|supported|disabled|multicast) (.+)$`, nil, parsed(rateSetting)),
	rule(`^option client (reject|reconnect) enable rssi (-?\d+(?:\.\d+)?)$`, nil, parsed(func(l *settingLine) error {
		v, err := parser.ParseNumber(&l.last, "")
		if err != nil {
			return err
		}
		l.assignTo(ensureMap(l.record, "client_options"), l.words[2], recordvalue.Scalar(v))
		return nil
	})),
	rule(`^sacp anti-sticky enable(?: rssi (-?\d+(?:\.\d+)?))? interval (\d+)$`, nil, parsed(func(l *settingLine) error {
		signal, err := parser.ParseNumber(optional(l.capture[1]), "")
		if err != nil {
			return err
		}
		interval, err := parser.ParseIntegerExact(&l.capture[2], nil)
		if err != nil {
			return err
		}
		l.assign("anti_sticky", wireless.Record{"enabled": true, "rssi": recordvalue.Scalar(signal), "interval": recordvalue.IntegerJSON(interval)})
		return nil
	})),
	rule(`^packet-filter name (\S+) (inbound|outbound)$`, nil, recorded(func(l *settingLine) {
		l.assignTo(ensureMap(l.record, "packet_filters"), l.last, l.words[2])
	})),
	rule(`^(?:client forwarding-location|wlan image-load filepath|wlan fast-forwarding mode|led-mode) (\S+)$`, nil, recorded(func(l *settingLine) {
		name := l.words[0]
		if name == "wlan" {
			name = strings.Join(l.words[:2], " ")
		}
		key := map[string]string{"client": "forwarding_location", "wlan image-load": "image_load_filepath", "wlan fast-forwarding": "fast_forwarding_mode", "led-mode": "led_mode"}[name]
		l.assign(key, l.last)
	})),
	rule(`^(local|member) ip (\S+)$`, nil, parsed(func(l *settingLine) error {
		v, err := parser.ParseIP(&l.last)
		if err != nil {
			return err
		}
		if l.words[0] == "local" {
			l.assign("local_ip", recordvalue.Scalar(v))
		} else {
			appendValue(l.record, "member_ips", recordvalue.Scalar(v))
		}
		return nil
	})),
	rule(`^client-mac (\S+)$`, nil, parsed(func(l *settingLine) error {
		v, err := parser.ParseMAC(&l.last)
		if err != nil {
			return err
		}
		appendValue(l.record, "mac_addresses", recordvalue.Scalar(v))
		return nil
	})),
	exact("wlan tunnel-preempt enable", recorded(func(l *settingLine) { l.assign("tunnel_preempt", true) })),
	exact("broadcast-probe reply disable", recorded(func(l *settingLine) { l.assign("broadcast_probe_reply", false) })),
	exact("radar-detect disable", recorded(func(l *settingLine) { l.assign("radar_detect_disabled", true) })),
	exact("calibrate-channel self-decisive enable all", recorded(func(l *settingLine) { l.assign("calibrate_channel", true) })),
	exact("calibrate-power self-decisive enable all", recorded(func(l *settingLine) { l.assign("calibrate_power", true) })),
	exact("ap-license-synchronization enable", recorded(func(l *settingLine) { l.assign("license_synchronization", true) })),
}

// staticMACList records a static black- or whitelist entry.
func staticMACList(l *settingLine) error {
	mac, err := parser.ParseMAC(&l.capture[2])
	if err != nil {
		return err
	}
	key := "whitelist"
	if l.capture[1] == "black" {
		key = "blacklist"
	}
	var reason any
	if l.capture[3] != "" {
		reason = l.capture[3]
	}
	rows, _ := l.parser.data[key].([]wireless.Record)
	l.parser.data[key] = append(rows, wireless.Record{"mac_address": recordvalue.Scalar(mac), "reason": reason})
	return nil
}

// rateSetting records a rate set: multicast as text, the others as numbers.
func rateSetting(l *settingLine) error {
	var value any
	if l.words[1] == "multicast" {
		value = strings.Join(l.words[2:], " ")
	} else {
		values := []any{}
		for _, word := range l.words[2:] {
			v, err := parser.ParseNumber(&word, "")
			if err != nil {
				return err
			}
			values = append(values, recordvalue.Scalar(v))
		}
		value = values
	}
	l.assignTo(ensureMap(l.record, "rates"), l.words[1], value)
	return nil
}

// applySetting records one configuration line of scope; it reports false for
// a line that is not a known setting of the scope.
func (p *configurationParser) applySetting(scope *configurationScope, block *sourceBlock) (bool, error) {
	line := p.source.lines[block.index].text
	record := scope.settings()
	words := parsetext.SplitWords(line)
	for _, s := range settings {
		capture := s.pattern.FindStringSubmatch(line)
		if capture == nil {
			continue
		}
		if len(s.kinds) > 0 && !slices.Contains(s.kinds, scope.kind) {
			return false, nil
		}
		return s.apply(&settingLine{parser: p, block: block, line: line, words: words, last: words[len(words)-1], capture: capture,
			enabled: !strings.HasPrefix(line, "undo "), kind: scope.kind, owner: scope.record, record: record})
	}
	return false, nil
}
