package bootstrap

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gatework/go-wireless/vendors"

	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/netdisco"
	wireless "github.com/gatework/go-wireless"
)

// InventorySettings owns only source and cache settings. It never exposes the
// controller credentials, report configuration, or mutable CLI Options.
type InventorySettings struct {
	source                             string
	http                               netdisco.HTTPOptions
	postgres                           netdisco.PostgresOptions
	fetch                              netdisco.FetchOptions
	postgresIdentity, postgresPassword bool
}

// Source returns the inventory source: http or postgres.
func (s InventorySettings) Source() string { return s.source }

// HTTP returns the Netdisco HTTP source options.
func (s InventorySettings) HTTP() netdisco.HTTPOptions { return s.http }

// Postgres returns the Netdisco Postgres source options.
func (s InventorySettings) Postgres() netdisco.PostgresOptions {
	options := s.postgres
	options.Parameters = slices.Clone(options.Parameters)
	return options
}

// FetchOptions returns the cache and limit options for a fetch.
func (s InventorySettings) FetchOptions() netdisco.FetchOptions {
	options := s.fetch
	if options.Cache != nil {
		cache := *options.Cache
		if cache.TTL != nil {
			ttl := *cache.TTL
			cache.TTL = &ttl
		}
		options.Cache = &cache
	}
	return options
}
func (InventorySettings) String() string { return "cli.InventorySettings" }

// GoString formats metadata only, never credentials.
func (s InventorySettings) GoString() string { return s.String() }

// MarshalJSON encodes nothing: settings may contain credentials.
func (InventorySettings) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

// InventoryFetcher replaces the former SourceFactory. It receives detached
// source settings and returns the complete connector cache receipt.
type InventoryFetcher func(context.Context, InventorySettings) (netdisco.CacheResult, error)

// InventoryResult is the fetched and selected inventory.
type InventoryResult struct {
	Devices []wireless.Record
	Total   int
	Cache   netdisco.CacheResult
}

func compileInventory(data wireless.Record, environment map[string]string, cwd string) (InventorySettings, error) {
	n, _ := data["netdisco"].(map[string]any)
	limits, err := inventoryLimits(n)
	if err != nil {
		return InventorySettings{}, err
	}
	s := InventorySettings{source: StringValue(n["source"]), fetch: netdisco.FetchOptions{Mode: netdisco.CacheMode(StringValue(n["cache_mode"]))}}
	if s.source == "" {
		s.source = "http"
	}
	if s.fetch.Mode == "" {
		s.fetch.Mode = netdisco.CachePrefer
	}
	if !has(n, "cache_enabled") || boolValue(n["cache_enabled"]) {
		directory := StringValue(n["cache_directory"])
		if directory == "" {
			directory = filepath.Join(cwd, ".cache/net-connector/inventory")
		}
		ttl := 72 * time.Hour
		if value, ok := n["cache_ttl"]; ok {
			seconds, _ := number(value)
			if seconds > float64(int64(^uint64(0)>>1))/1e9 {
				return InventorySettings{}, ErrOptions
			}
			ttl = time.Duration(seconds * 1e9)
		}
		s.fetch.Cache = &netdisco.CachePolicy{Directory: directory, Namespace: StringValue(n["cache_namespace"]), TTL: &ttl}
	}
	if s.source == "http" {
		s.http = netdisco.HTTPOptions{URL: StringValue(n["url"]), Username: secret(environment, "ND_USERNAME"), Password: secret(environment, "ND_PASSWORD"), APIKey: secret(environment, "ND_API_KEY"), RequireHTTPS: !boolValue(n["allow_insecure_http"]), Limits: limits}
		return s, nil
	}
	db := map[string]string{}
	for key, name := range map[string]string{ //nolint:gosec // maps settings to environment variable names, not secrets
		"host": "ND_DB_HOST", "dbname": "ND_DB_NAME", "user": "ND_DB_USER", "password": "ND_DB_PASS"} {
		db[key] = secret(environment, name)
	}
	s.postgresIdentity = db["host"] != "" && db["dbname"] != "" && db["user"] != ""
	s.postgresPassword = db["password"] != ""
	for field, key := range map[string]string{"db_port": "port", "db_sslmode": "sslmode", "db_sslrootcert": "sslrootcert", "db_connect_timeout": "connect_timeout"} {
		if value, ok := n[field]; ok {
			db[key] = fmt.Sprint(value)
		}
	}
	parts := []string{}
	for _, key := range []string{"host", "dbname", "user", "password", "port", "sslmode", "sslrootcert", "connect_timeout"} {
		if value, ok := db[key]; ok && value != "" {
			parts = append(parts, key+"='"+strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `'`, `\'`)+"'")
		}
	}
	params, _ := n["query_params"].([]any)
	s.postgres = netdisco.PostgresOptions{Connection: strings.Join(parts, " "), Query: StringValue(n["query"]), Parameters: slices.Clone(params), Limits: limits}
	return s, nil
}

