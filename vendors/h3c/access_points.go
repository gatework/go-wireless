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
	a := &apScan{state: p, aps: []wireless.Record{}, radios: []wireless.Record{}, issues: []wireless.Record{}, radioEvidence: map[uintptr]bool{}}
	for _, f := range readFields(text, &a.issues) {
		a.field(f)
	}
	return a.results(text, complete)
}

// apScan is the state of one AP listing parse, which yields both the AP and
// the radio dimension.
type apScan struct {
	state               *parseState
	aps, radios, issues []wireless.Record
	radioEvidence       map[uintptr]bool // APs that listed at least one radio
	ap, radio           wireless.Record
	section             string // the radio rate section being read
	sectionIndent       int
}

// rateLabels are the rows of a radio rate section.
var rateLabels = map[string]bool{"mandatory": true, "supported": true, "multicast": true, "disabled": true}

func (a *apScan) field(f parsedField) {
	if f.label == "ap name" {
		a.ap = wireless.Record{"name": record.Scalar(f.raw)}
		a.aps = append(a.aps, a.ap)
		a.radio, a.section = nil, ""
		return
	}
	if m := radioLabel.FindStringSubmatch(f.label); m != nil {
		var apName any
		if a.ap != nil {
			apName = a.ap["name"]
			a.radioEvidence[reflect.ValueOf(a.ap).Pointer()] = true
		}
		a.radio = wireless.Record{"ap_name": apName, "radio_id": m[1]}
		a.radios = append(a.radios, a.radio)
		a.section = ""
		return
	}
	if a.radioField(f) {
		return
	}
	owner, mapping := a.ap, h3cAP
	if a.radio != nil && h3cRadio[f.label] != "" {
		owner, mapping = a.radio, h3cRadio
	}
	var details wireless.Record
	if h3cAPDetails[f.label] {
		details = a.ap
	} else if h3cRadioDetails[f.label] {
		details = a.radio
	}
	a.state.assignField(owner, mapping, f.label, f.raw, &a.issues, f.line, details)
}

// radioField records a radio rate section heading or one of its rows and
// reports whether f was one.
func (a *apScan) radioField(f parsedField) bool {
	if a.radio != nil && h3cRadioSections[f.label] != "" {
		a.section, a.sectionIndent = h3cRadioSections[f.label], f.indent
		a.state.assign(ensureMap(a.radio, "details"), keyName(f.label), record.Scalar(f.raw), &a.issues, f.line)
		return true
	}
	if a.section != "" && f.indent <= a.sectionIndent {
		a.section = ""
	}
	if a.radio != nil && a.section != "" && rateLabels[f.label] {
		a.state.assign(ensureMap(ensureMap(a.radio, "details"), a.section), f.label, record.Scalar(f.raw), &a.issues, f.line)
		return true
	}
	return false
}

// results validates identities and counts and returns the AP and radio
// results.
func (a *apScan) results(text string, complete bool) map[wireless.Dimension]parser.Result {
	valid := []wireless.Record{}
	invalid := false
	for _, r := range a.aps {
		if parsetext.Strip(record.StringValue(r["name"])) == "" {
			invalid = true
		} else {
			valid = append(valid, r)
		}
	}
	if invalid {
		a.issues = append(a.issues, wireless.Record{"code": "invalid_identity"})
	}
	if parser.HasDuplicates(valid, []string{"name"}) {
		a.issues = append(a.issues, wireless.Record{"code": "duplicate_identity"})
	}
	validRadios := []wireless.Record{}
	for _, r := range a.radios {
		if parsetext.Strip(record.StringValue(r["ap_name"])) == "" {
			a.issues = append(a.issues, wireless.Record{"code": "invalid_identity"})
		} else {
			validRadios = append(validRadios, r)
		}
	}
	total := reportedTotal(text, "APs", &a.issues, nil, "connected (?:(?:manual |auto |common )?APs|WTUs)|inside APs|AP licenses")
	if total != nil && total.Cmp(big.NewInt(int64(len(valid)))) != 0 {
		a.issues = append(a.issues, wireless.Record{"code": "count_mismatch", "expected": record.IntegerJSON(total), "parsed": len(valid)})
	}
	radioIssues := append([]wireless.Record{}, a.issues...)
	for _, r := range valid {
		if !a.radioEvidence[reflect.ValueOf(r).Pointer()] {
			radioIssues = append(radioIssues, wireless.Record{"code": "missing_radio_evidence", "ap_name": r["name"]})
		}
	}
	var radioTotal *big.Int
	if total != nil && total.Sign() == 0 && len(valid) == 0 {
		radioTotal = total
	}
	return map[wireless.Dimension]parser.Result{
		wireless.AccessPoints: parser.FieldsResult(valid, a.issues, complete, total, []string{"name"}),
		wireless.Radios:       parser.FieldsResult(validRadios, radioIssues, complete, radioTotal, []string{"ap_name", "radio_id"}),
	}
}
