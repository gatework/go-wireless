package wireless_test

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	parse "github.com/gatework/go-wireless/parser"
)

func TestReviewLargePositiveRSSIMatchesRubyFloatRound(t *testing.T) {
	cases := []struct {
		input string
		want  any
	}{{"18.5", int64(19)}, {"0.5", int64(1)}, {"100000000000000000000", json.Number("100000000000000000000")}, {"123456789012345680000", json.Number("123456789012345683968")}}
	parser, _ := parse.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
	for _, test := range cases {
		t.Run(test.input, func(t *testing.T) {
			o, err := parser.Parse(wireless.Clients, "Total clients: 1\nMAC address: 0200-0000-0001\nRSSI: "+test.input+"\n", parse.ParseOptions{Complete: true})
			if err != nil || !o.Complete() {
				t.Fatal(o.Issues(), err)
			}
			if got := o.Records()[0]["rssi"]; got != test.want {
				t.Fatalf("got %v (%T), want %v (%T)", got, got, test.want, test.want)
			}
		})
	}
	// The result must describe the parsed binary float, rather than the decimal input.
	maximum := "179769313486231570000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	want := json.Number("179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368")
	o, err := parser.Parse(wireless.Clients, "Total clients: 1\nMAC address: 0200-0000-0001\nRSSI: "+maximum+"\n", parse.ParseOptions{Complete: true})
	if err != nil || !o.Complete() || o.Records()[0]["rssi"] != want {
		t.Fatal("maximum finite RSSI precision")
	}
}
func TestReviewIPv4CompatibleIPv6CanonicalIdentity(t *testing.T) {
	cases := map[string]string{"::192.0.2.1": "::192.0.2.1", "::c000:201": "::192.0.2.1", "::1:0": "::0.1.0.0", "::ffff:ffff": "::255.255.255.255", "::2": "::2", "::ffff": "::ffff", "::ffff:c000:201": "::ffff:192.0.2.1", "2001:0db8::0001": "2001:db8::1"}
	parser, _ := parse.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got, err := parse.ParseIP(&input)
			if err != nil || got == nil || *got != want {
				t.Fatalf("got %v, %v; want %s", got, err, want)
			}
			o, err := parser.Parse(wireless.Clients, "Total clients: 1\nMAC address: 0200-0000-0001\nIPv6 address: "+input+"\n", parse.ParseOptions{Complete: true})
			if err != nil || !o.Complete() || o.Records()[0]["client_ipv6"] != want {
				t.Fatal("parser address identity", o.Records(), err)
			}
		})
	}
}
func TestReviewCoverageCompositeStatusesAreOwned(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	for _, status := range []any{wireless.Record{"x": []any{"original"}}, []any{wireless.Record{"x": "original"}}} {
		coverage, err := wireless.NewCoverage(wireless.CoverageOptions{Observations: wireless.Record{"clients": wireless.Record{"latest": wireless.Record{"status": status, "observed_at": now.Format(time.RFC3339Nano)}}}, Projection: wireless.Record{"clients": wireless.Record{"status": status, "observed_at": now.Format(time.RFC3339Nano)}}, Dimensions: []wireless.Dimension{wireless.Clients}, Now: now, StaleAfter: 1})
		if err != nil {
			t.Fatal(err)
		}
		before := coverage.ToMap()
		baseline := wireless.CloneRecord(before)
		entry := before["dimensions"].(wireless.Record)["clients"].(wireless.Record)
		mutateReviewStatus(entry["status"])
		mutateReviewStatus(entry["projection_status"])
		if !reflect.DeepEqual(coverage.ToMap(), baseline) {
			t.Fatal("Coverage ToMap exposed retained status values")
		}
		// Previously exported results may be mutated independently of concurrent reads.
		var wg sync.WaitGroup
		wg.Go(func() {
			for range 100 {
				mutateReviewStatus(entry["status"])
				mutateReviewStatus(entry["projection_status"])
			}
		})
		for range 100 {
			if !reflect.DeepEqual(coverage.ToMap(), baseline) {
				t.Error("concurrent coverage output mutation changed retained state")
				break
			}
		}
		wg.Wait()
	}
}
func mutateReviewStatus(value any) {
	switch v := value.(type) {
	case wireless.Record:
		v["x"].([]any)[0] = "changed"
	case []any:
		v[0].(wireless.Record)["x"] = "changed"
	}
}
