package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gatework/go-netops/assess"
	wireless "github.com/gatework/go-wireless"

	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/wlan"
)

// parityInputs are the review inputs whose plans must not change when the
// review switches to the typed assessment: the Ruby interop device, the Ruby
// recommendation corpus and every template security state on both platforms.
func parityInputs(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	body, err := os.ReadFile("../testdata/ruby-interop/device.json")
	if err != nil {
		t.Fatal(err)
	}
	out["ruby-interop"] = body
	body, err = os.ReadFile("../testdata/ruby-recommendations.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Input string `json:"input_json"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(body, &corpus); err != nil {
		t.Fatal(err)
	}
	for i, c := range corpus.Cases {
		out[fmt.Sprintf("ruby-%02d", i)] = []byte(c.Input)
	}
	lists := map[string]any{"absent": nil, "empty": []any{}, "ccmp": []any{"ccmp"}, "ccmp_tkip": []any{"ccmp", "tkip"}, "tkip": []any{"tkip"}}
	ies := map[string]any{"absent": nil, "empty": []any{}, "rsn": []any{"rsn"}, "rsn_wpa": []any{"rsn", "wpa"}, "wpa": []any{"wpa"}}
	enabled := map[string]any{"absent": nil, "true": true, "false": false}
	names := map[string]string{"corp": "corp", "bad": "bad name"}
	for _, platform := range []string{"h3c_wireless", "huawei_wireless"} {
		for ln, l := range lists {
			for in, i := range ies {
				for en, e := range enabled {
					for nn, n := range names {
						settings := map[string]any{}
						if l != nil {
							settings["cipher_suites"] = l
						}
						if i != nil {
							settings["security_ies"] = i
						}
						tpl := map[string]any{"name": n, "configuration": settings}
						if e != nil {
							tpl["enabled"] = e
						}
						other := map[string]any{"name": "guest", "enabled": true, "configuration": map[string]any{"cipher_suites": []any{"ccmp", "tkip"}, "security_ies": []any{"rsn", "wpa"}}}
						doc := map[string]any{"device": map[string]any{"name": "wlc", "host": "192.0.2.1", "platform": platform},
							"snapshot": map[string]any{"observed_at": "2026-11-10T00:00:00Z", "dimensions": map[string]any{
								"configuration": map[string]any{"status": "complete", "records": []any{map[string]any{"service_templates": []any{tpl, other}}}}}}}
						body, err := json.Marshal(doc)
						if err != nil {
							t.Fatal(err)
						}
						out[fmt.Sprintf("%s/c=%s/i=%s/e=%s/n=%s", platform, ln, in, en, nn)] = body
					}
				}
			}
		}
	}
	return out
}

func planBodies(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "recommendations", "*.plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	slices.Sort(out)
	return out
}

// typedPlans runs the typed review output for one document.
func typedPlans(t *testing.T, body []byte) ([]string, bool) {
	t.Helper()
	doc, err := wireless.DecodeJSONDocument(body)
	if err != nil {
		return nil, false
	}
	c, err := wlan.DecodeDocument(body)
	if err != nil {
		return nil, false
	}
	assessor, err := rules.NewAssessor(assess.Policy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := assessor.Assess(context.Background(), []*wlan.Controller{c})
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(filepath.Join(t.TempDir(), "typed"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteAssessment(context.Background(), doc, c, r.Controllers[0], "x", "source.json", "00", true); err != nil {
		t.Fatal(err)
	}
	return planBodies(t, store.Directory()), true
}

const planOracle = "../testdata/typed/plan-oracle.json"

// withheld are inputs where legacy published plans and the typed review
// deliberately does not: their configuration carries a field the parser never
// emits, so the typed decoder marks it partial and no rule may authorize a
// change from it.
var withheld = map[string]string{
	"ruby-00": `access point "radio" (parsers emit "radios") is an unmodeled fact`,
}

// TestTypedPlansMatchFrozenLegacyPlans replays the plan files the legacy
// recommendation writer produced, frozen in testdata/typed/plan-oracle.json,
// on the typed review output.
func TestTypedPlansMatchFrozenLegacyPlans(t *testing.T) {
	body, err := os.ReadFile(planOracle)
	if err != nil {
		t.Fatal(err)
	}
	var oracle map[string][]string
	if err := json.Unmarshal(body, &oracle); err != nil {
		t.Fatal(err)
	}
	inputs := parityInputs(t)
	plans := 0
	for name, in := range inputs {
		want, frozen := oracle[name]
		if !frozen {
			t.Fatalf("%s: no frozen plans", name)
		}
		got, ok := typedPlans(t, in)
		if !ok {
			// The typed decoder rejects this historical shape; review then
			// refuses the whole document, so no plan can be published.
			got = []string{}
		}
		if reason, ok := withheld[name]; ok {
			if len(want) == 0 || len(got) != 0 {
				t.Errorf("%s: withheld (%s) but legacy %d typed %d plans", name, reason, len(want), len(got))
			}
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: typed plans differ from legacy\n got %q\nwant %q", name, got, want)
		}
		plans += len(want)
	}
	if len(inputs) != len(oracle) {
		t.Fatalf("compared %d of %d frozen inputs", len(inputs), len(oracle))
	}
	if plans == 0 {
		t.Fatal("no plans compared")
	}
	t.Logf("frozen legacy plan sets reproduced: %d inputs, %d plans", len(inputs), plans)
}
