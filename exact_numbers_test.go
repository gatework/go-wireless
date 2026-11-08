package wireless_test

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

func TestExactControllerIntegers(t *testing.T) {
	large := strings.Repeat("9", 80)
	p, _ := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
	o, e := p.Parse(wireless.Clients, "Total clients: 1\nMAC address: 0200-0000-0001\nAP ID: "+large+"\nOnline time: "+large+" days\n", parser.ParseOptions{Complete: true})
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
	o, e = p.Parse(wireless.Clients, "Total clients: "+large+"\n", parser.ParseOptions{Complete: true})
	if e != nil {
		t.Fatal(e)
	}
	if o.Status() != wireless.Failed || len(o.Issues()) != 1 || o.Issues()[0]["code"] != "count_mismatch" || o.Issues()[0]["expected"] != json.Number(large) {
		t.Fatal("large total must be count mismatch", o.Issues())
	}
	c, _ := vendors.NewBuiltinRegistry().Fetch("h3c", "")
	o, e = c.Parse(wireless.Configuration, "wlan ap AP model TEST\n priority "+large+"\nreturn\n", true)
	if e != nil || !o.Complete() {
		t.Fatal(o.Issues(), e)
	}
	if o.Records()[0]["access_points"].(wireless.Record)["AP"].(wireless.Record)["priority"] != json.Number(large) {
		t.Fatal("large configured integer lost")
	}
}
