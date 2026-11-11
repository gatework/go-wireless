package wireless_test

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/internal/record"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

func TestRubyFieldCorpus(t *testing.T) {
	data, e := os.ReadFile("testdata/ruby-field-corpus.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Method, ID, Error string
		Args              []any
		Kwargs            wireless.Record
		Expected          any
	}
	if e = decodeOracleJSON(data, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		t.Run(c.Method+"/"+c.ID, func(t *testing.T) {
			var raw *string
			if len(c.Args) > 0 && c.Args[0] != nil {
				raw = new(record.StringValue(c.Args[0]))
			}
			var actual any
			var err error
			switch c.Method {
			case "value":
				actual = record.Scalar(parser.NormalizeValue(record.StringValue(record.Scalar(raw))))
			case "mac_address":
				v, e := parser.ParseMAC(raw)
				actual, err = record.Scalar(v), e
			case "ip_address":
				v, e := parser.ParseIP(raw)
				actual, err = record.Scalar(v), e
			case "integer":
				var limit *int64
				if c.Kwargs["max"] != nil {
					n, err := c.Kwargs["max"].(json.Number).Int64()
					if err != nil {
						t.Fatal(err)
					}
					limit = &n
				}
				var maximum *big.Int
				if limit != nil {
					maximum = big.NewInt(*limit)
				}
				v, e := parser.ParseIntegerExact(raw, maximum)
				actual, err = record.IntegerJSON(v), e
			case "vlan_id":
				v, e := parser.ParseVLAN(raw)
				actual, err = record.Scalar(v), e
			case "number":
				v, e := parser.ParseNumber(raw, record.StringValue(c.Kwargs["unit"]))
				actual, err = record.Scalar(v), e
			case "duration_seconds":
				v, e := parser.ParseDurationExact(raw)
				actual, err = record.IntegerJSON(v), e
			case "rates_mbps":
				rx, tx, e := parser.ParseRatesMbps(raw)
				actual, err = []any{record.Scalar(rx), record.Scalar(tx)}, e
			case "channel":
				v, e := parser.ParseChannel(raw)
				actual, err = record.Scalar(v), e
			case "channel_width_mhz":
				actual = record.IntegerJSON(parser.ParseChannelWidthExact(raw))
			case "frequency_band":
				actual = record.Scalar(parser.FrequencyBand(record.StringValue(record.Scalar(raw))))
			case "online_status":
				if raw != nil {
					actual = parser.OnlineStatus(*raw)
				}
			default:
				t.Fatal("unmapped field method")
			}
			if c.Error != "" {
				if err == nil {
					t.Fatalf("expected %s", c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(actual)
			var normalized any
			decodeOracleJSON(b, &normalized)
			if !reflect.DeepEqual(numberMeanings(normalized), numberMeanings(c.Expected)) {
				t.Fatalf("got %s want %v", b, c.Expected)
			}
		})
	}
}
func TestIndependentFieldBoundaries(t *testing.T) {
	for _, value := range []string{"bad", "192.0.2.1/24", "fe80::1%en0", "999.1.1.1"} {
		if _, err := parser.ParseIP(&value); err == nil {
			t.Fatal("accepted invalid host")
		}
	}
	for _, value := range []string{"1:24:00:00", "1:00:60:00", "1:00:00:60", "nonsense"} {
		if _, err := parser.ParseDuration(&value); err == nil {
			t.Fatal("accepted invalid duration")
		}
	}
	if _, err := parser.ParseVLAN(new("0")); err == nil {
		t.Fatal("accepted VLAN zero")
	}
	if _, err := parser.ParseVLAN(new("4095")); err == nil {
		t.Fatal("accepted reserved VLAN")
	}
	if _, err := parser.ParseNumber(new(strings.Repeat("9", 400)), "dBm"); err == nil {
		t.Fatal("accepted float overflow")
	}
	if _, _, err := parser.ParseRatesMbps(new("1/" + strings.Repeat("9", 305) + " Tbps")); err == nil {
		t.Fatal("accepted scaled overflow")
	}
	if parser.FrequencyBand("5GHz / 6GHz") != nil || parser.FrequencyBand("802.11ax") != nil {
		t.Fatal("invented band")
	}
	if width := parser.ParseChannelWidth(new("80+80MHz")); width != nil {
		t.Fatal("invented contiguous width")
	}
	if v, err := parser.ParseNumber(new("0"), ""); err != nil || v == nil || math.Signbit(*v) {
		t.Fatal("lost zero")
	}
}

func TestAllTextNewlinePolicies(t *testing.T) {
	input := "A\r\nB\rC\n"
	for _, test := range []struct {
		policy parser.NewlinePolicy
		want   string
	}{{parser.PreserveNewlines, input}, {parser.LFNewlines, "A\nB\nC\n"}, {parser.DeleteCRNewlines, "A\nBC\n"}} {
		got, e := parser.NormalizeTextWithPolicy(input, test.policy)
		if e != nil || got != test.want {
			t.Fatal("newline policy", got, e)
		}
	}
	if _, e := parser.NormalizeTextWithPolicy(input, parser.NewlinePolicy("unknown")); e == nil {
		t.Fatal("invalid policy")
	}
}