func fetchInventory(ctx context.Context, s InventorySettings) (netdisco.CacheResult, error) {
	if s.source == "http" {
		if s.http.URL == "" || s.http.APIKey == "" && s.http.Username == "" {
			return netdisco.CacheResult{}, ErrOptions
		}
		if s.http.APIKey != "" && s.fetch.Cache != nil && strings.TrimSpace(s.http.Username) == "" && strings.TrimSpace(s.fetch.Cache.Namespace) == "" {
			return netdisco.CacheResult{}, &netdisco.InventoryError{Code: "cache_namespace_required"}
		}
		return netdisco.FetchHTTP(ctx, s.HTTP(), s.FetchOptions())
	}
	if s.source != "postgres" || !s.postgresIdentity || s.fetch.Mode != netdisco.CacheOnly && !s.postgresPassword {
		return netdisco.CacheResult{}, ErrOptions
	}
	return netdisco.FetchPostgres(ctx, s.Postgres(), s.FetchOptions())
}

// FetchInventory uses the validated snapshot even if a caller edits the exported
// configuration views. The result retains freshness and refresh diagnostics.
func (o *Options) FetchInventory(ctx context.Context, fetch InventoryFetcher) (InventoryResult, error) {
	if o.runtime == nil {
		return InventoryResult{}, ErrOptions
	}
	r := o.runtime
	result := InventoryResult{}
	targets := make([]wireless.Record, len(r.devices))
	for i, device := range r.devices {
		targets[i] = copyMap(device)
	}
	if r.dynamic {
		if fetch == nil {
			fetch = fetchInventory
		}
		var err error
		settings := r.inventory
		settings.postgres = settings.Postgres()
		settings.fetch = settings.FetchOptions()
		result.Cache, err = fetch(ctx, settings)
		if err != nil {
			return result, err
		}
		targets = []wireless.Record{}
		for _, device := range result.Cache.Rows {
			record := r.rules.Record(device)
			profile, err := vendors.NewBuiltinRegistry().Fetch(record.Vendor, record.Model)
			if record.Issue != "" || err != nil || profile.ConnectorPlatform() != record.Vendor {
				continue
			}
			var model any
			if record.Model != "" {
				model = record.Model
			}
			name := record.Name
			if name == "" {
				name = record.Host
			}
			targets = append(targets, wireless.Record{"host": record.Host, "name": name, "model": model, "platform": record.Vendor})
		}
		rows := make([]any, len(targets))
		for i, device := range targets {
			rows[i] = device
		}
		targets, err = NormalizeDevices(rows, r.platform)
		if err != nil {
			return result, err
		}
	}
	result.Total = len(targets)
	result.Devices = []wireless.Record{}
	for _, device := range targets {
		if r.selection.Evaluate(inventory.Device{IP: StringValue(device["host"]), Name: StringValue(device["name"])}).Selected() {
			result.Devices = append(result.Devices, device)
		}
	}
	return result, nil
}
