package change

import (
	"errors"
	"reflect"
	"regexp"
	"slices"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/record"
)

var ErrChangePlan = errors.New("wireless: invalid change plan")
var templateName = regexp.MustCompile(`\A[A-Za-z0-9_.-]{1,63}\z`)

// RenderChange is the only source of applicable commands. Caller-supplied command
// strings are never accepted as instructions without exact re-render comparison.
func RenderChange(change wireless.Record) ([]string, []string, error) {
	name, ok := change["name"].(string)
	enabled, known := change["enabled"].(bool)
	ciphers, cok := record.Strings(change["cipher_suites"])
	ies, iok := record.Strings(change["security_ies"])
	if change["kind"] != "wireless_security" || !ok || !templateName.MatchString(name) || !known || !cok || !iok || !slices.Contains(ciphers, "ccmp") || !slices.Contains(ies, "rsn") || !(slices.Contains(ciphers, "tkip") || slices.Contains(ies, "wpa")) {
		return nil, nil, ErrChangePlan
	}
	remove, restore := []string{}, []string{}
	if slices.Contains(ies, "wpa") {
		remove = append(remove, " undo security-ie wpa")
		restore = append(restore, " security-ie wpa")
	}
	if slices.Contains(ciphers, "tkip") {
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

func planTemplate(config, change wireless.Record) wireless.Record {
	for _, t := range record.Records(config["service_templates"]) {
		if t["name"] == change["name"] {
			return t
		}
	}
	return nil
}
func ChangeEligible(config, change wireless.Record) bool {
	if _, _, e := RenderChange(change); e != nil {
		return false
	}
	t := planTemplate(config, change)
	if t == nil || t["enabled"] != change["enabled"] {
		return false
	}
	settings := record.Map(t["configuration"])
	for _, key := range []string{"cipher_suites", "security_ies"} {
		a, ok := record.Strings(settings[key])
		b, bok := record.Strings(change[key])
		if !ok || !bok || !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}
func ChangeSatisfied(config, change wireless.Record) bool {
	if _, _, e := RenderChange(change); e != nil {
		return false
	}
	t := planTemplate(config, change)
	if t == nil || t["enabled"] != change["enabled"] {
		return false
	}
	settings := record.Map(t["configuration"])
	for key, removed := range map[string]string{"cipher_suites": "tkip", "security_ies": "wpa"} {
		a, ok := record.Strings(settings[key])
		before, bok := record.Strings(change[key])
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
