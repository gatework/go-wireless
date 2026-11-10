package wlan

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"

	"go.yaml.in/yaml/v3"
)

// BaselineSchemaVersion is the version of the wireless baseline document.
const BaselineSchemaVersion = 1

// ErrBaseline reports an invalid baseline document.
var ErrBaseline = errors.New("wlan: invalid baseline")

// TemplateExpectation is the intended state of one service template.
// Nil fields and nil lists state no expectation.
type TemplateExpectation struct {
	SSID         *string  `yaml:"ssid,omitempty" json:"ssid,omitempty"`
	VLAN         *int     `yaml:"vlan,omitempty" json:"vlan,omitempty"`
	Enabled      *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	AKMModes     []string `yaml:"akm_modes,omitempty" json:"akm_modes,omitempty"`
	CipherSuites []string `yaml:"cipher_suites,omitempty" json:"cipher_suites,omitempty"`
	SecurityIEs  []string `yaml:"security_ies,omitempty" json:"security_ies,omitempty"`
}

// Expectation is the declared intent for one controller.
type Expectation struct {
	ServiceTemplates map[string]TemplateExpectation `yaml:"service_templates,omitempty" json:"service_templates,omitempty"`
	APFirmware       map[string]string              `yaml:"ap_firmware,omitempty" json:"ap_firmware,omitempty"` // AP model -> software version
	BackupAC         *string                        `yaml:"backup_ac,omitempty" json:"backup_ac,omitempty"`
}

// Baseline is the frozen intent of one or more controllers.
type Baseline struct {
	SchemaVersion int                    `yaml:"schema_version" json:"schema_version"`
	Defaults      Expectation            `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Controllers   map[string]Expectation `yaml:"controllers,omitempty" json:"controllers,omitempty"` // host -> overrides
}

// DecodeBaseline strictly decodes and validates a YAML baseline.
func DecodeBaseline(body []byte) (Baseline, error) {
	var b Baseline
	d := yaml.NewDecoder(bytes.NewReader(body))
	d.KnownFields(true)
	if err := d.Decode(&b); err != nil {
		return Baseline{}, fmt.Errorf("%w: %w", ErrBaseline, err)
	}
	return b, b.Validate()
}

// Encode renders the baseline as YAML.
func (b Baseline) Encode() ([]byte, error) {
	var out bytes.Buffer
	e := yaml.NewEncoder(&out)
	e.SetIndent(2)
	if err := e.Encode(b); err != nil {
		return nil, err
	}
	return out.Bytes(), e.Close()
}

// Validate checks the schema version and value ranges.
func (b Baseline) Validate() error {
	if b.SchemaVersion != BaselineSchemaVersion {
		return fmt.Errorf("%w: schema_version %d", ErrBaseline, b.SchemaVersion)
	}
	check := func(scope string, e Expectation) error {
		for name, t := range e.ServiceTemplates {
			if name == "" || t.VLAN != nil && (*t.VLAN < 1 || *t.VLAN > 4094) {
				return fmt.Errorf("%w: %s service template %q", ErrBaseline, scope, name)
			}
		}
		if e.BackupAC != nil {
			if a, err := netip.ParseAddr(*e.BackupAC); err != nil || a.String() != *e.BackupAC {
				return fmt.Errorf("%w: %s backup_ac", ErrBaseline, scope)
			}
		}
		return nil
	}
	if err := check("defaults", b.Defaults); err != nil {
		return err
	}
	for _, host := range slices.Sorted(maps.Keys(b.Controllers)) {
		if err := check("controller "+host, b.Controllers[host]); err != nil {
			return err
		}
	}
	return nil
}

// For returns a controller's effective expectation: per-template and
// per-model entries override the defaults, other fields replace them.
func (b Baseline) For(host string) Expectation {
	e := Expectation{ServiceTemplates: maps.Clone(b.Defaults.ServiceTemplates), APFirmware: maps.Clone(b.Defaults.APFirmware), BackupAC: b.Defaults.BackupAC}
	o, ok := b.Controllers[host]
	if !ok {
		return e
	}
	if e.ServiceTemplates == nil && o.ServiceTemplates != nil {
		e.ServiceTemplates = map[string]TemplateExpectation{}
	}
	maps.Copy(e.ServiceTemplates, o.ServiceTemplates)
	if e.APFirmware == nil && o.APFirmware != nil {
		e.APFirmware = map[string]string{}
	}
	maps.Copy(e.APFirmware, o.APFirmware)
	if o.BackupAC != nil {
		e.BackupAC = o.BackupAC
	}
	return e
}
