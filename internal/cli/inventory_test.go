package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/internal/bootstrap"

	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/netdisco"
	wireless "github.com/gatework/go-wireless"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestHTTPInventoryThroughNativePaginationAndControllerRules(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "wireless.yml"), "netdisco: {source: http, url: 'https://inventory.invalid', page_size: 2, cache_enabled: false}\ninventory: {include_vendors: [h3c_wireless]}\n")
	o, e := bootstrap.LoadConfiguration(t.Context(), bootstrap.Request{}, map[string]string{"ND_API_KEY": "private-token"}, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	requests := 0
	rows, total, e := inventoryRows(context.Background(), o, func(ctx context.Context, settings bootstrap.InventorySettings) (netdisco.CacheResult, error) {
		httpOptions := settings.HTTP()
		httpOptions.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			requests++
			if r.Header.Get("Authorization") != "Apikey private-token" {
				t.Fatal("auth")
			}
			body := `[]`
			if requests == 1 {
				body = `[{"ip":"192.0.2.10","vendor":"H3C","model":"WX3540","name":"WLC"},{"ip":"192.0.2.11","vendor":"H3C","model":"S6520","name":"Switch"}]`
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})}
		return netdisco.FetchHTTP(ctx, httpOptions, settings.FetchOptions())
	})
	if e != nil || len(rows) != 1 || total != 1 || requests != 2 {
		t.Fatal(rows, total, requests, e)
	}
}
func TestCacheOnlyAndRefreshFallbackUseNativeVerifiedCache(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "wireless.yml"), "netdisco: {cache_enabled: true, cache_directory: cache, cache_ttl: 10}\n")
	o, e := bootstrap.LoadConfiguration(t.Context(), bootstrap.Request{}, map[string]string{}, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	source := netdisco.SourceFunc(func(context.Context) ([]inventory.Device, error) {
		calls++
		return []inventory.Device{{IP: "192.0.2.10", Vendor: "H3C", Model: "WX3540"}}, nil
	})
	factory := fixtureInventoryFetcher(source, "identity")
	rows, _, e := inventoryRows(context.Background(), o, factory)
	if e != nil || len(rows) != 1 || calls != 1 {
		t.Fatal(rows, calls, e)
	}
	rows, _, e = inventoryRows(context.Background(), o, factory)
	if e != nil || len(rows) != 1 || calls != 1 {
		t.Fatal("cache not preferred", rows, calls, e)
	}
	only, e := bootstrap.LoadConfiguration(t.Context(), bootstrap.Request{InventoryOverrides: map[string]any{"cache_mode": "only"}}, map[string]string{}, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	rows, _, e = inventoryRows(context.Background(), only, fixtureInventoryFetcher(nil, "identity"))
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	refresh, e := bootstrap.LoadConfiguration(t.Context(), bootstrap.Request{InventoryOverrides: map[string]any{"cache_mode": "refresh"}}, map[string]string{}, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	rows, _, e = inventoryRows(context.Background(), refresh, fixtureInventoryFetcher(netdisco.SourceFunc(func(context.Context) ([]inventory.Device, error) { return nil, errors.New("private remote error") }), "identity"))
	if e != nil || len(rows) != 1 {
		t.Fatal("no cache fallback", rows, e)
	}
	paths, _ := filepath.Glob(filepath.Join(d, "cache/*.json"))
	if len(paths) != 1 {
		t.Fatal(paths)
	}
	if e = os.WriteFile(paths[0], []byte(`{"schema":1,"identity":"tampered","fetched_at":0,"rows":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = inventoryRows(context.Background(), only, factory); e == nil {
		t.Fatal("tampered cache accepted")
	}
}
func TestPostgresConfigurationAndSecretsRemainOffline(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "wireless.yml"), "netdisco: {source: postgres, query: 'SELECT ip, name, vendor, model FROM device WHERE vendor=$1', query_params: [H3C], cache_enabled: false, db_sslmode: require, db_port: 5433, db_connect_timeout: 5}\n")
	env := map[string]string{"ND_DB_HOST": "database.invalid", "ND_DB_NAME": "netdisco", "ND_DB_USER": "reader", "ND_DB_PASS": "private-pass"}
	o, e := bootstrap.LoadConfiguration(t.Context(), bootstrap.Request{}, env, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	source, e := netdisco.NewPostgres(o.InventorySettings().Postgres())
	if e != nil || source == nil {
		t.Fatal(e)
	}
	if strings.Contains(fmt.Sprint(o.InventorySettings()), "private-pass") {
		t.Fatal("password in cache identity")
	}
	var out, err bytes.Buffer
	code := Run(context.Background(), []string{"--show-config"}, Dependencies{Environment: map[string]string{}, Input: panicReader{}, Output: &out, Error: &err, Discovery: discovery(d), InventoryFetch: func(context.Context, bootstrap.InventorySettings) (netdisco.CacheResult, error) {
		panic("show-config read inventory")
	}})
	if code != 0 || strings.Contains(out.String(), "private-pass") {
		t.Fatal(code, out.String(), err.String())
	}
}
func TestDefaultHTTPSourceOnlyDoesNotRequirePassword(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "wireless.yml"), "netdisco: {source: http, url: 'https://example.invalid', cache_mode: only}\n")
	o, e := bootstrap.LoadConfiguration(t.Context(), bootstrap.Request{}, map[string]string{"ND_USERNAME": "reader"}, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = o.FetchInventory(t.Context(), nil)
	var problem *netdisco.InventoryError
	if !errors.As(e, &problem) || problem.Code != "cache_unavailable" || !errors.Is(e, netdisco.ErrCacheRefreshRequired) {
		t.Fatal("cache-only required a password", e)
	}
}
func TestInitializationPreservesPublicSchemaDefaults(t *testing.T) {
	d := t.TempDir()
	var out, err bytes.Buffer
	code := Run(context.Background(), []string{"--init", "--non-interactive"}, Dependencies{Environment: map[string]string{}, Input: panicReader{}, Output: &out, Error: &err, Discovery: discovery(d)})
	if code != 0 {
		t.Fatal(code, err.String())
	}
	o, e := bootstrap.LoadConfiguration(t.Context(), bootstrap.Request{ConfigFile: filepath.Join(d, ".config/net-connector/wireless.yml"), ExplicitConfig: true}, map[string]string{}, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	n := o.Values["netdisco"].(map[string]any)
	for k, v := range (wireless.Record{"source": "http", "cache_enabled": true, "cache_mode": "prefer", "cache_ttl": 259200, "page_size": 500, "max_pages": 10000, "max_response_bytes": 16777216, "max_inventory_bytes": 134217728, "max_devices": 100000, "inventory_timeout": 300, "allow_insecure_http": false}) {
		if n[k] != v {
			t.Errorf("%s=%v want %v", k, n[k], v)
		}
	}
	if o.Values["ssh"].(map[string]any)["host_key_policy"] != "strict" {
		t.Fatal(o.Values)
	}
}

func inventoryRows(ctx context.Context, o *bootstrap.Options, fetch bootstrap.InventoryFetcher) ([]wireless.Record, int, error) {
	result, err := o.FetchInventory(ctx, fetch)
	return result.Devices, result.Total, err
}
func fixtureInventoryFetcher(source netdisco.Source, identity string) bootstrap.InventoryFetcher {
	return func(ctx context.Context, settings bootstrap.InventorySettings) (netdisco.CacheResult, error) {
		policy := settings.FetchOptions()
		if policy.Cache == nil {
			rows, err := source.Devices(ctx)
			return netdisco.CacheResult{Rows: rows, Status: netdisco.CacheStatusDisabled}, err
		}
		cache, err := netdisco.NewCache(source, netdisco.CacheOptions{Directory: policy.Cache.Directory, Identity: identity, TTL: policy.Cache.TTL, Limits: settings.HTTP().Limits})
		if err != nil {
			return netdisco.CacheResult{}, err
		}
		return cache.Fetch(ctx, policy.Mode)
	}
}
