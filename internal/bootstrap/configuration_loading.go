package bootstrap

import (
	"context"
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

// NewOptions returns unloaded options for a command request;
// LoadConfiguration fills them.
func NewOptions(request Request, discovery Discovery, registry dialect.Registry) *Options {
	if registry == nil {
		registry = ncvendors.NewBuiltinRegistry()
	}
	return &Options{initializing: request.Initialize, discovery: discovery, overrides: copyMap(request.Overrides), inventoryOverrides: copyMap(request.InventoryOverrides), registry: registry,
		selectedConfig: request.ConfigFile, selectedEnv: request.EnvFile, explicitConfig: request.ExplicitConfig, explicitEnv: request.ExplicitEnv}
}

// LoadConfiguration resolves configuration once, without inventory or device I/O.
// Initialization requests load only the environment snapshot required by templates.
func LoadConfiguration(ctx context.Context, request Request, environment map[string]string, discovery Discovery, registry dialect.Registry) (*Options, error) {
	o := NewOptions(request, discovery, registry)
	if err := o.loadEnvironment(ctx, environment); err != nil {
		return nil, err
	}
	if request.Initialize {
		return o, nil
	}
	data, specific, err := o.loadFiles(ctx)
	if err != nil {
		return nil, err
	}
	if o.applicable {
		if err = o.load(ctx, data, specific); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return o, nil
}

// Missing reports that no applicable configuration file was found.
func (o *Options) Missing() bool { return !o.applicable }

// Workers returns the number of devices collected in parallel.
func (o *Options) Workers() int {
	if o.runtime == nil {
		return 0
	}
	return o.runtime.workers
}

// Directory returns the batch report root.
func (o *Options) Directory() string {
	if o.runtime == nil {
		return ""
	}
	return o.runtime.directory
}

// Dimensions returns the requested dimensions, or nil for the defaults.
func (o *Options) Dimensions() []wireless.Dimension {
	if o.runtime == nil {
		return nil
	}
	return slices.Clone(o.runtime.dimensions)
}

// APNames returns the APs to read LLDP neighbors from; empty means all.
func (o *Options) APNames() []string {
	if o.runtime == nil {
		return nil
	}
	return slices.Clone(o.runtime.apNames)
}

// Connection returns the connector settings for one device, credentials
// resolved from the environment.
func (o *Options) Connection(device wireless.Record) (connector.ConnectionConfig, error) {
	if o.runtime == nil {
		return connector.ConnectionConfig{}, ErrOptions
	}
	return o.runtime.connection(device)
}

// InventorySettings returns the resolved inventory source settings.
func (o *Options) InventorySettings() InventorySettings {
	if o.runtime == nil {
		return InventorySettings{}
	}
	return o.runtime.inventory
}

// EnvironmentSnapshot returns a copy of the effective environment.
func (o *Options) EnvironmentSnapshot() map[string]string {
	if o.runtime == nil {
		return maps.Clone(o.Environment)
	}
	return maps.Clone(o.runtime.environment)
}
