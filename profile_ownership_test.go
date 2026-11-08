package wireless_test

import (
	"context"
	"reflect"
	"testing"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
	"github.com/gatework/go-wireless/parser"
	"github.com/gatework/go-wireless/vendors/h3c"
	"github.com/gatework/go-wireless/vendors/huawei"
)

func TestRegisteredVendorParsersOwnCapturedDeclarations(t *testing.T) {
	for _, spec := range []parser.ProfileSpec{h3c.Profile(), huawei.Profile()} {
		t.Run(spec.Platform, func(t *testing.T) {
			registry, err := parser.NewRegistry(spec)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := registry.Fetch(spec.Platform, "")
			if err != nil {
				t.Fatal(err)
			}
			name := spec.Queries[0].Dimensions[0]
			before, err := profile.Parse(name, "", true)
			if err != nil {
				t.Fatal(err)
			}
			spec.Queries[0].Dimensions[0] = wireless.Clients
			spec.Queries[0].Command = "changed command"
			after, err := profile.Parse(name, "", true)
			if err != nil || !reflect.DeepEqual(before.ToMap(), after.ToMap()) {
				t.Fatal("caller changed registered dispatch", err, before, after)
			}
		})
	}
}
func TestCollectorUsesSelectedProfileForAPNeighbors(t *testing.T) {
	spec := h3c.Profile()
	calls := 0
	for i := range spec.Queries {
		if spec.Queries[i].Command == selectedProfile("h3c").APQuery().Command() {
			spec.Queries[i].Parse = func(string, bool) map[wireless.Dimension]parser.Result {
				calls++
				return map[wireless.Dimension]parser.Result{wireless.LLDP: {Status: wireless.Complete, Records: []wireless.Record{{"selected_profile": true}}}}
			}
		}
	}
	registry, err := parser.NewRegistry(spec)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := registry.Fetch("h3c", "")
	if err != nil {
		t.Fatal(err)
	}
	session := controllerFixture()
	apInventory(session)
	opts := options(session, wireless.LLDP)
	opts.Profile = profile
	capture := goodAPCapture(t, "custom neighbor format")
	collector := collect.NewCollector(collect.CollectorOptions{APFactory: func(context.Context, collect.Session, collect.APCredentials) (collect.APReader, error) {
		return apReaderFunc(func(context.Context, collect.APRequest) (collect.APOutcome, error) {
			return collect.APOutcome{Result: &capture}, nil
		}), nil
	}})
	snapshot := mustCollect(t, collector, opts)
	records := snapshot.Records(wireless.LLDP)
	if calls != 2 || len(records) != 2 || records[0]["selected_profile"] != true || records[0]["ap_name"] != "AP-1" || records[1]["ap_name"] != "AP-2" {
		t.Fatal("selected LLDP parser bypassed", calls, records)
	}
}
func TestZeroProfileRejectedBeforeOpeningSession(t *testing.T) {
	opts := options(controllerFixture(), wireless.Clients)
	opts.Profile = &parser.Profile{}
	opts.Open = func(context.Context, func(collect.Session) error) error {
		t.Fatal("opened invalid profile")
		return nil
	}
	if _, err := collect.NewCollector(collect.CollectorOptions{}).Collect(t.Context(), opts); err == nil {
		t.Fatal("zero profile admitted")
	}
	for _, profile := range []*parser.Profile{nil, {}} {
		if _, err := profile.CollectionScope(nil); err == nil {
			t.Fatal("scope accepted zero profile")
		}
		if _, err := parser.NewNeighborParser(profile, ""); err == nil {
			t.Fatal("neighbor parser accepted zero profile")
		}
	}
	if _, err := parser.NewNeighborParser(selectedProfile("h3c"), string([]byte{0xff})); err == nil {
		t.Fatal("invalid AP model accepted")
	}
	if _, err := parser.NewRuntimeParser(nil, ""); err == nil {
		t.Fatal("nil registry accepted")
	}
}
