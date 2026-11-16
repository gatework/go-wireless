package wlan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/wlan"
)

func TestServerReferencePrivacyAndWireShapes(t *testing.T) {
	for _, wire := range []string{`"named-server"`, `true`, `false`, `null`} {
		t.Run(wire, func(t *testing.T) {
			var ref wlan.ServerReference
			if err := json.Unmarshal([]byte(wire), &ref); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(ref)
			if err != nil || string(data) != wire {
				t.Fatal("server reference wire shape changed")
			}
		})
	}
	url := "https://synthetic:credential@private.invalid/token"
	for _, field := range []string{"apply_web_server", "apply_mac_trigger_server"} {
		t.Run(field, func(t *testing.T) {
			var portal wlan.Portal
			wire := `{"` + field + `":"` + url + `"}`
			err := json.Unmarshal([]byte(wire), &portal)
			if err == nil || strings.Contains(err.Error(), url) {
				t.Fatal("URL name accepted or leaked in diagnostic")
			}
		})
	}
	ref := wlan.ServerReference{Name: &url}
	if _, err := json.Marshal(ref); err == nil || strings.Contains(err.Error(), url) {
		t.Fatal("URL name emitted or leaked in diagnostic")
	}
}

func FuzzServerReference(f *testing.F) {
	for _, seed := range []string{`"named-server"`, `true`, `false`, `null`, `"https://synthetic:credential@private.invalid/token"`, `{}`, `1`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var ref wlan.ServerReference
		if err := json.Unmarshal([]byte(input), &ref); err != nil {
			return
		}
		encoded, err := json.Marshal(ref)
		if err != nil || strings.Contains(string(encoded), "://") {
			t.Fatal("accepted server reference cannot be safely encoded")
		}
	})
}
