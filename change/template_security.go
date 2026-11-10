package change

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"

	netchange "github.com/gatework/go-netops/change"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/wlan"
)

// KindTemplateSecurity removes legacy TKIP/WPA from one service template.
const KindTemplateSecurity netchange.Kind = "wireless_security"

// TemplateSecurity is the typed form of the wireless_security change. It
// renders exactly the commands of RenderChange.
type TemplateSecurity struct {
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	CipherSuites []string `json:"cipher_suites"`
	SecurityIEs  []string `json:"security_ies"`
}

var _ netchange.Change[*wlan.Configuration] = TemplateSecurity{}

func (TemplateSecurity) Kind() netchange.Kind { return KindTemplateSecurity }

func (t TemplateSecurity) record() wireless.Record {
	return wireless.Record{"kind": string(KindTemplateSecurity), "name": t.Name, "enabled": t.Enabled,
		"cipher_suites": slices.Clone(t.CipherSuites), "security_ies": slices.Clone(t.SecurityIEs)}
}

// Validate requires a legal name, CCMP and RSN to remain, and something to remove.
func (t TemplateSecurity) Validate() error {
	_, _, err := RenderChange(t.record())
	return err
}

// Render returns the H3C commands and rollback.
func (t TemplateSecurity) Render() (netchange.Script, error) {
	commands, rollback, err := RenderChange(t.record())
	if err != nil {
		return netchange.Script{}, err
	}
	return netchange.Script{Commands: commands, Rollback: rollback}, nil
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
		return TemplateSecurity{}, ErrChangePlan
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return TemplateSecurity{}, ErrChangePlan
	}
	var kind string
	if json.Unmarshal(raw["kind"], &kind) != nil || netchange.Kind(kind) != KindTemplateSecurity {
		return TemplateSecurity{}, ErrChangePlan
	}
	delete(raw, "kind")
	for _, required := range []string{"name", "enabled", "cipher_suites", "security_ies"} {
		if v, ok := raw[required]; !ok || string(bytes.TrimSpace(v)) == "null" {
			return TemplateSecurity{}, ErrChangePlan // null would silently decode as false or empty
		}
	}
	rest, _ := json.Marshal(raw)
	d := json.NewDecoder(bytes.NewReader(rest))
	d.DisallowUnknownFields()
	var t TemplateSecurity
	if d.Decode(&t) != nil {
		return TemplateSecurity{}, ErrChangePlan
	}
	if err := t.Validate(); err != nil {
		return TemplateSecurity{}, err
	}
	return t, nil
}
