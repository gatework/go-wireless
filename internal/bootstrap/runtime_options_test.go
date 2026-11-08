package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/netdisco"
	wireless "github.com/gatework/go-wireless"
)

func TestOptionsExecutionOwnsConfigurationAndCredentials(t *testing.T) {
	environment := map[string]string{"NC_DEVICE_USERNAME": "original", "NC_DEVICE_PASSWORD": "secret"}
	o, err := options(t, "devices: [{host: '192.0.2.10', name: original}]\ndimensions: [clients]\nap_names: [AP-1]\n", environment)
	if err != nil {
		t.Fatal(err)
	}
	environment["NC_DEVICE_USERNAME"] = "changed"
	o.Environment["NC_DEVICE_USERNAME"] = "changed"
	o.Values["devices"].([]any)[0].(map[string]any)["host"] = "192.0.2.99"
	o.Values["dimensions"].([]any)[0] = "lldp"
	o.Values["ap_names"].([]any)[0] = "AP-OTHER"
	o.Values["ssh"].(map[string]any)["host_key_policy"] = "accept_new"
	o.Values["parallel"] = "invalid-after-validation"
	o.Values["netdisco"] = wireless.Record{}
	result, err := o.FetchInventory(t.Context(), func(context.Context, InventorySettings) (netdisco.CacheResult, error) {
		t.Fatal("view switched to dynamic inventory")
		return netdisco.CacheResult{}, nil
	})
	if err != nil || len(result.Devices) != 1 || result.Devices[0]["host"] != "192.0.2.10" {
		t.Fatal(result, err)
	}
	configuration, err := o.Connection(result.Devices[0])
	if err != nil || configuration.Username != "original" || configuration.HostKeyPolicy != "strict" || o.Workers() != 4 || o.Dimensions()[0] != wireless.Clients || o.APNames()[0] != "AP-1" {
		t.Fatal("validated execution changed", err)
	}
	result.Devices[0]["host"] = "192.0.2.88"
	fresh, err := o.FetchInventory(t.Context(), nil)
	if err != nil || fresh.Devices[0]["host"] != "192.0.2.10" {
		t.Fatal("returned target aliases snapshot", err)
	}
}

func TestInventorySettingsDetachAdaptersAndRetainCompleteReceipt(t *testing.T) {
	o, err := options(t, "netdisco: {source: postgres, query: 'SELECT ip FROM device WHERE vendor=$1', query_params: [H3C], cache_mode: only}\n", map[string]string{"ND_DB_HOST": "database.invalid", "ND_DB_NAME": "netdisco", "ND_DB_USER": "reader", "ND_DB_PASS": "private-db", "NC_DEVICE_PASSWORD": "private-controller"})
	if err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1234, 0)
	for range 2 {
		result, err := o.FetchInventory(t.Context(), func(_ context.Context, settings InventorySettings) (netdisco.CacheResult, error) {
			pg, fetch := settings.Postgres(), settings.FetchOptions()
			if pg.Parameters[0] != "H3C" || *fetch.Cache.TTL != 72*time.Hour {
				t.Fatal("prior adapter mutated snapshot")
			}
			pg.Parameters[0] = "changed"
			*fetch.Cache.TTL = 0
			body, err := json.Marshal(settings)
			if err != nil || strings.Contains(string(body), "private") || strings.Contains(settings.String(), "private") {
				t.Fatal("settings exposed secrets", err)
			}
			return netdisco.CacheResult{Rows: []inventory.Device{{IP: "192.0.2.10", Vendor: "H3C", Model: "WX3540"}}, Status: netdisco.CacheStatusStale, RefreshErrorCode: "source_failed", WriteErrorType: "write_failed", FetchedAt: when}, nil
		})
		if err != nil || len(result.Devices) != 1 || result.Cache.Status != netdisco.CacheStatusStale || result.Cache.RefreshErrorCode != "source_failed" || result.Cache.WriteErrorType != "write_failed" || !result.Cache.FetchedAt.Equal(when) {
			t.Fatal("lost cache evidence", result, err)
		}
	}
	var zero Options
	if _, err := zero.FetchInventory(t.Context(), nil); !errors.Is(err, ErrOptions) {
		t.Fatal("unvalidated options executable", err)
	}
}
