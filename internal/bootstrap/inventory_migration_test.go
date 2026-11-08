package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/netdisco"
	wireless "github.com/gatework/go-wireless"
)

func TestAPIKeyCacheRequiresNonsecretAccountIdentity(t *testing.T) {
	for _, mode := range []string{"prefer", "only", "refresh"} {
		t.Run(mode, func(t *testing.T) {
			o, err := options(t, "netdisco: {url: 'https://inventory.invalid', cache_mode: "+mode+"}\n", map[string]string{"ND_API_KEY": "private-key"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = fetchInventory(t.Context(), o.InventorySettings())
			var problem *netdisco.InventoryError
			if !errors.As(err, &problem) || problem.Code != "cache_namespace_required" {
				t.Fatal(err)
			}
		})
	}
	o, err := options(t, "netdisco: {url: 'https://inventory.invalid', cache_enabled: false}\n", map[string]string{"ND_API_KEY": "private-key"})
	if err != nil {
		t.Fatal(err)
	}
	settings := o.InventorySettings()
	settings.http.Client = inventoryHTTP(t, nil)
	if _, err := fetchInventory(t.Context(), settings); err != nil {
		t.Fatal("uncached API key requires namespace", err)
	}
}

func inventoryHTTP(t *testing.T, calls *int) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		body := `[]`
		if request.URL.Query().Get("offset") == "0" {
			if calls != nil {
				*calls++
			}
			body = `[{"ip":"192.0.2.10","vendor":"H3C","model":"WX3540"}]`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
}

func TestHTTPIdentitySeparatesAccountsAndQueryWithoutKeyDerivatives(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "cache")
	settings := func(username, namespace, key, endpoint string, pageSize int) InventorySettings {
		t.Helper()
		o, err := options(t, fmt.Sprintf("netdisco: {url: '%s', cache_mode: only, page_size: %d}\n", endpoint, pageSize), map[string]string{"ND_USERNAME": username, "ND_CACHE_NAMESPACE": namespace, "ND_API_KEY": key})
		if err != nil {
			t.Fatal(err)
		}
		s := o.InventorySettings()
		s.fetch.Cache.Directory = directory
		s.http.Client = inventoryHTTP(t, nil)
		return s
	}
	base := settings("", "reader-one", "private-key-1", "https://inventory.invalid", 500)
	base.fetch.Mode = "refresh"
	if _, err := fetchInventory(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	rotated := settings("", "reader-one", "private-key-2", "https://INVENTORY.invalid/", 500)
	if _, err := fetchInventory(t.Context(), rotated); err != nil {
		t.Fatal("rotation or normalized URL missed account cache", err)
	}
	for _, changed := range []InventorySettings{
		settings("", "reader-two", "private-key-1", "https://inventory.invalid", 500),
		settings("reader-one", "", "private-key-1", "https://inventory.invalid", 500),
		settings("", "reader-one", "private-key-1", "https://other.invalid", 500),
		settings("", "reader-one", "private-key-1", "https://inventory.invalid", 100),
		settings("reader-one", "", "", "https://inventory.invalid", 500),
	} {
		_, err := fetchInventory(t.Context(), changed)
		var problem *netdisco.InventoryError
		if !errors.As(err, &problem) || problem.Code != "cache_unavailable" || !errors.Is(err, netdisco.ErrCacheRefreshRequired) {
			t.Fatal("distinct account/query reused identity", err)
		}
	}
}

func TestLegacyInventoryCacheRequiresNewFetchAndRetainsMetadata(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "cache")
	legacy, _ := json.Marshal(wireless.Record{"source": "http", "url": "https://inventory.invalid", "username": "reader", "authentication": "api_key", "namespace": "", "query": wireless.Record{"endpoint": "api/v1/search/device", "fields": "ip,name,dns,vendor,os,model,os_ver,serial", "page_size": 500, "fallback_q": "%", "fallback_seeallcolumns": true}})
	cache, err := netdisco.NewCache(netdisco.SourceFunc(func(context.Context) ([]inventory.Device, error) {
		return []inventory.Device{{IP: "192.0.2.99", Vendor: "H3C", Model: "WX3540"}}, nil
	}), netdisco.CacheOptions{Directory: directory, Identity: string(legacy)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Fetch(t.Context(), "refresh"); err != nil {
		t.Fatal(err)
	}
	load := func(mode string) *Options {
		t.Helper()
		o, err := options(t, fmt.Sprintf("netdisco: {url: 'https://inventory.invalid', cache_directory: '%s', cache_mode: %s}\n", directory, mode), map[string]string{"ND_USERNAME": "reader", "ND_API_KEY": "fixture-key"})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	calls := 0
	fetch := func(ctx context.Context, settings InventorySettings) (netdisco.CacheResult, error) {
		settings.http.Client = inventoryHTTP(t, &calls)
		return fetchInventory(ctx, settings)
	}
	if _, err := load("only").FetchInventory(t.Context(), fetch); err == nil || calls != 0 {
		t.Fatal("legacy cache satisfied new identity", err, calls)
	}
	for _, mode := range []string{"prefer", "only", "refresh"} {
		result, err := load(mode).FetchInventory(t.Context(), fetch)
		if err != nil || len(result.Devices) != 1 || result.Devices[0]["host"] != "192.0.2.10" || result.Cache.Status == "" || result.Cache.FetchedAt.IsZero() {
			t.Fatal(mode, result, err)
		}
	}
	if calls != 2 {
		t.Fatal("only fetched or prefer reused legacy", calls)
	}
	paths, err := filepath.Glob(filepath.Join(directory, "*.json"))
	if err != nil || len(paths) != 2 {
		t.Fatal("legacy cache was rewritten", paths, err)
	}
}

func TestInventoryModelRulesUseConnectorSelection(t *testing.T) {
	o, err := options(t, "netdisco: {}\ninventory: {include_models: [WX], exclude_models: [old], model_match: contains}\n", map[string]string{"NC_EXCLUDE_MODELS": "3540"})
	if err != nil {
		t.Fatal(err)
	}
	rules, err := inventoryRules(o.inventoryData(), o.registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		model    string
		selected bool
	}{
		{"WX3540", false},
		{"WX5000", true},
		{"WXold", true},
		{"S6520", false},
	} {
		record := rules.Record(inventory.Device{IP: "192.0.2.10", Vendor: "H3C", Model: tc.model})
		if selected := record.Issue == ""; selected != tc.selected {
			t.Errorf("model %s selected=%v issue=%s", tc.model, selected, record.Issue)
		}
	}
}
