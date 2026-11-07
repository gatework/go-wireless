package wireless

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrChangePlan = errors.New("wireless: invalid change plan")
var templateName = regexp.MustCompile(`\A[A-Za-z0-9_.-]{1,63}\z`)
var jsonNumberSyntax = regexp.MustCompile(`\A-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?\z`)

// CanonicalJSON follows Ruby JSON 3.1.0: sorted object keys, ordered arrays,
// exact integers, fpconv binary64 rendering, and unescaped UTF-8/HTML.
func CanonicalJSON(value any) ([]byte, error) {
	var b bytes.Buffer
	e := canonicalValue(&b, reflect.ValueOf(value), map[visit]bool{})
	return b.Bytes(), e
}
func Fingerprint(value any) (string, error) {
	b, e := CanonicalJSON(value)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func canonicalString(b *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return ErrChangePlan
	}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 32 {
				const hex = "0123456789abcdef"
				b.WriteString(`\u00`)
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&15])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return nil
}
func canonicalValue(b *bytes.Buffer, v reflect.Value, active map[visit]bool) error {
	if !v.IsValid() {
		b.WriteString("null")
		return nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			b.WriteString("null")
			return nil
		}
		return canonicalValue(b, v.Elem(), active)
	}
	if v.CanInterface() {
		switch x := v.Interface().(type) {
		case json.Number:
			s := string(x)
			if !jsonNumberSyntax.MatchString(s) {
				return ErrChangePlan
			}
			if strings.ContainsAny(s, ".eE") {
				f, e := strconv.ParseFloat(s, 64)
				if e != nil || math.IsInf(f, 0) || math.IsNaN(f) {
					return ErrChangePlan
				}
				b.WriteString(rubyFloat(f))
			} else {
				n, ok := new(big.Int).SetString(s, 10)
				if !ok {
					return ErrChangePlan
				}
				b.WriteString(n.String())
			}
			return nil
		case big.Int:
			b.WriteString(x.String())
			return nil
		case *big.Int:
			if x == nil {
				b.WriteString("null")
			} else {
				b.WriteString(x.String())
			}
			return nil
		}
	}
	switch v.Kind() {
	case reflect.Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.String:
		return canonicalString(b, v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return ErrChangePlan
		}
		b.WriteString(rubyFloat(f))
	case reflect.Map, reflect.Slice, reflect.Array:
		if v.Kind() != reflect.Array {
			if v.IsNil() {
				b.WriteString("null")
				return nil
			}
			key := visit{kind: v.Kind(), ptr: v.Pointer()}
			if active[key] {
				return ErrChangePlan
			}
			active[key] = true
			defer delete(active, key)
		}
		if v.Kind() == reflect.Map {
			if v.Type().Key().Kind() != reflect.String {
				return ErrChangePlan
			}
			keys := v.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			b.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					b.WriteByte(',')
				}
				if e := canonicalString(b, k.String()); e != nil {
					return e
				}
				b.WriteByte(':')
				if e := canonicalValue(b, v.MapIndex(k), active); e != nil {
					return e
				}
			}
			b.WriteByte('}')
		} else {
			b.WriteByte('[')
			for i := 0; i < v.Len(); i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				if e := canonicalValue(b, v.Index(i), active); e != nil {
					return e
				}
			}
			b.WriteByte(']')
		}
	default:
		return ErrChangePlan
	}
	return nil
}

// RenderChange is the only source of applicable commands. Caller-supplied command
// strings are never accepted as instructions without exact re-render comparison.
func RenderChange(change Record) ([]string, []string, error) {
	name, ok := change["name"].(string)
	enabled, known := change["enabled"].(bool)
	ciphers, cok := planStrings(change["cipher_suites"])
	ies, iok := planStrings(change["security_ies"])
	if change["kind"] != "wireless_security" || !ok || !templateName.MatchString(name) || !known || !cok || !iok || !hasString(ciphers, "ccmp") || !hasString(ies, "rsn") || !(hasString(ciphers, "tkip") || hasString(ies, "wpa")) {
		return nil, nil, ErrChangePlan
	}
	remove, restore := []string{}, []string{}
	if hasString(ies, "wpa") {
		remove = append(remove, " undo security-ie wpa")
		restore = append(restore, " security-ie wpa")
	}
	if hasString(ciphers, "tkip") {
		remove = append(remove, " undo cipher-suite tkip")
		restore = append([]string{" cipher-suite tkip"}, restore...)
	}
	wrap := func(a []string) []string {
		r := []string{"system-view", "wlan service-template " + name, " undo service-template enable"}
		r = append(r, a...)
		if enabled {
			r = append(r, " service-template enable")
		}
		return append(r, "quit", "return")
	}
	return wrap(remove), wrap(restore), nil
}
func planStrings(v any) ([]string, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, false
	}
	r := []string{}
	for i := 0; i < rv.Len(); i++ {
		s, ok := rv.Index(i).Interface().(string)
		if !ok {
			return nil, false
		}
		r = append(r, s)
	}
	return r, true
}
func hasString(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
func planTemplate(config, change Record) Record {
	for _, t := range recordList(config["service_templates"]) {
		if t["name"] == change["name"] {
			return t
		}
	}
	return nil
}
func ChangeEligible(config, change Record) bool {
	if _, _, e := RenderChange(change); e != nil {
		return false
	}
	t := planTemplate(config, change)
	if t == nil || t["enabled"] != change["enabled"] {
		return false
	}
	settings := asRecord(t["configuration"])
	for _, key := range []string{"cipher_suites", "security_ies"} {
		a, ok := planStrings(settings[key])
		b, bok := planStrings(change[key])
		if !ok || !bok || !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}
func ChangeSatisfied(config, change Record) bool {
	if _, _, e := RenderChange(change); e != nil {
		return false
	}
	t := planTemplate(config, change)
	if t == nil || t["enabled"] != change["enabled"] {
		return false
	}
	settings := asRecord(t["configuration"])
	for key, removed := range map[string]string{"cipher_suites": "tkip", "security_ies": "wpa"} {
		a, ok := planStrings(settings[key])
		before, bok := planStrings(change[key])
		expected := []string{}
		for _, s := range before {
			if s != removed {
				expected = append(expected, s)
			}
		}
		if !ok || !bok || !reflect.DeepEqual(a, expected) {
			return false
		}
	}
	return true
}
