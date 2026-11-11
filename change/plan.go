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
	"github.com/gatework/go-wireless/wlan"
)

var configDigest = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// Plan owns a validated configuration change. Its commands are rendered
// from the typed action and checked against the supplied document. Construction
// and accessors detach mutable data; the zero value cannot be executed.
type Plan struct {
	document           wireless.Record
	device             wireless.Record
	change             TemplateSecurity
	host, fingerprint  string
	commands, rollback []string
}

// NewPlan validates and copies a plan before credentials, an execution
// directory, or a device session is used. It preserves the document's JSON
// values, including exact json.Number representations.
func NewPlan(document wireless.Record) (Plan, error) {
	if document == nil {
		return Plan{}, ErrInvalid
	}
	value, err := wireless.CopyValue(document)
	if err != nil {
		return Plan{}, ErrInvalid
	}
	owned, _ := value.(wireless.Record) // CopyValue preserves the Record type
	if !schemaOne(owned["schema_version"]) || owned["platform"] != "h3c_wireless" {
		return Plan{}, ErrInvalid
	}
	device, ok := owned["device"].(wireless.Record)
	if !ok {
		return Plan{}, ErrInvalid
	}
	host, ok := device["host"].(string)
	if !ok {
		return Plan{}, ErrInvalid
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || ip.String() != host {
		return Plan{}, ErrInvalid
	}
	fingerprint, ok := owned["expected_config_sha256"].(string)
	if !ok || !configDigest.MatchString(fingerprint) {
		return Plan{}, ErrInvalid
	}
	change, err := DecodeTemplateSecurity(owned["change"])
	if err != nil {
		return Plan{}, err
	}
	script, err := change.Render()
	if err != nil {
		return Plan{}, err
	}
	commands, rollback := script.Commands, script.Rollback
	given, givenOK := stringList(owned["commands"])
	back, backOK := stringList(owned["rollback"])
	if !givenOK || !backOK || !slices.Equal(commands, given) || !slices.Equal(rollback, back) {
		return Plan{}, ErrInvalid
	}
	return Plan{document: owned, device: device, change: change, host: host, fingerprint: fingerprint, commands: commands, rollback: rollback}, nil
}

// Host returns the target device address.
func (p Plan) Host() string { return p.host }

// ExpectedConfigSHA256 returns the fingerprint of the configuration the plan
// was made against.
func (p Plan) ExpectedConfigSHA256() string { return p.fingerprint }

// Device returns a copy of the target device identity.
func (p Plan) Device() wireless.Record { return wireless.CloneRecord(p.device) }

// Commands returns a copy of the commands.
func (p Plan) Commands() []string { return slices.Clone(p.commands) }

// Rollback returns a copy of the rollback commands.
func (p Plan) Rollback() []string { return slices.Clone(p.rollback) }

// ToMap returns a copy of the plan document.
func (p Plan) ToMap() wireless.Record { return wireless.CloneRecord(p.document) }
func (p Plan) String() string {
	return fmt.Sprintf("change.Plan{commands:%d}", len(p.commands))
}

// GoString formats metadata only.
func (p Plan) GoString() string { return p.String() }

// MarshalJSON encodes the plan document.
func (p Plan) MarshalJSON() ([]byte, error) {
	if p.document == nil {
		return nil, ErrInvalid
	}
	return wireless.MarshalJSON(p.document)
}

// CheckBefore verifies the complete configuration fingerprint and the action's
// original settings before any command intent is journaled.
func (p Plan) CheckBefore(configuration wireless.Record) error {
	if p.document == nil {
		return ErrInvalid
	}
	fingerprint, err := wireless.Fingerprint(configuration)
	if err != nil || fingerprint != p.fingerprint {
		return ErrInvalid
	}
	typed, err := typedConfiguration(configuration)
	if err != nil || p.change.Eligible(typed) != nil {
		return ErrInvalid
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
		return nil, ErrInvalid
	}
	return &c, nil
}

// CheckAfter verifies the requested settings and fingerprints the complete
// readback. It does not assert business acceptance or saved configuration.
func (p Plan) CheckAfter(configuration wireless.Record) (string, error) {
	if p.document == nil {
		return "", ErrInvalid
	}
	typed, err := typedConfiguration(configuration)
	if err != nil || p.change.Satisfied(typed) != nil {
		return "", ErrInvalid
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

// stringList returns v as strings when it is a list of strings only.
func stringList(v any) ([]string, bool) {
	switch list := v.(type) {
	case []string:
		return list, true
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}
