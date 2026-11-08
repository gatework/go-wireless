package report

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
	review "github.com/gatework/go-wireless/review"
)

func TestReviewRubyFullConfiguration(t *testing.T) {
	body, e := os.ReadFile("../testdata/ruby-interop/device.json")
	if e != nil {
		t.Fatal(e)
	}
	doc, order, e := review.DecodeReviewJSON(body)
	if e != nil {
		t.Fatal(e)
	}
	r, e := New(filepath.Join(t.TempDir(), "private"), Options{})
	if e != nil {
		t.Fatal(e)
	}
	advice := review.Recommendations(doc, order)
	_, e = r.WriteReview(context.Background(), doc, "ruby", "source.json", fmt.Sprintf("%x", sha256.Sum256(body)), advice, true)
	if e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(filepath.Join(r.Directory(), "recommendations/*.plan.json"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	planBody, _ := os.ReadFile(files[0])
	plan, _, e := wireless.DecodeJSONDocument(planBody)
	if e != nil {
		t.Fatal(e)
	}
	if plan["expected_config_sha256"] != "294aea47c2657702a541a4d791b3376d5a96c3a9bcadbda1a606d64545d5d573" {
		t.Fatal(plan)
	}
	for _, suffix := range []string{"suggested.cfg", "rollback.cfg"} {
		b, _ := os.ReadFile(filepath.Join(r.Directory(), "recommendations/ruby."+suffix))
		for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			if !strings.HasPrefix(line, "! ") {
				t.Fatal("executable script", line)
			}
		}
	}
}

func TestHistoricalExponentNameRemainsAdvisoryOnly(t *testing.T) {
	doc, order, e := review.DecodeReviewJSON([]byte(`{"device":{"name":"wlc","host":"192.0.2.1","platform":"h3c_wireless"},"snapshot":{"dimensions":{"configuration":{"status":"complete","records":[{"service_templates":[{"name":1e+3,"enabled":true,"configuration":{"cipher_suites":["ccmp","tkip"],"security_ies":["rsn","wpa"]}}]}]}}}}`))
	if e != nil {
		t.Fatal(e)
	}
	advice := review.Recommendations(doc, order)
	items := adviceItems(advice)
	if len(items) != 1 || items[0]["status"] != "candidate" || items[0]["scope"] != "service-template 1000.0" {
		t.Fatal(advice)
	}
	if _, _, e = change.RenderChange(mapping(items[0]["change"])); e == nil {
		t.Fatal("numeric name widened typed action")
	}
	r, e := New(filepath.Join(t.TempDir(), "private"), Options{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.WriteReview(context.Background(), doc, "history", "source.json", strings.Repeat("0", 64), advice, true); e != nil {
		t.Fatal(e)
	}
	plans, e := filepath.Glob(filepath.Join(r.Directory(), "recommendations", "*.plan.json"))
	if e != nil || len(plans) != 0 {
		t.Fatal("historical scalar became applicable plan", plans, e)
	}
}
