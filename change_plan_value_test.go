package wireless_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
)

func changePlanDocument(t *testing.T) wireless.Record {
	t.Helper()
	body, err := os.ReadFile("testdata/ruby-interop/security.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	document, err := wireless.DecodeJSONDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func changePlanConfigurations(t *testing.T) (wireless.Record, wireless.Record) {
	t.Helper()
	body, err := os.ReadFile("testdata/ruby-interop/source.cfg")
	if err != nil {
		t.Fatal(err)
	}
	parser, err := vendors.NewBuiltinRegistry().Fetch("h3c_wireless", "")
	if err != nil {
		t.Fatal(err)
	}
	parse := func(body string) wireless.Record {
		observation, err := parser.Parse(wireless.Configuration, body, true)
		if err != nil || observation.Status() != wireless.Complete {
			t.Fatalf("configuration fixture: %v, %v", observation, err)
		}
		return observation.Records()[0]
	}
	return parse(string(body)), parse(strings.ReplaceAll(strings.ReplaceAll(string(body), " cipher-suite tkip\n", ""), " security-ie wpa\n", ""))
}

func TestChangePlanOwnsDocumentActionAndRenderedCommands(t *testing.T) {
	document := changePlanDocument(t)
	document["metadata"] = wireless.Record{"values": []any{wireless.Record{"counter": json.Number("9007199254740993")}}}
	wantJSON, err := wireless.MarshalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	wantCommands, _ := record.Strings(document["commands"])
	wantRollback, _ := record.Strings(document["rollback"])
	plan, err := change.NewChangePlan(document)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(value wireless.Record) {
		value["device"].(wireless.Record)["host"] = "192.0.2.99"
		value["expected_config_sha256"] = strings.Repeat("0", 64)
		value["change"].(wireless.Record)["enabled"] = false
		value["change"].(wireless.Record)["cipher_suites"].([]any)[0] = "other"
		value["commands"].([]any)[0] = "save force"
		value["rollback"].([]any)[0] = "save force"
		value["metadata"].(wireless.Record)["values"].([]any)[0].(wireless.Record)["counter"] = 0
	}
	mutate(document)
	mutate(plan.ToMap())
	plan.Device()["host"] = "192.0.2.99"
	plan.Commands()[0] = "save force"
	plan.Rollback()[0] = "save force"
	duplicate := plan
	gotJSON, err := wireless.MarshalJSON(duplicate)
	if err != nil || !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("plan changed through an alias: %s, %v", gotJSON, err)
	}
	if duplicate.Host() != "192.0.2.10" || !reflect.DeepEqual(duplicate.Commands(), wantCommands) || !reflect.DeepEqual(duplicate.Rollback(), wantRollback) {
		t.Fatal("rendered execution changed through an alias")
	}
	before, after := changePlanConfigurations(t)
	if err := duplicate.CheckBefore(before); err != nil {
		t.Fatal("original action or fingerprint changed", err)
	}
	fingerprint, err := duplicate.CheckAfter(after)
	wantFingerprint, _ := wireless.Fingerprint(after)
	if err != nil || fingerprint != wantFingerprint {
		t.Fatal("readback verification changed", fingerprint, err)
	}
	if duplicate.CheckBefore(after) == nil {
		t.Fatal("accepted drift from original configuration")
	}
	if _, err := duplicate.CheckAfter(before); err == nil {
		t.Fatal("accepted unapplied readback")
	}
}

func TestChangePlanSchemaIsExactlyOneAndPreservesJSONNumber(t *testing.T) {
	for _, number := range []string{"1", "1.0", "1e0", "1.000E+000", "10e-1", "100.0e-2", "0.001e3", "0.10e+1", "1e-0"} {
		t.Run(number, func(t *testing.T) {
			document := changePlanDocument(t)
			document["schema_version"] = json.Number(number)
			plan, err := change.NewChangePlan(document)
			if err != nil {
				t.Fatal("rejected exact one", err)
			}
			body, err := wireless.MarshalJSON(plan)
			if err != nil || !bytes.Contains(body, []byte(`"schema_version":`+number+`,`)) {
				t.Fatal("JSON numeric representation changed", string(body), err)
			}
			if plan.ToMap()["schema_version"] != json.Number(number) {
				t.Fatal("schema lexeme changed")
			}
		})
	}
	for _, value := range []any{
		json.Number("1.0000000000000001"), json.Number("0.99999999999999999"),
		json.Number("10000000000000001e-16"), json.Number("1e9999999999999999999999"),
		json.Number("1e-9999999999999999999999"), json.Number("0.1"), json.Number("-1"),
		json.Number("0"), json.Number("2"), json.Number("01"), json.Number("1e"),
		math.Nextafter(1, 2), math.Nextafter(1, 0), math.Inf(1), math.NaN(), nil, "1", true,
	} {
		document := changePlanDocument(t)
		document["schema_version"] = value
		if _, err := change.NewChangePlan(document); !errors.Is(err, change.ErrChangePlan) {
			t.Fatalf("accepted invalid schema %v (%T)", value, value)
		}
	}
}

func TestChangePlanRejectsZeroAndUncopyableDocuments(t *testing.T) {
	var zero change.ChangePlan
	if err := zero.CheckBefore(wireless.Record{}); !errors.Is(err, change.ErrChangePlan) {
		t.Fatal("zero plan passed precheck", err)
	}
	if _, err := zero.CheckAfter(wireless.Record{}); !errors.Is(err, change.ErrChangePlan) {
		t.Fatal("zero plan passed readback", err)
	}
	if _, err := json.Marshal(zero); !errors.Is(err, change.ErrChangePlan) {
		t.Fatal("zero plan serialized", err)
	}
	if _, err := change.NewChangePlan(nil); !errors.Is(err, change.ErrChangePlan) {
		t.Fatal("nil plan accepted", err)
	}
	for _, uncopyable := range []any{func() {}, make(chan string)} {
		document := changePlanDocument(t)
		document["metadata"] = uncopyable
		if _, err := change.NewChangePlan(document); !errors.Is(err, change.ErrChangePlan) {
			t.Fatal("accepted unowned value", err)
		}
	}
	document := changePlanDocument(t)
	document["metadata"] = document
	if _, err := change.NewChangePlan(document); !errors.Is(err, change.ErrChangePlan) {
		t.Fatal("accepted cycle", err)
	}
}
