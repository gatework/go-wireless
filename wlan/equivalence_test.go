package wlan_test

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/gatework/go-netops/evidence"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
	"github.com/gatework/go-wireless/vendors"
	"github.com/gatework/go-wireless/wlan"
)

var declarations = map[string][]wireless.Dimension{
	"DisplayCurrentConfiguration": {wireless.Configuration, wireless.APGroups, wireless.ServiceTemplates}, "DisplayWlanApAllVerbose": {wireless.AccessPoints, wireless.Radios},
	"DisplayWlanBssAllVerbose": {wireless.BSSes}, "DisplayWlanClientVerbose": {wireless.Clients}, "DisplayDot1xConnection": {wireless.Dot1xConnections},
	"DisplayMacAuthenticationConnection": {wireless.MABConnections}, "DisplayWlanClientSecurityBlockMac": {wireless.BlockedClients},
	"DisplayWlanStatisticsClientIpConflict": {wireless.ClientIPConflicts}, "DisplayWlanApModelAll": {wireless.APModels},
	"DisplayLldpNeighborInformationVerbose": {wireless.LLDP}, "DisplayApAll": {wireless.AccessPoints}, "DisplayStationAll": {wireless.Clients},
}

func generic(t *testing.T, body []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func zero(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == ""
	case json.Number:
		f, _ := new(big.Float).SetString(x.String())
		return f != nil && f.Sign() == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func lossless(original, roundTrip any, path string) error {
	switch o := original.(type) {
	case map[string]any:
		r, ok := roundTrip.(map[string]any)
		if !ok {
			if zero(o) && roundTrip == nil {
				return nil
			}
			return fmt.Errorf("%s: object became %T", path, roundTrip)
		}
		for k, v := range o {
			rv, present := r[k]
			if !present {
				if zero(v) {
					continue
				}
				return fmt.Errorf("%s.%s: lost %v", path, k, v)
			}
			if err := lossless(v, rv, path+"."+k); err != nil {
				return err
			}
		}
		for k, v := range r {
			if _, present := o[k]; !present && !zero(v) {
				return fmt.Errorf("%s.%s: invented %v", path, k, v)
			}
		}
		return nil
	case []any:
		r, ok := roundTrip.([]any)
		if !ok {
			if len(o) == 0 && roundTrip == nil {
				return nil
			}
			return fmt.Errorf("%s: list became %T", path, roundTrip)
		}
		if len(r) != len(o) {
			return fmt.Errorf("%s: %d items became %d", path, len(o), len(r))
		}
		for i := range o {
			if err := lossless(o[i], r[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case json.Number:
		r, ok := roundTrip.(json.Number)
		a, _ := new(big.Float).SetString(o.String())
		var b *big.Float
		if ok {
			b, _ = new(big.Float).SetString(r.String())
		}
		if a == nil || b == nil || a.Cmp(b) != 0 {
			return fmt.Errorf("%s: %v became %v", path, original, roundTrip)
		}
		return nil
	}
	if fmt.Sprint(original) != fmt.Sprint(roundTrip) {
		return fmt.Errorf("%s: %v became %v", path, original, roundTrip)
	}
	return nil
}

func typed(c *wlan.Controller, dim evidence.Dimension) any {
	switch dim {
	case wlan.DimConfiguration:
		if c.Configuration == nil {
			return nil
		}
		return []any{c.Configuration}
	case wlan.DimAccessPoints:
		return c.AccessPoints
	case wlan.DimRadios:
		return c.Radios
	case wlan.DimBSSes:
		return c.BSSes
	case wlan.DimClients:
		return c.Clients
	case wlan.DimAPGroups:
		return c.APGroups
	case wlan.DimServiceTemplates:
		return c.ServiceTemplates
	case wlan.DimDot1x:
		return c.Dot1x
	case wlan.DimMAB:
		return c.MAB
	case wlan.DimBlockedClients:
		return c.BlockedClients
	case wlan.DimIPConflicts:
		return c.IPConflicts
	case wlan.DimAPModels:
		return c.APModels
	case wlan.DimLLDP:
		return c.LLDP
	}
	return nil
}

func TestParserCorpusDecodesLosslessly(t *testing.T) {
	f, err := os.Open("../testdata/ruby-parser-corpus.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Parser   string
		Input    string `json:"input_base64"`
		Complete bool
		ID       string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	runtime, err := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
	if err != nil {
		t.Fatal(err)
	}
	records := 0
	for _, c := range cases {
		parts := strings.Split(c.Parser, "::")
		dims := declarations[parts[len(parts)-1]]
		if dims == nil {
			t.Fatalf("%s: unmapped parser %s", c.ID, c.Parser)
		}
		platform := "h3c"
		if strings.Contains(c.Parser, "Huawei") {
			platform = "huawei"
		}
		input, _ := base64.StdEncoding.DecodeString(c.Input)
		out, err := runtime.ParseMany(dims, string(input), parser.ParseOptions{Platform: platform, Complete: c.Complete})
		if err != nil {
			t.Fatal(err)
		}
		observed := map[evidence.Dimension]wlan.Observed{}
		originals := map[evidence.Dimension]any{}
		for _, dim := range dims {
			o := out[dim]
			var obs wlan.Observed
			obs.Status = evidence.Status(o.Status())
			var recs []any
			for _, r := range o.Records() {
				body, err := wireless.MarshalJSON(r)
				if err != nil {
					t.Fatal(err)
				}
				obs.Records = append(obs.Records, body)
				recs = append(recs, generic(t, body))
				records++
			}
			for _, i := range o.Issues() {
				body, err := wireless.MarshalJSON(i)
				if err != nil {
					t.Fatal(err)
				}
				obs.Issues = append(obs.Issues, body)
			}
			observed[evidence.Dimension(dim)] = obs
			originals[evidence.Dimension(dim)] = recs
		}
		ctl := wlan.NewController(wlan.Identity{Host: "192.0.2.10"}, observed)
		for _, dim := range dims {
			d := evidence.Dimension(dim)
			for _, i := range ctl.Issues[d] {
				if i.Code == wlan.IssueUnmodeled {
					t.Fatalf("%s %s/%s: %s", c.ID, parts[len(parts)-1], d, i.Detail)
				}
			}
			if got, want := ctl.Coverage.Status(d), observed[d].Status; got != want {
				t.Fatalf("%s %s: status %s became %s", c.ID, d, want, got)
			}
			body, err := json.Marshal(typed(ctl, d))
			if err != nil {
				t.Fatal(err)
			}
			orig := originals[d]
			if orig == nil {
				orig = []any{}
			}
			if err := lossless(orig, generic(t, body), string(d)); err != nil {
				t.Fatalf("%s %s: %v", c.ID, parts[len(parts)-1], err)
			}
		}
	}
	t.Logf("decoded %d records from %d corpus cases losslessly", records, len(cases))
}

func TestUnmodeledFieldDowngradesDimension(t *testing.T) {
	c := wlan.NewController(wlan.Identity{Host: "192.0.2.10"}, map[evidence.Dimension]wlan.Observed{
		wlan.DimRadios: {Status: evidence.StatusComplete, Records: []json.RawMessage{json.RawMessage(`{"radio_id":"1","tx_power_dbm":20}`)}},
	})
	if c.Has(wlan.DimRadios) || c.Issues[wlan.DimRadios][0].Code != wlan.IssueUnmodeled {
		t.Fatalf("unmodeled field accepted: %+v", c)
	}
}
