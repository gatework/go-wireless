package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/netdisco"
)

func TestAPIKeyCacheRequiresNonsecretAccountIdentity(t *testing.T) {
	for _, mode := range []string{"prefer", "only", "refresh"} {
		t.Run(mode, func(t *testing.T) {
			o, err := options(t, "netdisco: {url: 'https://inventory.invalid', cache_mode: "+mode+"}\n", map[string]string{"ND_API_KEY": "private-key"})
			if err != nil {
				t.Fatal(err)
			}
			_, identity, err := defaultSource(o)
			var problem *netdisco.InventoryError
			if !errors.As(err, &problem) || problem.Code != "cache_namespace_required" || identity != "" {
				t.Fatalf("identity=%q error=%v", identity, err)
			}
		})
	}
	o, err := options(t, "netdisco: {url: 'https://inventory.invalid', cache_enabled: false}\n", map[string]string{"ND_API_KEY": "private-key"})
	if err != nil {
		t.Fatal(err)
	}
	if source, _, err := defaultSource(o); err != nil || source == nil {
		t.Fatal("uncached API key requires namespace", err)
	}
}

func TestHTTPIdentitySeparatesAccountsAndQueryWithoutKeyDerivatives(t *testing.T) {
	identity := func(username, namespace, key, endpoint string, pageSize int) string {
		t.Helper()
		o, err := options(t, "netdisco: {url: 'https://inventory.invalid', cache_mode: only}\n", map[string]string{"ND_USERNAME": username, "ND_CACHE_NAMESPACE": namespace, "ND_API_KEY": key})
		if err != nil {
			t.Fatal(err)
		}
		n := o.Values["netdisco"].(map[string]any)
		n["url"], n["page_size"] = endpoint, pageSize
		source, result, err := defaultSource(o)
		if err != nil || source != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(result, "private-key") || strings.Contains(result, "api_key_hash") {
			t.Fatal("credential material in identity")
		}
		return result
	}
	base := identity("", "reader-one", "private-key-1", "https://inventory.invalid", 500)
	rotated := identity("", "reader-one", "private-key-2", "https://INVENTORY.invalid/", 500)
	if base != rotated {
		t.Fatal("key rotation or normalized URL changed account identity")
	}
	for _, changed := range []string{
		identity("", "reader-two", "private-key-1", "https://inventory.invalid", 500),
		identity("reader-one", "", "private-key-1", "https://inventory.invalid", 500),
		identity("", "reader-one", "private-key-1", "https://other.invalid", 500),
		identity("", "reader-one", "private-key-1", "https://inventory.invalid", 100),
	} {
		if base == changed {
			t.Fatal("distinct account or query reused identity")
		}
	}
	apiKey := identity("reader-one", "", "private-key-1", "https://inventory.invalid", 500)
	basic := identity("reader-one", "", "", "https://inventory.invalid", 500)
	if apiKey == basic || basic == identity("reader-two", "", "", "https://inventory.invalid", 500) {
		t.Fatal("authentication or username was omitted from identity")
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
