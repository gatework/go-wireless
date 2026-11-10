package change

import (
	"errors"
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
