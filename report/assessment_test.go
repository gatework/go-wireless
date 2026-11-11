package report

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/go-netops/assess"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/wlan"
)

const tkipDocument = `{"device":{"name":"wlc","host":"192.0.2.1","platform":"h3c_wireless"},"snapshot":{"observed_at":"2026-11-10T00:00:00Z","dimensions":{"configuration":{"status":"complete","records":[{"service_templates":[{"name":"corp","enabled":true,"configuration":{"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}}]}]}}}}`

func assessed(t *testing.T, body string) (wireless.Record, *wlan.Controller, rules.ControllerAssessment) {
	t.Helper()
	doc, err := wireless.DecodeJSONDocument([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	c, err := wlan.DecodeDocument([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	a, err := rules.NewAssessor(assess.Policy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Assess(context.Background(), []*wlan.Controller{c})
	if err != nil {
		t.Fatal(err)
	}
	return doc, c, r.Controllers[0]
}

func published(t *testing.T, r *Report) map[string]string {
	t.Helper()
	out := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(r.Directory(), "recommendations", "*"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = string(b)
	}
	return out
}

func TestWriteAssessmentWithoutScriptsPublishesNothingExecutable(t *testing.T) {
	doc, c, a := assessed(t, tkipDocument)
	r, err := New(filepath.Join(t.TempDir(), "out"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := r.WriteAssessment(context.Background(), doc, c, a, "wlc", "source.json", "00", false)
	if err != nil {
		t.Fatal(err)
	}
	files := published(t, r)
	for name, body := range files {
		if strings.HasSuffix(name, ".plan.json") || strings.HasSuffix(name, ".cfg") {
			t.Errorf("published %s without scripts", name)
		}
		if strings.Contains(body, "undo cipher-suite") {
			t.Errorf("%s carries a command without scripts", name)
		}
	}
	if !strings.Contains(files["wlc.txt"], "wireless.legacy_wlan_security") || entry["highest"] != "medium" {
		t.Fatal(entry, files["wlc.txt"])
	}
	// The caller's assessment is not modified.
	if a.Assessment.Findings[0].Remediation.Change == nil {
		t.Fatal("caller assessment stripped")
	}
}

func TestWriteAssessmentPlansOnlyEligibleCandidates(t *testing.T) {
	doc, c, a := assessed(t, tkipDocument)
	r, err := New(filepath.Join(t.TempDir(), "out"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteAssessment(context.Background(), doc, c, a, "wlc", "source.json", "00", true); err != nil {
		t.Fatal(err)
	}
	files := published(t, r)
	var plan map[string]any
	if err := json.Unmarshal([]byte(files["wlc.1.plan.json"]), &plan); err != nil {
		t.Fatal(files)
	}
	if plan["scope"] != "service-template corp" || plan["platform"] != "h3c_wireless" {
		t.Fatal(plan)
	}
	for _, suffix := range []string{"suggested.cfg", "rollback.cfg"} {
		for line := range strings.SplitSeq(strings.TrimSuffix(files["wlc."+suffix], "\n"), "\n") {
			if !strings.HasPrefix(line, "! ") {
				t.Fatalf("%s has executable line %q", suffix, line)
			}
		}
	}
	// A configuration that changed after assessment yields no plan.
	c.Configuration.ServiceTemplates[0].Configuration.CipherSuites = []string{"ccmp"}
	r2, _ := New(filepath.Join(t.TempDir(), "out"), Options{})
	if _, err := r2.WriteAssessment(context.Background(), doc, c, a, "wlc", "source.json", "00", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := published(t, r2)["wlc.1.plan.json"]; ok {
		t.Fatal("plan for ineligible candidate")
	}
}

func TestWriteAssessmentRejectsUnsafeName(t *testing.T) {
	doc, c, a := assessed(t, tkipDocument)
	r, _ := New(filepath.Join(t.TempDir(), "out"), Options{})
	if _, err := r.WriteAssessment(context.Background(), doc, c, a, "../x", "s", "00", true); !errors.Is(err, ErrReviewName) {
		t.Fatal(err)
	}
}

func TestDecodeDocumentEnvelope(t *testing.T) {
	for _, bad := range []string{
		`{"device":{"host":"192.0.2.1"},"snapshot":null}`,
		`{"device":{"name":"a","host":"192.0.2.1"}}`,
		`{"device":{"name":"a","host":"192.0.2.1"},"snapshot":{}}`,
		`{"device":{"name":"a","host":"192.0.2.1"},"snapshot":{"dimensions":{"configuration":{"status":"complete","records":null}}}}`,
		`{"device":{"name":"a","host":"192.0.2.1"},"snapshot":{"dimensions":{"configuration":{"status":"done","records":[]}}}}`,
		`{"device":{"name":"a","host":"192.0.2.1"},"snapshot":{"dimensions":{"configuration":{"status":"complete","records":[1]}}}}`,
		`{"device":{"name":"a","host":"192.0.2.1"},"snapshot":{"dimensions":{"configuration":{"status":"complete","records":[],"issues":["x"]}}}}`,
	} {
		if _, err := wlan.DecodeDocument([]byte(bad)); !errors.Is(err, wlan.ErrDocument) {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := wlan.DecodeDocument([]byte(`{"device":{"name":"a","host":"192.0.2.1"},"snapshot":null}`)); err != nil {
		t.Fatal(err)
	}
}
