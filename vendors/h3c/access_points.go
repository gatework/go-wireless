package h3c

import (
	"math/big"
	"reflect"
	"regexp"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
	"github.com/gatework/go-wireless/internal/record"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

var radioLabel = regexp.MustCompile(`^radio (\d+)$`)

func (p *parseState) parseAPs(text string, complete bool) map[wireless.Dimension]parser.Result {
	aps, radios, issues := []wireless.Record{}, []wireless.Record{}, []wireless.Record{}
	radioEvidence := map[uintptr]bool{}
	var ap, radio wireless.Record
	section := ""
	sectionIndent := 0
	for _, f := range readFields(text, &issues) {
		label, raw, line := f.label, f.raw, f.line
		if label == "ap name" {
			ap = wireless.Record{"name": record.Scalar(raw)}
			aps = append(aps, ap)
			radio = nil
			section = ""
		} else if m := radioLabel.FindStringSubmatch(label); m != nil {
			var apName any
			if ap != nil {
				apName = ap["name"]
			}
			radio = wireless.Record{"ap_name": apName, "radio_id": m[1]}
			radios = append(radios, radio)
			if ap != nil {
				radioEvidence[reflect.ValueOf(ap).Pointer()] = true
			}
			section = ""
		} else {
			if radio != nil && h3cRadioSections[label] != "" {
				section = h3cRadioSections[label]
				sectionIndent = f.indent
				p.assign(ensureMap(radio, "details"), keyName(label), record.Scalar(raw), &issues, line)
				continue
			}
			if section != "" && f.indent <= sectionIndent {
				section = ""
			}
			if radio != nil && section != "" && (label == "mandatory" || label == "supported" || label == "multicast" || label == "disabled") {
				p.assign(ensureMap(ensureMap(radio, "details"), section), label, record.Scalar(raw), &issues, line)
				continue
			}
			owner, mapping := ap, h3cAp
			if radio != nil && h3cRadio[label] != "" {
				owner, mapping = radio, h3cRadio
			}
			var details wireless.Record
			if h3cApDetails[label] {
				details = ap
			} else if h3cRadioDetails[label] {
				details = radio
			}
			p.assignField(owner, mapping, label, raw, &issues, line, details)
		}
	}
	valid := []wireless.Record{}
	invalid := false
	for _, r := range aps {
		if parsetext.Strip(record.StringValue(r["name"])) == "" {
			invalid = true
		} else {
			valid = append(valid, r)
		}
	}
	if invalid {
		issues = append(issues, wireless.Record{"code": "invalid_identity"})
	}
	if parser.HasDuplicates(valid, []string{"name"}) {
		issues = append(issues, wireless.Record{"code": "duplicate_identity"})
	}
	validRadios := []wireless.Record{}
	for _, r := range radios {
		if parsetext.Strip(record.StringValue(r["ap_name"])) == "" {
			issues = append(issues, wireless.Record{"code": "invalid_identity"})
		} else {
			validRadios = append(validRadios, r)
		}
	}
	total := reportedTotal(text, "APs", &issues, nil, "connected (?:(?:manual |auto |common )?APs|WTUs)|inside APs|AP licenses")
	if total != nil && total.Cmp(big.NewInt(int64(len(valid)))) != 0 {
		issues = append(issues, wireless.Record{"code": "count_mismatch", "expected": record.IntegerJSON(total), "parsed": len(valid)})
	}
	radioIssues := append([]wireless.Record{}, issues...)
	for _, r := range valid {
		if !radioEvidence[reflect.ValueOf(r).Pointer()] {
			radioIssues = append(radioIssues, wireless.Record{"code": "missing_radio_evidence", "ap_name": r["name"]})
		}
	}
	var radioTotal *big.Int
	if total != nil && total.Sign() == 0 && len(valid) == 0 {
		radioTotal = total
	}
	return map[wireless.Dimension]parser.Result{wireless.AccessPoints: parser.FieldsResult(valid, issues, complete, total, []string{"name"}), wireless.Radios: parser.FieldsResult(validRadios, radioIssues, complete, radioTotal, []string{"ap_name", "radio_id"})}
}
