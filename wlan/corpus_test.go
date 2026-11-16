//go:build corpus

package wlan_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/evidence"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/vendors"
	"github.com/gatework/go-wireless/wlan"
	source "github.com/gatework/parser"
)

type corpusCapture struct {
	Command     string `json:"command"`
	Success     bool   `json:"success"`
	Bytes       int    `json:"bytes"`
	SHA256      string `json:"sha256"`
	PromptAfter string `json:"prompt_after"`
}

type replayCounts struct {
	Captures     int            `json:"captures"`
	Queries      int            `json:"queries"`
	Observations int            `json:"observations"`
	Statuses     map[string]int `json:"statuses"`
	Issues       map[string]int `json:"issues"`
	Rules        map[string]int `json:"rules"`
}

// TestPrivateControllerCorpus is an explicit opt-in replay, not a live-device test.
// The private input stays outside Git; logs contain aggregate counts only.
func TestPrivateControllerCorpus(t *testing.T) {
	root := os.Getenv("WIRELESS_CORPUS_DIR")
	if root == "" {
		t.Fatal("WIRELESS_CORPUS_DIR must name a nonempty private corpus")
	}
	paths, err := filepath.Glob(filepath.Join(root, "*", "*.output"))
	if err != nil || len(paths) == 0 {
		t.Fatal("private corpus has no captures")
	}
	profile, err := vendors.NewBuiltinRegistry().Fetch("h3c", "")
	if err != nil {
		t.Fatal(err)
	}
	queries, err := profile.Queries(profile.Dimensions())
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string][]wireless.Dimension{}
	for _, q := range queries {
		commands[q.Command] = q.Dimensions
	}
	counts := replayCounts{Statuses: map[string]int{}, Issues: map[string]int{}, Rules: map[string]int{}}
	controllers := map[string]map[evidence.Dimension]wlan.Observed{}
	for i, path := range paths {
		meta, raw, body := readCorpusCapture(t, path)
		counts.Captures++
		if meta.Command == profile.PreparationCommand() {
			continue
		}
		dims, ok := commands[meta.Command]
		if !ok {
			t.Fatalf("capture %d has an undeclared command", i+1)
		}
		for _, input := range []struct{ mode, text string }{{"raw", raw}, {"body", body}} {
			out, err := profile.ParseMany(dims, input.text, meta.Success)
			if err != nil {
				t.Fatalf("capture %d parse admission failed", i+1)
			}
			counts.Queries++
			key := input.mode + ":" + filepath.Dir(path)
			if controllers[key] == nil {
				controllers[key] = map[evidence.Dimension]wlan.Observed{}
			}
			for _, dim := range dims {
				o := out[dim]
				counts.Observations++
				counts.Statuses[input.mode+":parser:"+string(dim)+":"+string(o.Status())]++
				controllers[key][evidence.Dimension(dim)] = corpusObserved(t, o)
			}
		}
	}
	if counts.Queries == 0 || counts.Observations == 0 {
		t.Fatal("corpus executed no business queries")
	}
	assessCorpus(t, controllers, &counts)
	summary, err := json.Marshal(counts)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(summary))
}

func readCorpusCapture(t *testing.T, path string) (corpusCapture, string, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("capture cannot be read")
	}
	metadata, err := os.ReadFile(strings.TrimSuffix(path, ".output") + ".json")
	if err != nil {
		t.Fatal("capture metadata cannot be read")
	}
	var meta corpusCapture
	if err := json.Unmarshal(metadata, &meta); err != nil {
		t.Fatal("invalid capture metadata")
	}
	indexed, err := source.New(string(data), source.Limits{MaxBytes: 32 << 20, MaxLines: 400000})
	if err != nil {
		t.Fatal("capture exceeds replay source budget")
	}
	if len(data) != meta.Bytes || fmt.Sprintf("%x", indexed.Digest()) != meta.SHA256 {
		t.Fatal("capture identity mismatch")
	}
	first, firstOK := indexed.Line(1)
	last, lastOK := indexed.Line(indexed.Len())
	if !firstOK || !lastOK || first.Text != meta.Command || meta.PromptAfter == "" || last.Text != meta.PromptAfter {
		t.Fatal("capture command envelope mismatch")
	}
	body := ""
	if indexed.Len() > 2 {
		body, _ = indexed.Span(2, indexed.Len()-1)
	}
	return meta, string(data), body
}

func corpusObserved(t *testing.T, o wireless.Observation) wlan.Observed {
	t.Helper()
	out := wlan.Observed{Status: evidence.Status(o.Status())}
	for _, rec := range o.Records() {
		out.Records = append(out.Records, mustJSON(t, rec))
	}
	for _, issue := range o.Issues() {
		out.Issues = append(out.Issues, mustJSON(t, issue))
	}
	return out
}

func assessCorpus(t *testing.T, controllers map[string]map[evidence.Dimension]wlan.Observed, counts *replayCounts) {
	t.Helper()
	assessor, err := rules.NewAssessor(assess.Policy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, dims := range controllers {
		mode, _, _ := strings.Cut(key, ":")
		c := wlan.NewController(wlan.Identity{Host: "192.0.2.1", Platform: "h3c_wireless"}, dims)
		for dim, status := range c.Coverage {
			counts.Statuses[mode+":typed:"+string(dim)+":"+string(status)]++
			for _, issue := range c.Issues[dim] {
				counts.Issues[mode+":"+string(dim)+":"+issue.Code]++
				if issue.Code == wlan.IssueUnmodeled {
					t.Fatalf("%s %s has unmodeled parser facts", mode, dim)
				}
			}
		}
		report, err := assessor.Assess(t.Context(), []*wlan.Controller{c})
		if err != nil {
			t.Fatal("corpus assessment failed")
		}
		for _, finding := range report.Controllers[0].Assessment.Findings {
			counts.Rules[mode+":"+string(finding.Rule)]++
		}
	}
}
