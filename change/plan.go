package change

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/wlan"
)

var configDigest = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// ChangePlan owns a validated configuration change. Its commands are rendered
// from the typed action and checked against the supplied document. Construction
// and accessors detach mutable data; the zero value cannot be executed.
type ChangePlan struct {
	document           wireless.Record
	device             wireless.Record
	change             TemplateSecurity
	host, fingerprint  string
	commands, rollback []string
}

// NewChangePlan validates and copies a plan before credentials, an execution
// directory, or a device session is used. It preserves the document's JSON
// values, including exact json.Number representations.
func NewChangePlan(document wireless.Record) (ChangePlan, error) {
	if document == nil {
		return ChangePlan{}, ErrChangePlan
	}
	value, err := wireless.CopyValue(document)
	if err != nil {
		return ChangePlan{}, ErrChangePlan
	}
	owned := value.(wireless.Record)
	if !schemaOne(owned["schema_version"]) || owned["platform"] != "h3c_wireless" {
		return ChangePlan{}, ErrChangePlan
	}
	device, ok := owned["device"].(wireless.Record)
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
	change, err := DecodeTemplateSecurity(owned["change"])
	if err != nil {
		return ChangePlan{}, err
	}
	script, err := change.Render()
	if err != nil {
		return ChangePlan{}, err
	}
	commands, rollback := script.Commands, script.Rollback
	given, givenOK := record.Strings(owned["commands"])
	back, backOK := record.Strings(owned["rollback"])
	if !givenOK || !backOK || !slices.Equal(commands, given) || !slices.Equal(rollback, back) {
		return ChangePlan{}, ErrChangePlan
	}
	return ChangePlan{document: owned, device: device, change: change, host: host, fingerprint: fingerprint, commands: commands, rollback: rollback}, nil
}

func (p ChangePlan) Host() string                 { return p.host }
func (p ChangePlan) ExpectedConfigSHA256() string { return p.fingerprint }
func (p ChangePlan) Device() wireless.Record      { return wireless.CloneRecord(p.device) }
func (p ChangePlan) Commands() []string           { return slices.Clone(p.commands) }
func (p ChangePlan) Rollback() []string           { return slices.Clone(p.rollback) }
func (p ChangePlan) ToMap() wireless.Record       { return wireless.CloneRecord(p.document) }
func (p ChangePlan) String() string {
	return fmt.Sprintf("wireless.ChangePlan{commands:%d}", len(p.commands))
}
func (p ChangePlan) GoString() string { return p.String() }
func (p ChangePlan) MarshalJSON() ([]byte, error) {
	if p.document == nil {
		return nil, ErrChangePlan
	}
	return wireless.MarshalJSON(p.document)
}

// CheckBefore verifies the complete configuration fingerprint and the action's
// original settings before any command intent is journaled.
func (p ChangePlan) CheckBefore(configuration wireless.Record) error {
	if p.document == nil {
		return ErrChangePlan
	}
	fingerprint, err := wireless.Fingerprint(configuration)
	if err != nil || fingerprint != p.fingerprint {
		return ErrChangePlan
	}
	typed, err := typedConfiguration(configuration)
	if err != nil || p.change.Eligible(typed) != nil {
		return ErrChangePlan
	}
	return nil
}

// typedConfiguration strictly decodes a configuration record; unknown fields
// are rejected so unmodeled facts cannot authorize or verify a change.
func typedConfiguration(configuration wireless.Record) (*wlan.Configuration, error) {
	body, err := wireless.MarshalJSON(configuration)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var c wlan.Configuration
	if err := d.Decode(&c); err != nil {
		return nil, ErrChangePlan
	}
	return &c, nil
}

// CheckAfter verifies the requested settings and fingerprints the complete
// readback. It does not assert business acceptance or saved configuration.
func (p ChangePlan) CheckAfter(configuration wireless.Record) (string, error) {
	if p.document == nil {
		return "", ErrChangePlan
	}
	typed, err := typedConfiguration(configuration)
	if err != nil || p.change.Satisfied(typed) != nil {
		return "", ErrChangePlan
	}
	return wireless.Fingerprint(configuration)
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
	if !wireless.ValidJSONNumber(number) || strings.HasPrefix(number, "-") {
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
