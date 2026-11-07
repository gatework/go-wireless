package wireless

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestJSONPreservesParsedFloatKinds(t *testing.T) {
	body, err := os.ReadFile("testdata/synthetic-complete-configuration.cfg")
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "max-power 20.5", "max-power 20", 1))
	p, _ := NewConfig("h3c", "")
	observation, err := p.Parse(string(body), true)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte(`"max_power":20.0`)) {
		t.Fatalf("float max_power was lost: %s", wire)
	}
	var decoded Observation
	if err = json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(decoded)
	if !bytes.Equal(wire, again) {
		t.Fatal("numeric tokens changed on roundtrip")
	}
}
func TestJSONFloatAndIntegerIntent(t *testing.T) {
	b, e := MarshalJSON(Record{"integer": 20, "float": float64(20), "zero": math.Copysign(0, -1), "number": json.Number("2e1")})
	if e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{`"integer":20,`, `"float":20.0`, `"zero":-0.0`, `"number":2e1`} {
		if !strings.Contains(string(b), token) {
			t.Errorf("missing %s in %s", token, b)
		}
	}
	if _, e = MarshalJSON(Record{"bad": math.Inf(1)}); e == nil {
		t.Fatal("accepted infinity")
	}
}
func TestCoreJSONExportsRetainRealFloatAndNegativeZero(t *testing.T) {
	body, e := os.ReadFile("testdata/float-kind-configuration.cfg")
	if e != nil {
		t.Fatal(e)
	}
	p, _ := NewConfig("h3c", "")
	observation, e := p.Parse(string(body), true)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := NewSnapshot(SnapshotOptions{Target: Record{"host": "192.0.2.10"}, Dimensions: map[Dimension]Observation{Configuration: observation}, ObservedAt: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
	exports := []any{observation, observation.ToMap(), snapshot, snapshot.ToMap()}
	for _, value := range exports {
		wire, e := MarshalJSON(value)
		if e != nil {
			t.Fatal(e)
		}
		for _, token := range []string{`"max_power":20.0`, `"rssi":-0.0`, `"mandatory":[1.0,5.5]`, `"supported":[2.0,11.0]`} {
			if !bytes.Contains(wire, []byte(token)) {
				t.Fatal("numeric kind lost", token, string(wire))
			}
		}
	}
}
