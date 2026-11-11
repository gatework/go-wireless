package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gatework/go-net-connector/connector"
	ncvendors "github.com/gatework/go-net-connector/vendors"
	"github.com/gatework/go-net-connector/vendors/dialect"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/adapter/netconnector"
	"github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/collect"
	"github.com/gatework/go-wireless/internal/workflow/apply"
	collectrun "github.com/gatework/go-wireless/internal/workflow/collect"
	"github.com/gatework/go-wireless/report"
	"github.com/gatework/go-wireless/vendors"
)

// Setup errors; their messages never include credentials or device text.
var (
	// ErrCredentials reports that device credentials are unavailable.
	ErrCredentials = errors.New("wireless: credentials unavailable")
	ErrPlatform    = errors.New("wireless: unavailable platform")
	ErrConnection  = errors.New("wireless: invalid connection settings")
	ErrResources   = errors.New("wireless: insufficient local resources")
	ErrReport      = errors.New("wireless: report unavailable")
	ErrEnvironment = errors.New("wireless: environment unavailable")
)

// Connection is an opened collection session and the connector platform it
// uses.
type Connection struct {
	Open     collect.OpenSession
	Platform string
}

// SessionFactory opens a collection session to one device.
type SessionFactory func(context.Context, wireless.Record, connector.ConnectionConfig) (Connection, error)

// ApplyFactory lends one synchronous session and returns operation and cleanup errors separately.
type ApplyFactory func(context.Context, wireless.Record, connector.ConnectionConfig, func(context.Context, change.ApplySession) error) (error, error)

// CollectionDependencies are the injectable collaborators of a collection
// run.
type CollectionDependencies struct {
	Registry           dialect.Registry
	SessionFactory     SessionFactory
	APFactory          collect.APFactory
	PrepareResources   func(context.Context, int) error
	Execute            collectrun.Executor
	NewReport          func(string, report.Options) (*report.Report, error)
	ReportOptions      report.Options
	RecommendationHook func(bool) report.DeviceHook
	Scripts            bool
	SessionEvent       func(context.Context, connector.SessionEvent) error
}

// CollectionDefaults binds production defaults without opening sources, sessions or reports.
func CollectionDefaults(d CollectionDependencies) CollectionDependencies {
	if d.Registry == nil {
		d.Registry = ncvendors.NewBuiltinRegistry()
	}
	if d.PrepareResources == nil {
		d.PrepareResources = PrepareResources
	}
	if d.Execute == nil {
		d.Execute = Execute
	}
	if d.NewReport == nil {
		d.NewReport = report.New
	}
	if d.RecommendationHook == nil && d.ReportOptions.DeviceHook == nil {
		d.RecommendationHook = report.AssessmentHook
	}
	return d
}

// PrepareCollection binds validated settings to concrete dependencies. Opening a
// session remains lazy and is performed only by the workflow's task callback.
func PrepareCollection(ctx context.Context, o *Options, devices []wireless.Record, d CollectionDependencies) (collectrun.Options, error) {
	callbacks := make(map[string]func(context.Context) (wireless.Snapshot, error), len(devices))
	registry := vendors.NewBuiltinRegistry()
	for _, device := range devices {
		profile, err := registry.Fetch(StringValue(device["platform"]), StringValue(device["model"]))
		if err != nil {
			return collectrun.Options{}, fmt.Errorf("%w: %w", ErrPlatform, err)
		}
		cfg, err := o.runtime.connectionForProfile(device, profile)
		if err != nil {
			return collectrun.Options{}, fmt.Errorf("%w: %w", ErrCredentials, err)
		}
		cfg.OnSessionEvent = d.SessionEvent
		driver, err := d.Registry.Lookup(profile.ConnectorPlatform())
		if err != nil {
			return collectrun.Options{}, fmt.Errorf("%w: %w", ErrPlatform, err)
		}
		native, err := connector.New(driver.Profile(), cfg)
		if err != nil {
			return collectrun.Options{}, fmt.Errorf("%w: %w", ErrConnection, err)
		}
		scope, err := profile.CollectionScope(o.runtime.dimensions)
		if err != nil {
			return collectrun.Options{}, err
		}
		apFactory := d.APFactory
		if apFactory == nil && scope.APNeighbors() {
			apFactory = netconnector.APFactory(nil)
		}
		callbacks[StringValue(device["host"])] = func(taskCtx context.Context) (wireless.Snapshot, error) {
			connection := Connection{Open: netconnector.Open(native, profile), Platform: native.ProfileName()}
			if d.SessionFactory != nil {
				var err error
				connection, err = d.SessionFactory(taskCtx, copyMap(device), cfg)
				if err != nil {
					return wireless.Snapshot{}, err
				}
			}
			return collect.NewCollector(collect.CollectorOptions{APFactory: apFactory}).Collect(taskCtx, collect.Request{Target: device, Profile: profile, ConnectorPlatform: connection.Platform, Dimensions: o.runtime.dimensions, APNames: o.runtime.apNames, Open: connection.Open, APCredentials: func(context.Context) (collect.APCredentials, error) { return o.runtime.apCredentials, nil }})
		}
	}
	if err := d.PrepareResources(ctx, min(o.Workers(), len(devices))); err != nil {
		return collectrun.Options{}, fmt.Errorf("%w: %w", ErrResources, err)
	}
	if err := ctx.Err(); err != nil {
		return collectrun.Options{}, err
	}
	reportOptions := d.ReportOptions
	if d.RecommendationHook != nil {
		reportOptions.DeviceHook = d.RecommendationHook(d.Scripts)
	}
	store, err := d.NewReport(o.Directory(), reportOptions)
	if err != nil {
		return collectrun.Options{}, fmt.Errorf("%w: %w", ErrReport, err)
	}
	return collectrun.Options{Workers: o.Workers(), Requested: devices, Execute: d.Execute, Journal: store, Collect: func(ctx context.Context, device wireless.Record) (wireless.Snapshot, error) {
		run := callbacks[StringValue(device["host"])]
		if run == nil {
			return wireless.Snapshot{}, ErrOptions
		}
		return run(ctx)
	}}, nil
}

// PrepareApply resolves credentials and connection settings for the plan's
// device and returns the opener apply uses.
func PrepareApply(ctx context.Context, environment map[string]string, envFile string, discovery Discovery, registry dialect.Registry, factory ApplyFactory, plan change.Plan, port int) (apply.Opener, error) {
	env, _, err := LoadEnvironment(ctx, environment, envFile, discovery)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEnvironment, err)
	}
	settings := runtimeOptions{environment: env, ssh: sshSettings{port: port, policy: connector.HostKeyPolicy("strict"), knownHosts: env["NC_KNOWN_HOSTS"], commandTimeout: 60 * time.Second, loginTimeout: 15 * time.Second}}
	cfg, err := settings.connection(wireless.Record{"host": plan.Host(), "platform": "h3c_wireless"})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCredentials, err)
	}
	platform, err := registry.Lookup("h3c_wireless")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPlatform, err)
	}
	native, err := connector.New(platform.Profile(), cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConnection, err)
	}
	if factory == nil {
		return func(ctx context.Context, run func(context.Context, change.ApplySession) error) (error, error) {
			return netconnector.Apply(ctx, native, run)
		}, nil
	}
	return func(ctx context.Context, run func(context.Context, change.ApplySession) error) (error, error) {
		return factory(ctx, plan.Device(), cfg, run)
	}, nil
}
