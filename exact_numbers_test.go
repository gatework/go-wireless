package wireless

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

func TestExactControllerIntegers(t *testing.T) {
	large := strings.Repeat("9", 80)
	p, _ := NewRuntimeParser("")
	o, e := p.Parse(Clients, "Total clients: 1\nMAC address: 0200-0000-0001\nAP ID: "+large+"\nOnline time: "+large+" days\n", ParseOptions{Complete: true})
	if e != nil {
		t.Fatal(e)
	}
	if !o.Complete() {
		t.Fatal(o.Issues())
	}
	if o.Records()[0]["ap_id"] != large {
		t.Fatal("large identity lost")
	}
	want, _ := new(big.Int).SetString(large, 10)
	want.Mul(want, big.NewInt(86400))
	if o.Records()[0]["online_seconds"] != json.Number(want.String()) {
		t.Fatal("large duration lost")
	}
	o, e = p.Parse(Clients, "Total clients: "+large+"\n", ParseOptions{Complete: true})
	if e != nil {
		t.Fatal(e)
	}
	if o.Status() != Failed || len(o.Issues()) != 1 || o.Issues()[0]["code"] != "count_mismatch" || o.Issues()[0]["expected"] != json.Number(large) {
		t.Fatal("large total must be count mismatch", o.Issues())
	}
	c, _ := NewConfig("", "")
	o, e = c.Parse("wlan ap AP model TEST\n priority "+large+"\nreturn\n", true)
	if e != nil || !o.Complete() {
		t.Fatal(o.Issues(), e)
	}
	if o.Records()[0]["access_points"].(Record)["AP"].(Record)["priority"] != json.Number(large) {
		t.Fatal("large configured integer lost")
	}
}
