package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResourcePreflightReservesOnlyActiveWorkers(t *testing.T) {
	for _, tc := range []struct {
		parallel string
		want     int
	}{{"1", 1}, {"8", 2}} {
		t.Run(tc.parallel, func(t *testing.T) {
			code, _, _, _, _ := runFixture(t, "devices: [{host: '192.0.2.10'}, {host: '192.0.2.11'}]\n", []string{"--parallel", tc.parallel}, func(d *Dependencies) {
				d.SessionFactory = panicFactory
				d.PrepareResources = func(_ context.Context, active int) error {
					if active != tc.want {
						t.Errorf("active=%d want=%d", active, tc.want)
					}
					return errors.New("fixture limit exhausted")
				}
			})
			if code != 2 {
				t.Fatal(code)
			}
		})
	}
}

func TestAPIKeyNamespaceFailureIsActionableAndDoesNotConnect(t *testing.T) {
	code, _, _, diagnostic, _ := runFixture(t, "netdisco: {url: 'https://inventory.invalid'}\n", nil, func(d *Dependencies) {
		d.Environment["ND_API_KEY"] = "private-token"
		d.SessionFactory = panicFactory
		d.PrepareResources = func(context.Context, int) error { panic("unexpected resource preparation") }
	})
	if code != 2 || !strings.Contains(diagnostic, "ND_CACHE_NAMESPACE") || strings.Contains(diagnostic, "private-token") {
		t.Fatal(code, diagnostic)
	}
}
