package cli

import (
	"context"
	"testing"

	"github.com/gatework/go-net-connector/netdisco"
	"github.com/gatework/go-wireless/internal/bootstrap"
)

func TestCacheFlagsOverrideInventoryEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name, flag, want string
	}{
		{name: "cache_only", flag: "--cache-only", want: "only"},
		{name: "refresh_inventory", flag: "--refresh-inventory", want: "refresh"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, config, _, diagnostic, _ := runFixture(t,
				"netdisco: {cache_enabled: true, cache_mode: prefer}\n",
				[]string{"collect", "--show-config", tt.flag},
				func(d *Dependencies) {
					d.Environment = map[string]string{"ND_CACHE_MODE": "invalid"}
					d.SessionFactory = panicFactory
					d.InventoryFetch = func(context.Context, bootstrap.InventorySettings) (netdisco.CacheResult, error) {
						panic("show-config fetched inventory")
					}
				})
			if code != 0 {
				t.Fatalf("%s exited %d: %s", tt.flag, code, diagnostic)
			}
			inventory, ok := config["netdisco"].(map[string]any)
			if !ok {
				t.Fatal("show-config omitted inventory configuration")
			}
			if got := inventory["cache_mode"]; got != tt.want {
				t.Fatalf("cache_mode = %v, want %q", got, tt.want)
			}
		})
	}
}
