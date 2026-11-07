package wireless

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var configDigest = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// ChangePlan owns a validated configuration change. Its commands are rendered
// from the typed action and checked against the supplied document. Construction
// and accessors detach mutable data; the zero value cannot be executed.
type ChangePlan struct {
	document           Record
	device, change     Record
	host, fingerprint  string
	commands, rollback []string
}

// NewChangePlan validates and copies a plan before credentials, an execution
// directory, or a device session is used. It preserves the document's JSON
// values, including exact json.Number representations.
func NewChangePlan(document Record) (ChangePlan, error) {
	if document == nil {
		return ChangePlan{}, ErrChangePlan
	}
	value, err := copyJSON(document, make(map[visit]bool))
	if err != nil {
		return ChangePlan{}, ErrChangePlan
	}
	owned := value.(Record)
	if !schemaOne(owned["schema_version"]) || owned["platform"] != "h3c_wireless" {
		return ChangePlan{}, ErrChangePlan
	}
	device, ok := owned["device"].(Record)
	if !ok {
		return ChangePlan{}, ErrChangePlan
	}
	host, ok := device["host"].(string)
	if !ok {
		return ChangePlan{}, ErrChangePlan
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || ip.String() != host {
		return ChangePlan{}, ErrChangePlan
	}
	fingerprint, ok := owned["expected_config_sha256"].(string)
	if !ok || !configDigest.MatchString(fingerprint) {
		return ChangePlan{}, ErrChangePlan
	}
	change, ok := owned["change"].(Record)
	if !ok {
		return ChangePlan{}, ErrChangePlan
	}
	commands, rollback, err := RenderChange(change)
	if err != nil {
		return ChangePlan{}, err
	}
	given, givenOK := planStrings(owned["commands"])
	back, backOK := planStrings(owned["rollback"])
	if !givenOK || !backOK || !slices.Equal(commands, given) || !slices.Equal(rollback, back) {
		return ChangePlan{}, ErrChangePlan
	}
	return ChangePlan{document: owned, device: device, change: change, host: host, fingerprint: fingerprint, commands: commands, rollback: rollback}, nil
}

func (p ChangePlan) Host() string                 { return p.host }
func (p ChangePlan) ExpectedConfigSHA256() string { return p.fingerprint }
func (p ChangePlan) Device() Record               { return cloneRecord(p.device) }
func (p ChangePlan) Commands() []string           { return slices.Clone(p.commands) }
func (p ChangePlan) Rollback() []string           { return slices.Clone(p.rollback) }
func (p ChangePlan) ToMap() Record                { return cloneRecord(p.document) }
func (p ChangePlan) String() string {
	return fmt.Sprintf("wireless.ChangePlan{commands:%d}", len(p.commands))
}
func (p ChangePlan) GoString() string { return p.String() }
func (p ChangePlan) MarshalJSON() ([]byte, error) {
	if p.document == nil {
		return nil, ErrChangePlan
	}
	return MarshalJSON(p.document)
}

// CheckBefore verifies the complete configuration fingerprint and the action's
// original settings before any command intent is journaled.
func (p ChangePlan) CheckBefore(configuration Record) error {
	if p.document == nil {
		return ErrChangePlan
	}
	fingerprint, err := Fingerprint(configuration)
	if err != nil || fingerprint != p.fingerprint || !ChangeEligible(configuration, p.change) {
		return ErrChangePlan
	}
	return nil
}

// CheckAfter verifies the requested settings and fingerprints the complete
// readback. It does not assert business acceptance or saved configuration.
func (p ChangePlan) CheckAfter(configuration Record) (string, error) {
	if p.document == nil || !ChangeSatisfied(configuration, p.change) {
		return "", ErrChangePlan
	}
	return Fingerprint(configuration)
}

func schemaOne(value any) bool {
	if number, ok := value.(json.Number); ok {
		return decimalOne(string(number))
	}
	number := reflect.ValueOf(value)
	switch number.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return number.Int() == 1
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return number.Uint() == 1
	case reflect.Float32, reflect.Float64:
		return number.Float() == 1
	default:
		return false
	}
}

// decimalOne compares a JSON decimal exactly, without floating-point rounding or
// allocating an integer whose size is controlled by an untrusted exponent.
func decimalOne(number string) bool {
	if !jsonNumberPattern.MatchString(number) || strings.HasPrefix(number, "-") {
		return false
	}
	mantissa, exponent, hasExponent := strings.Cut(strings.ToLower(number), "e")
	if !hasExponent {
		exponent = "0"
	}
	decimal := strings.IndexByte(mantissa, '.')
	if decimal < 0 {
		decimal = len(mantissa)
	}
	digits := strings.ReplaceAll(mantissa, ".", "")
	significant := strings.TrimLeft(digits, "0")
	if significant == "" || significant[0] != '1' || strings.Trim(significant[1:], "0") != "" {
		return false
	}
	wanted := len(digits) - len(significant) - decimal + 1
	negative := strings.HasPrefix(exponent, "-")
	exponent = strings.TrimLeft(strings.TrimLeft(exponent, "+-"), "0")
	if exponent == "" {
		exponent = "0"
	} else if negative {
		exponent = "-" + exponent
	}
	return exponent == strconv.Itoa(wanted)
}
