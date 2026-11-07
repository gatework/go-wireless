package wireless

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestRubyRecommendations(t *testing.T) {
	b, e := os.ReadFile("testdata/ruby-recommendations.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Cases []struct {
			Input  string `json:"input_json"`
			Advice json.RawMessage
		}
	}
	if e = json.Unmarshal(b, &corpus); e != nil {
		t.Fatal(e)
	}
	for i, c := range corpus.Cases {
		doc, order, e := DecodeReviewJSON([]byte(c.Input))
		if e != nil {
			t.Fatal(i, e)
		}
		got := Recommendations(doc, order)
		d := json.NewDecoder(bytes.NewReader(c.Advice))
		d.UseNumber()
		var expected any
		d.Decode(&expected)
		gb, e := CanonicalJSON(got)
		if e != nil {
			t.Fatal(e)
		}
		eb, _ := CanonicalJSON(expected)
		if !bytes.Equal(gb, eb) {
			t.Fatalf("case %d\ngot %s\nwant%s", i, gb, eb)
		}
	}
}
func TestReviewRejectsMalformedContainers(t *testing.T) {
	for _, s := range []string{`{"device":{"name":"a","host":"x"}}`, `{"device":{"name":"a","host":"x"},"snapshot":{}}`, `{"device":{"name":"a","host":"x"},"snapshot":{"dimensions":{"configuration":{"status":"complete","records":[{"access_points":null}]}}}}`, `{"device":{"name":"a","host":"x"},"snapshot":{"dimensions":{"configuration":{"status":"complete","records":[],"issues":null}}}}`, `{"device":{"name":"a","host":"x"},"snapshot":null} {}`} {
		if _, _, e := DecodeReviewJSON([]byte(s)); e == nil {
			t.Fatal("accepted", s)
		}
	}
}

func TestRecommendationsOwnReturnedEvidence(t *testing.T) {
	doc, order, e := DecodeReviewJSON([]byte(`{"device":{"name":"original","host":"192.0.2.1","platform":"h3c_wireless"},"snapshot":{"dimensions":{"client_ip_conflicts":{"status":"complete","records":[{"ip":"192.0.2.9","nested":{"mac":"original"}}]}}}}`))
	if e != nil {
		t.Fatal(e)
	}
	advice := Recommendations(doc, order)
	source := recordList(asRecord(asRecord(asRecord(doc["snapshot"])["dimensions"])["client_ip_conflicts"])["records"])[0]
	source["ip"] = "changed"
	asRecord(source["nested"])["mac"] = "changed"
	item := recordList(advice["items"])[0]
	evidence := recordList(item["evidence"])[0]
	if evidence["ip"] != "192.0.2.9" || asRecord(evidence["nested"])["mac"] != "original" {
		t.Fatal("input mutation changed returned evidence")
	}
	asRecord(advice["device"])["name"] = "returned"
	asRecord(evidence["nested"])["mac"] = "returned"
	if asRecord(doc["device"])["name"] != "original" || asRecord(source["nested"])["mac"] != "changed" {
		t.Fatal("returned evidence mutation changed input")
	}
}
