package bootstrap

import (
	"maps"
	"slices"

	"github.com/gatework/go-net-connector/connector"
	ncvendors "github.com/gatework/go-net-connector/vendors"
	"github.com/gatework/go-net-connector/vendors/dialect"
	wireless "github.com/gatework/go-wireless"
)

// Request carries explicit configuration inputs; presentation flags stay in CLI.
type Request struct {
	ConfigFile, EnvFile           string
	ExplicitConfig, ExplicitEnv   bool
	Initialize                    bool
	Overrides, InventoryOverrides wireless.Record
}

func NewOptions(request Request, discovery Discovery, registry dialect.Registry) *Options {
	if registry == nil {
		registry = ncvendors.NewBuiltinRegistry()
	}
	return &Options{initializing: request.Initialize, discovery: discovery, overrides: copyMap(request.Overrides), inventoryOverrides: copyMap(request.InventoryOverrides), registry: registry,
		selectedConfig: request.ConfigFile, selectedEnv: request.EnvFile, explicitConfig: request.ExplicitConfig, explicitEnv: request.ExplicitEnv}
}

// LoadConfiguration resolves configuration once, without inventory or device I/O.
// Initialization requests load only the environment snapshot required by templates.
func LoadConfiguration(request Request, environment map[string]string, discovery Discovery, registry dialect.Registry) (*Options, error) {
	o := NewOptions(request, discovery, registry)
	if err := o.loadEnvironment(environment); err != nil {
		return nil, err
	}
	if request.Initialize {
		return o, nil
	}
	data, specific, err := o.loadFiles()
	if err != nil {
		return nil, err
	}
	if o.applicable {
		if err = o.load(data, specific); err != nil {
			return nil, err
		}
	}
	return o, nil
}
func (o *Options) Missing() bool { return !o.applicable }
func (o *Options) Workers() int {
	if o.runtime == nil {
		return 0
	}
	return o.runtime.workers
}
func (o *Options) Directory() string {
	if o.runtime == nil {
		return ""
	}
	return o.runtime.directory
}
func (o *Options) Dimensions() []wireless.Dimension {
	if o.runtime == nil {
		return nil
	}
	return slices.Clone(o.runtime.dimensions)
}
func (o *Options) APNames() []string {
	if o.runtime == nil {
		return nil
	}
	return slices.Clone(o.runtime.apNames)
}
func (o *Options) Connection(device wireless.Record) (connector.ConnectionConfig, error) {
	if o.runtime == nil {
		return connector.ConnectionConfig{}, ErrOptions
	}
	return o.runtime.connection(device)
}
func (o *Options) InventorySettings() InventorySettings {
	if o.runtime == nil {
		return InventorySettings{}
	}
	return o.runtime.inventory
}
func (o *Options) EnvironmentSnapshot() map[string]string {
	if o.runtime == nil {
		return maps.Clone(o.Environment)
	}
	return maps.Clone(o.runtime.environment)
}
