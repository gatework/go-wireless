package change

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"

	netchange "github.com/gatework/go-netops/change"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/wlan"
)

// KindTemplateSecurity removes legacy TKIP/WPA from one service template.
const KindTemplateSecurity netchange.Kind = "wireless_security"

// ErrInvalid reports a change or plan that is invalid or no longer applies.
var ErrInvalid = errors.New("wireless: invalid change plan")

// templateName is the set of H3C service template names a plan may target.
var templateName = regexp.MustCompile(`\A[A-Za-z0-9_.-]{1,63}\z`)

// TemplateSecurity is the wireless_security change: it removes legacy TKIP
// and/or WPA from one H3C service template while keeping CCMP and RSN.
type TemplateSecurity struct {
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	CipherSuites []string `json:"cipher_suites"`
	SecurityIEs  []string `json:"security_ies"`
}

var _ netchange.Change[*wlan.Configuration] = TemplateSecurity{}

// Kind returns KindTemplateSecurity.
func (TemplateSecurity) Kind() netchange.Kind { return KindTemplateSecurity }

// Validate requires a legal name, CCMP and RSN to remain, and something to remove.
func (t TemplateSecurity) Validate() error {
	if !templateName.MatchString(t.Name) || !slices.Contains(t.CipherSuites, "ccmp") || !slices.Contains(t.SecurityIEs, "rsn") ||
		(!slices.Contains(t.CipherSuites, "tkip") && !slices.Contains(t.SecurityIEs, "wpa")) {
		return ErrInvalid
	}
	return nil
}

// Render returns the H3C commands and their rollback. The template is
// disabled while its security settings change and then restored to its
// planned enabled state; this is the only source of applicable commands.
func (t TemplateSecurity) Render() (netchange.Script, error) {
	if err := t.Validate(); err != nil {
		return netchange.Script{}, err
	}
	var remove, restore []string
	if slices.Contains(t.SecurityIEs, "wpa") {
		remove = append(remove, " undo security-ie wpa")
		restore = append(restore, " security-ie wpa")
	}
	if slices.Contains(t.CipherSuites, "tkip") {
		remove = append(remove, " undo cipher-suite tkip")
		restore = append([]string{" cipher-suite tkip"}, restore...)
	}
	return netchange.Script{Commands: t.wrap(remove), Rollback: t.wrap(restore)}, nil
}

// Eligible requires the template to still have the planned state.
func (t TemplateSecurity) Eligible(c *wlan.Configuration) error {
	tpl, err := t.template(c)
	if err != nil {
		return err
	}
	if tpl.Enabled == nil || *tpl.Enabled != t.Enabled ||
		!slices.Equal(tpl.Configuration.CipherSuites, t.CipherSuites) || !slices.Equal(tpl.Configuration.SecurityIEs, t.SecurityIEs) {
		return errors.New("service template changed since planning")
	}
	return nil
}

// Satisfied requires TKIP and WPA removed and everything else unchanged.
func (t TemplateSecurity) Satisfied(c *wlan.Configuration) error {
	tpl, err := t.template(c)
	if err != nil {
		return err
	}
	without := func(list []string, removed string) []string {
		return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == removed })
	}
	if tpl.Enabled == nil || *tpl.Enabled != t.Enabled ||
		!slices.Equal(nonNil(tpl.Configuration.CipherSuites), nonNil(without(t.CipherSuites, "tkip"))) ||
		!slices.Equal(nonNil(tpl.Configuration.SecurityIEs), nonNil(without(t.SecurityIEs, "wpa"))) {
		return errors.New("readback does not show TKIP/WPA removed")
	}
	return nil
}

// wrap places settings inside the template view, disabling the template
// first and restoring its enabled state last.
func (t TemplateSecurity) wrap(settings []string) []string {
	out := append([]string{"system-view", "wlan service-template " + t.Name, " undo service-template enable"}, settings...)
	if t.Enabled {
		out = append(out, " service-template enable")
	}
	return append(out, "quit", "return")
}

func (t TemplateSecurity) template(c *wlan.Configuration) (*wlan.ServiceTemplate, error) {
	if c == nil {
		return nil, errors.New("no configuration")
	}
	for i := range c.ServiceTemplates {
		if c.ServiceTemplates[i].Name == t.Name {
			return &c.ServiceTemplates[i], nil
		}
	}
	return nil, errors.New("service template not found")
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// DecodeTemplateSecurity strictly decodes a persisted wireless_security
// change record; any other kind or extra field is rejected.
func DecodeTemplateSecurity(v any) (TemplateSecurity, error) {
	body, err := wireless.MarshalJSON(v)
	if err != nil {
		return TemplateSecurity{}, ErrInvalid
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return TemplateSecurity{}, ErrInvalid
	}
	var kind string
	if json.Unmarshal(raw["kind"], &kind) != nil || netchange.Kind(kind) != KindTemplateSecurity {
		return TemplateSecurity{}, ErrInvalid
	}
	delete(raw, "kind")
	for _, required := range []string{"name", "enabled", "cipher_suites", "security_ies"} {
		if v, ok := raw[required]; !ok || string(bytes.TrimSpace(v)) == "null" {
			return TemplateSecurity{}, ErrInvalid // null would silently decode as false or empty
		}
	}
	rest, _ := json.Marshal(raw)
	d := json.NewDecoder(bytes.NewReader(rest))
	d.DisallowUnknownFields()
	var t TemplateSecurity
	if d.Decode(&t) != nil {
		return TemplateSecurity{}, ErrInvalid
	}
	if err := t.Validate(); err != nil {
		return TemplateSecurity{}, err
	}
	return t, nil
}
