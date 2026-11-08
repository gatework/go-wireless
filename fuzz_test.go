package wireless_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
	h3c "github.com/gatework/go-wireless/vendors/h3c"
)

// A successful generic decoder must remain stable through the exact Ruby
// canonical boundary, including source numeric kinds and Unicode escapes.
func FuzzJSONCanonicalRoundtrip(f *testing.F) {
	for _, seed := range []string{`{}`, `{"integer":9007199254740993,"float":-0.0}`, `{"text":"中文\\n","n":1e+30}`, `{"x":"\ud800"}`, `{} {}`, `{"n":1e9999}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1<<20 {
			t.Skip()
		}
		doc, _, err := wireless.DecodeJSONDocument([]byte(input))
		if err != nil {
			return
		}
		canonical, err := wireless.CanonicalJSON(doc)
		if err != nil {
			return
		} // decoder accepts historical values outside canonical domain
		again, _, err := wireless.DecodeJSONDocument(canonical)
		if err != nil {
			t.Fatal("canonical output rejected", err)
		}
		encoded, err := wireless.CanonicalJSON(again)
		if err != nil || !bytes.Equal(canonical, encoded) {
			t.Fatal("canonical bytes changed", err)
		}
	})
}

func FuzzRuntimeParserEvidence(f *testing.F) {
	for _, seed := range []string{"", "Total APs: 0\n", "Total APs: 1\nAP name: AP\nRadio 1:\n Channel usage: 0\n", "% Unknown command\n", "AP name: --More--\n", string([]byte{0xff}), "\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 1<<20 {
			t.Skip()
		}
		p, _ := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), "")
		observations, err := p.ParseMany([]wireless.Dimension{wireless.AccessPoints, wireless.Radios}, content, parser.ParseOptions{Complete: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range observations {
			if _, err := json.Marshal(o); err != nil {
				t.Fatal("invalid serializable evidence")
			}
			if o.Complete() && parser.TerminalInvalid(content) {
				t.Fatal("terminal damage certified complete")
			}
			if (o.Status() == wireless.Failed || o.Status() == wireless.Unsupported) && len(o.Records()) != 0 {
				t.Fatal("unavailable evidence has records")
			}
		}
	})
}
func FuzzConfigurationSource(f *testing.F) {
	for _, seed := range []string{"return\n", "wlan service-template T\n ssid A\nreturn\n", "header login ^C\nUnknown command\n^C\nreturn\n", "wlan ap AP model TEST\n radio 1\n  channel 999\nreturn\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 1<<20 {
			t.Skip()
		}
		source, err := h3c.ParseConfigurationSource(content, true)
		if err != nil {
			t.Fatal(err)
		}
		o := source.Observation()
		if _, err := json.Marshal(o); err != nil {
			t.Fatal("invalid serializable configuration")
		}
		if source.Content() != "" && source.Content() != content {
			t.Fatal("source bytes changed")
		}
		if len(source.Lines()) != len(source.States()) {
			t.Fatal("line state alignment")
		}
		for _, capture := range source.Captures() {
			if capture.FirstLine() < 1 || capture.LastLine() < capture.FirstLine() || capture.LastLine() > len(source.Lines()) {
				t.Fatal("capture outside document")
			}
		}
	})
}
