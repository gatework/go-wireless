package wireless

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRubyFieldCorpus(t *testing.T) {
	data, e := os.ReadFile("testdata/ruby-field-corpus.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Method, ID, Error string
		Args              []any
		Kwargs            Record
		Expected          any
	}
	if e = decodeOracleJSON(data, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		t.Run(c.Method+"/"+c.ID, func(t *testing.T) {
			var raw *string
			if len(c.Args) > 0 && c.Args[0] != nil {
				raw = strptr(stringValue(c.Args[0]))
			}
			var actual any
			var err error
			switch c.Method {
			case "value":
				actual = scalar(NormalizeValue(stringValue(optional(raw))))
			case "mac_address":
				v, e := ParseMAC(raw)
				actual, err = scalar(v), e
			case "ip_address":
				v, e := ParseIP(raw)
				actual, err = scalar(v), e
			case "integer":
				var max *int64
				if c.Kwargs["max"] != nil {
					n, err := c.Kwargs["max"].(json.Number).Int64()
					if err != nil {
						t.Fatal(err)
					}
					max = &n
				}
				var maximum *big.Int
				if max != nil {
					maximum = big.NewInt(*max)
				}
				v, e := ParseIntegerExact(raw, maximum)
				actual, err = integerJSON(v), e
			case "vlan_id":
				v, e := ParseVLAN(raw)
				actual, err = scalar(v), e
			case "number":
				v, e := ParseNumber(raw, stringValue(c.Kwargs["unit"]))
				actual, err = scalar(v), e
			case "duration_seconds":
				v, e := ParseDurationExact(raw)
				actual, err = integerJSON(v), e
			case "rates_mbps":
				rx, tx, e := ParseRatesMbps(raw)
				actual, err = []any{scalar(rx), scalar(tx)}, e
			case "channel":
				v, e := ParseChannel(raw)
				actual, err = scalar(v), e
			case "channel_width_mhz":
				actual = integerJSON(ParseChannelWidthExact(raw))
			case "frequency_band":
				actual = scalar(FrequencyBand(stringValue(optional(raw))))
			case "online_status":
				if raw != nil {
					actual = OnlineStatus(*raw)
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
		if _, err := ParseIP(&value); err == nil {
			t.Fatal("accepted invalid host")
		}
	}
	for _, value := range []string{"1:24:00:00", "1:00:60:00", "1:00:00:60", "nonsense"} {
		if _, err := ParseDuration(&value); err == nil {
			t.Fatal("accepted invalid duration")
		}
	}
	if _, err := ParseVLAN(strptr("0")); err == nil {
		t.Fatal("accepted VLAN zero")
	}
	if _, err := ParseVLAN(strptr("4095")); err == nil {
		t.Fatal("accepted reserved VLAN")
	}
	if _, err := ParseNumber(strptr(strings.Repeat("9", 400)), "dBm"); err == nil {
		t.Fatal("accepted float overflow")
	}
	if _, _, err := ParseRatesMbps(strptr("1/" + strings.Repeat("9", 305) + " Tbps")); err == nil {
		t.Fatal("accepted scaled overflow")
	}
	if FrequencyBand("5GHz / 6GHz") != nil || FrequencyBand("802.11ax") != nil {
		t.Fatal("invented band")
	}
	if width := ParseChannelWidth(strptr("80+80MHz")); width != nil {
		t.Fatal("invented contiguous width")
	}
	if v, err := ParseNumber(strptr("0"), ""); err != nil || v == nil || math.Signbit(*v) {
		t.Fatal("lost zero")
	}
}

func TestAllTextNewlinePolicies(t *testing.T) {
	input := "A\r\nB\rC\n"
	for _, test := range []struct {
		policy NewlinePolicy
		want   string
	}{{PreserveNewlines, input}, {LFNewlines, "A\nB\nC\n"}, {DeleteCRNewlines, "A\nBC\n"}} {
		got, e := NormalizeTextWithPolicy(input, test.policy)
		if e != nil || got != test.want {
			t.Fatal("newline policy", got, e)
		}
	}
	if _, e := NormalizeTextWithPolicy(input, NewlinePolicy("unknown")); e == nil {
		t.Fatal("invalid policy")
	}
}
