package wireless

import (
	"context"
	"encoding/json"
	"net/netip"
	"reflect"
	"regexp"
)

// ApplySession is borrowed for one owned operation. Implementations must not
// reconnect, retry, save configuration or automatically roll back. Reads must
// return exactly one complete parsed configuration, never partial observations.
type ApplySession interface {
	ReadConfiguration(context.Context) (Record, error)
	Write(context.Context, string) error
}

var configDigest = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// ValidateChangePlan re-renders the sole supported typed action before any
// credentials, execution directory or connection is used.
func ValidateChangePlan(plan Record) error {
	schema := false
	switch v := plan["schema_version"].(type) {
	case json.Number:
		f, e := v.Float64()
		schema = e == nil && f == 1
	case int:
		schema = v == 1
	case float64:
		schema = v == 1
	}
	if !schema || plan["platform"] != "h3c_wireless" {
		return ErrChangePlan
	}
	device, ok := plan["device"].(Record)
	if !ok {
		return ErrChangePlan
	}
	host, ok := device["host"].(string)
	if !ok {
		return ErrChangePlan
	}
	ip, e := netip.ParseAddr(host)
	if e != nil || ip.Zone() != "" || ip.String() != host {
		return ErrChangePlan
	}
	fingerprint, ok := plan["expected_config_sha256"].(string)
	if !ok || !configDigest.MatchString(fingerprint) {
		return ErrChangePlan
	}
	change, ok := plan["change"].(Record)
	if !ok {
		return ErrChangePlan
	}
	commands, rollback, e := RenderChange(change)
	if e != nil {
		return e
	}
	given, gok := planStrings(plan["commands"])
	back, bok := planStrings(plan["rollback"])
	if !gok || !bok || !reflect.DeepEqual(commands, given) || !reflect.DeepEqual(rollback, back) {
		return ErrChangePlan
	}
	return nil
}
