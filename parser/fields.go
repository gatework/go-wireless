package parser

import (
	"errors"
	"math"
	"math/big"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
)

func rx(s string) *regexp.Regexp { return regexp.MustCompile(s) }

var missingValue = rx(`(?i)^(?:N/A|--|-|Not configured|Not supported)$`)

// NewlinePolicy selects the Ruby Fields.text newline conversion.
type NewlinePolicy string

const (
	PreserveNewlines NewlinePolicy = "preserve"
	LFNewlines       NewlinePolicy = "lf"
	DeleteCRNewlines NewlinePolicy = "delete_cr"
)

func NormalizeText(content string, preserveNewlines bool) (string, error) {
	policy := LFNewlines
	if preserveNewlines {
		policy = PreserveNewlines
	}
	return NormalizeTextWithPolicy(content, policy)
}
func NormalizeTextWithPolicy(content string, policy NewlinePolicy) (string, error) {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return "", errors.New("invalid wireless encoding")
	}
	switch policy {
	case PreserveNewlines:
		return content, nil
	case LFNewlines:
		return strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n"), nil
	case DeleteCRNewlines:
		return strings.ReplaceAll(content, "\r", ""), nil
	default:
		return "", errors.New("invalid wireless newline policy")
	}
}
func NormalizeValue(raw string) *string {
	s := parsetext.Strip(raw)
	if s == "" || missingValue.MatchString(s) {
		return nil
	}
	return &s
}

var macPattern = rx(`(?i)^(?:[0-9a-f]{12}|[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}|[0-9a-f]{4}\.[0-9a-f]{4}\.[0-9a-f]{4}|(?:[0-9a-f]{2}:){5}[0-9a-f]{2})$`)

func ParseMAC(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	if !macPattern.MatchString(*raw) {
		return nil, errors.New("invalid wireless MAC")
	}
	s := strings.ToLower(strings.NewReplacer("-", "", ".", "", ":", "").Replace(*raw))
	out := ""
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			out += ":"
		}
		out += s[i : i+2]
	}
	return &out, nil
}

var ipv4CompatibleIPv6String = rx(`^::[0-9a-f]{1,4}:[0-9a-f]{1,4}$`)

func ParseIP(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	if strings.ContainsAny(*raw, "/%") {
		return nil, errors.New("invalid wireless host address")
	}
	s := *raw
	bracketed := strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")
	if bracketed {
		s = s[1 : len(s)-1]
	}
	a, err := netip.ParseAddr(s)
	if err != nil || (bracketed && !a.Is6()) {
		return nil, errors.New("invalid wireless host address")
	}
	s = a.String()
	// IPAddr renders compatible IPv6 with two remaining groups as dotted IPv4.
	// Single-group forms (::, ::1, ::ffff) keep their compressed IPv6 spelling.
	if a.Is6() && ipv4CompatibleIPv6String.MatchString(s) {
		bytes := a.As16()
		s = "::" + netip.AddrFrom4([4]byte{bytes[12], bytes[13], bytes[14], bytes[15]}).String()
	}
	return &s, nil
}

func ParseInteger(raw *string, max *int64) (*int64, error) {
	var maximum *big.Int
	if max != nil {
		maximum = big.NewInt(*max)
	}
	value, err := ParseIntegerExact(raw, maximum)
	if err != nil || value == nil {
		return nil, err
	}
	if !value.IsInt64() {
		return nil, errors.New("wireless integer out of range")
	}
	result := value.Int64()
	return &result, nil
}
func ParseVLAN(raw *string) (*int64, error) {
	max := int64(4094)
	v, err := ParseInteger(raw, &max)
	if err == nil && v != nil && *v == 0 {
		return nil, errors.New("invalid wireless VLAN")
	}
	return v, err
}
func ParseNumber(raw *string, unit string) (*float64, error) {
	if raw == nil {
		return nil, nil
	}
	m := rx(`(?i)^(-?\d+(?:\.\d+)?)\s*(?:` + regexp.QuoteMeta(unit) + `)?$`).FindStringSubmatch(*raw)
	if m == nil {
		return nil, errors.New("invalid wireless number")
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, errors.New("invalid wireless number")
	}
	return &v, nil
}

var durationColon = rx(`^\d+:\d{2}:\d{2}:\d{2}$`)
var durationWords = rx(`(?i)(\d+)\s*(days?|hours?|minutes?|seconds?|[dhms])\b`)

func ParseDuration(raw *string) (*int64, error) {
	value, err := ParseDurationExact(raw)
	if err != nil || value == nil {
		return nil, err
	}
	if !value.IsInt64() {
		return nil, errors.New("wireless duration out of range")
	}
	result := value.Int64()
	return &result, nil
}

var ratesPattern = rx(`(?i)^(\d+(?:\.\d+)?)\s*/\s*(\d+(?:\.\d+)?)\s*([kmgt]?bps)?$`)

func ParseRatesMbps(raw *string) (*float64, *float64, error) {
	if raw == nil {
		return nil, nil, nil
	}
	m := ratesPattern.FindStringSubmatch(*raw)
	if m == nil {
		return nil, nil, errors.New("invalid wireless rates")
	}
	unit := strings.ToLower(m[3])
	if unit == "" {
		unit = "mbps"
	}
	multiplier := map[string]float64{"bps": .000001, "kbps": .001, "mbps": 1, "gbps": 1000, "tbps": 1000000}[unit]
	values := make([]float64, 2)
	for i := range values {
		v, err := strconv.ParseFloat(m[i+1], 64)
		v *= multiplier
		if err != nil || math.IsInf(v, 0) {
			return nil, nil, errors.New("invalid wireless rates")
		}
		values[i] = v
	}
	return &values[0], &values[1], nil
}

var channelPattern = rx(`^(\d+)(?:\s*\([^\n]*\))?$`)

func ParseChannel(raw *string) (*int64, error) {
	if raw == nil || strings.EqualFold(*raw, "auto") {
		return nil, nil
	}
	m := channelPattern.FindStringSubmatch(*raw)
	if m == nil {
		return nil, errors.New("invalid wireless channel")
	}
	max := int64(233)
	v, err := ParseInteger(&m[1], &max)
	if v != nil && *v == 0 {
		return nil, nil
	}
	return v, err
}

var widthPattern = rx(`(?i)^(\d+)\s*(?:MHz)?$`)

func ParseChannelWidth(raw *string) *int64 {
	if raw == nil {
		return nil
	}
	m := widthPattern.FindStringSubmatch(*raw)
	if m == nil {
		return nil
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}

var band6 = rx(`(?i)(?:^|[^\d.])6\s*g(?:hz)?\b`)
var band5 = rx(`(?i)(?:^|[^\d.])5\s*g(?:hz)?\b`)
var band24 = rx(`(?i)(?:^|[^\d.])2\.4\s*g(?:hz)?\b|802\.11[bg](?:\b|/)`)

func FrequencyBand(hint string) *string {
	bands := []string{}
	for _, b := range []struct {
		re *regexp.Regexp
		s  string
	}{{band6, "6GHz"}, {band5, "5GHz"}, {band24, "2.4GHz"}} {
		if b.re.MatchString(hint) {
			bands = append(bands, b.s)
		}
	}
	if len(bands) == 1 {
		return &bands[0]
	}
	return nil
}
func OnlineStatus(raw string) string {
	switch strings.ToLower(raw) {
	case "run", "running", "online", "normal", "nor", "up":
		return "online"
	case "idle", "fault", "offline", "down":
		return "offline"
	}
	return raw
}
