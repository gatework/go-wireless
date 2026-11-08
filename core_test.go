package wireless_test

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
	h3c "github.com/gatework/go-wireless/vendors/h3c"
)

func TestRubyParserCorpus(t *testing.T) {
	file, err := os.Open("testdata/ruby-parser-corpus.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gz)
	gz.Close()
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Parser   string
		Input    string `json:"input_base64"`
		Complete bool
		Expected map[string]any
		ID       string
		Source   map[string]any
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	declarations := map[string][]wireless.Dimension{
		"DisplayCurrentConfiguration": {wireless.Configuration}, "DisplayWlanApAllVerbose": {wireless.AccessPoints, wireless.Radios}, "DisplayWlanBssAllVerbose": {wireless.BSSes}, "DisplayWlanClientVerbose": {wireless.Clients}, "DisplayDot1xConnection": {wireless.Dot1xConnections}, "DisplayMacAuthenticationConnection": {wireless.MABConnections}, "DisplayWlanClientSecurityBlockMac": {wireless.BlockedClients}, "DisplayWlanStatisticsClientIpConflict": {wireless.ClientIPConflicts}, "DisplayWlanApModelAll": {wireless.APModels}, "DisplayLldpNeighborInformationVerbose": {wireless.LLDP}, "DisplayApAll": {wireless.AccessPoints}, "DisplayStationAll": {wireless.Clients},
	}
	for _, c := range cases {
		t.Run(c.Parser+"/"+c.ID, func(t *testing.T) {
			parts := strings.Split(c.Parser, "::")
			dims, ok := declarations[parts[len(parts)-1]]
			if !ok {
				t.Fatal("unmapped parser")
			}
			platform := "h3c"
			if strings.Contains(c.Parser, "Huawei") {
				platform = "huawei"
			}
			input, err := base64.StdEncoding.DecodeString(c.Input)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
			out, err := p.ParseMany(dims, string(input), parser.ParseOptions{Platform: platform, Complete: c.Complete})
			if err != nil {
				t.Fatal(err)
			}

			if c.Source != nil {
				source, err := h3c.ParseConfigurationSource(string(input), c.Complete)
				if err != nil {
					t.Fatal(err)
				}
				captures := []any{}
				for _, capture := range source.Captures() {
					captures = append(captures, wireless.Record{"first_line": capture.FirstLine(), "last_line": capture.LastLine(), "content_base64": base64.StdEncoding.EncodeToString([]byte(capture.Content())), "path": capture.Path(), "data": capture.Data(), "complete": capture.Complete()})
				}
				actual := wireless.Record{"checksum": source.Checksum(), "states": source.States(), "unparsed_base64": base64.StdEncoding.EncodeToString([]byte(source.UnparsedContent())), "captures": captures}
				b, _ := json.Marshal(actual)
				var normalized wireless.Record
				decodeOracleJSON(b, &normalized)
				if !reflect.DeepEqual(numberMeanings(normalized), numberMeanings(c.Source)) {
					t.Errorf("configuration source/capture mismatch for %s", c.ID)
					want, _ := json.Marshal(c.Source)
					if len(b) < 12000 && len(want) < 12000 {
						t.Errorf("got %s\nwant %s", b, want)
					}
				}
			}
			for _, dim := range dims {
				expected := c.Expected
				if _, ok := expected["status"]; !ok {
					expected = expected[string(dim)].(map[string]any)
				}
				obs := out[dim]
				actual := map[string]any{"status": obs.Status(), "records": obs.Records(), "issues": obs.Issues()}
				encoded, _ := json.Marshal(actual)
				var normalized map[string]any
				decodeOracleJSON(encoded, &normalized)
				if !reflect.DeepEqual(numberMeanings(normalized), numberMeanings(expected)) {
					got, _ := json.Marshal(normalized)
					want, _ := json.Marshal(expected)
					if len(got) > 3000 {
						got = got[:3000]
					}
					if len(want) > 3000 {
						want = want[:3000]
					}
					t.Errorf("%s\ngot %s\nwant %s", dim, got, want)
				}
			}
		})
	}
}

func TestObservationOwnershipAndSafeFormatting(t *testing.T) {
	source := wireless.Record{"secret": []any{wireless.Record{"token": "PRIVATE"}}}
	o, err := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Clients, Status: wireless.Complete, Records: []wireless.Record{source}})
	if err != nil {
		t.Fatal(err)
	}
	source["secret"].([]any)[0].(wireless.Record)["token"] = "MUTATED"
	records := o.Records()
	records[0]["secret"].([]any)[0].(wireless.Record)["token"] = "AGAIN"
	if o.Records()[0]["secret"].([]any)[0].(wireless.Record)["token"] != "PRIVATE" {
		t.Fatal("ownership leaked")
	}
	if strings.Contains(o.String(), "PRIVATE") || strings.Contains(o.GoString(), "PRIVATE") {
		t.Fatal("unsafe format")
	}
	if _, err := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Clients, Status: wireless.Failed, Records: []wireless.Record{{"x": 1}}}); err == nil {
		t.Fatal("failed observations cannot contain records")
	}
}
func TestRuntimeInvalidRequestsAndIndependentViews(t *testing.T) {
	p, _ := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
	for _, names := range [][]wireless.Dimension{nil, {}, {wireless.Clients, wireless.Clients}, {""}} {
		if _, err := p.ParseMany(names, "", parser.ParseOptions{}); err == nil {
			t.Fatalf("accepted %v", names)
		}
	}
	if _, err := p.ParseMany([]wireless.Dimension{wireless.Clients, wireless.AccessPoints}, "", parser.ParseOptions{}); err == nil {
		t.Fatal("accepted unrelated shared output")
	}
	out, err := p.ParseMany([]wireless.Dimension{wireless.AccessPoints, wireless.Radios}, "Total APs: 1\nAP name: AP\n", parser.ParseOptions{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out[wireless.AccessPoints].Complete() || out[wireless.Radios].Complete() {
		t.Fatal("AP count proves no radio coverage")
	}
	o, err := p.Parse(wireless.Radios, "", parser.ParseOptions{Platform: "huawei", Complete: true})
	if err != nil || o.Status() != wireless.Unsupported {
		t.Fatal("unsupported dimension")
	}
}
func TestCoverageFreshness(t *testing.T) {
	now := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	history := wireless.Record{"access_points": wireless.Record{"latest": wireless.Record{"status": "complete", "observed_at": now.Format(time.RFC3339)}}}
	c, err := wireless.NewCoverage(wireless.CoverageOptions{Observations: history, Dimensions: []wireless.Dimension{wireless.AccessPoints}, Now: now, StaleAfter: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if c.ToMap()["state"] != "complete" {
		t.Fatal(c.ToMap())
	}
	c, _ = wireless.NewCoverage(wireless.CoverageOptions{Observations: history, Projection: wireless.Record{"access_points": wireless.Record{"status": "blocked", "observed_at": now.Format(time.RFC3339)}}, Dimensions: []wireless.Dimension{wireless.AccessPoints}, Now: now, StaleAfter: 3600})
	if c.ToMap()["state"] != "unknown" {
		t.Fatal(c.ToMap())
	}
}

// Compare JSON number meaning without rounding large integers through float64.
// Ruby emits Float(5) as 5.0 and Go emits float64(5) as 5, which mean the same number.
type numberMeaning struct{ exact string }

func decodeOracleJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(destination)
}
func numberMeanings(v any) any {
	switch value := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(string(value))
		if !ok {
			panic("invalid oracle number")
		}
		return numberMeaning{r.RatString()}
	case map[string]any:
		out := map[string]any{}
		for key, child := range value {
			out[key] = numberMeanings(child)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = numberMeanings(child)
		}
		return out
	default:
		return v
	}
}
