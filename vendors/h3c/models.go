package h3c

import (
	"regexp"
	"slices"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
	"github.com/gatework/go-wireless/internal/record"
	fields "github.com/gatework/go-wireless/vendors/internal/fields"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

var hardwareVersionLabel = regexp.MustCompile(`^hardware version (.+)$`)
var hardwareVersionSource = regexp.MustCompile(`(?i)^Hardware version (.+?)\s*:`)

func (p *parseState) parseModels(text string, complete bool) parser.Result {
	m := &modelScan{state: p, lines: parser.SourceLines(text), records: []wireless.Record{}, issues: []wireless.Record{}, mapping: h3cModel}
	for _, f := range readFields(text, &m.issues) {
		m.field(f)
	}
	for _, r := range m.records {
		m.issues = append(m.issues, modelIssues(r)...)
	}
	total := reportedTotal(text, "AP models", &m.issues, nil, "")
	return parser.FieldsResult(m.records, m.issues, complete, total, []string{"model"})
}

// modelScan is the state of one AP model listing parse: the current model,
// the section (model, radio or version) fields go to, and its label mapping.
type modelScan struct {
	state           *parseState
	lines           []string
	records, issues []wireless.Record
	model, section  wireless.Record
	mapping         map[string]string
}

func (m *modelScan) issue(code string, line int) {
	m.issues = append(m.issues, wireless.Record{"code": code, "line": line})
}

// field consumes one labeled field.
func (m *modelScan) field(f parsedField) {
	if f.label == "ap model" {
		if f.raw == nil {
			m.issue("invalid_field", f.line)
			return
		}
		m.model = wireless.Record{"model": *f.raw, "radios": []wireless.Record{}, "versions": []wireless.Record{}}
		m.records = append(m.records, m.model)
		m.section, m.mapping = m.model, h3cModel
		return
	}
	if m.model != nil && f.raw == nil && m.openSection(f) {
		return
	}
	if field := m.mapping[f.label]; m.section != nil && field != "" {
		v, e := convertModelField(field, f.raw)
		if e != nil {
			m.issue("invalid_field", f.line)
			return
		}
		m.state.assign(m.section, field, v, &m.issues, f.line)
		return
	}
	m.issue("unrecognized_field", f.line)
}

// openSection handles a value-less heading inside a model: a radio, the
// version support list or one hardware version. It reports whether f was one.
func (m *modelScan) openSection(f parsedField) bool {
	if id := radioLabel.FindStringSubmatch(f.label); id != nil {
		n, e := parser.ParseIntegerExact(&id[1], nil)
		if e != nil {
			m.issue("invalid_field", f.line)
			return true
		}
		m.section = wireless.Record{"radio_id": n.String()}
		radios, _ := m.model["radios"].([]wireless.Record)
		m.model["radios"] = append(radios, m.section)
		m.mapping = h3cModelRadio
		return true
	}
	if f.label == "version support list" {
		m.section, m.mapping = nil, h3cModelVersion
		return true
	}
	if hardwareVersionLabel.MatchString(f.label) {
		var version any
		if v := hardwareVersionSource.FindStringSubmatch(parsetext.Strip(m.lines[f.line-1])); v != nil {
			version = v[1]
		}
		m.section = wireless.Record{"hardware_version": version}
		versions, _ := m.model["versions"].([]wireless.Record)
		m.model["versions"] = append(versions, m.section)
		m.mapping = h3cModelVersion
		return true
	}
	return false
}

// modelIssues checks one model for missing radio or version facts and
// duplicate radios or versions.
func modelIssues(r wireless.Record) []wireless.Record {
	var out []wireless.Record
	radios, _ := r["radios"].([]wireless.Record)
	versions, _ := r["versions"].([]wireless.Record)
	incomplete := r["radio_count"] == nil || record.StringValue(r["radio_count"]) != record.StringValue(len(radios))
	for _, radio := range radios {
		for _, key := range []string{"modes", "default_mode", "bss_count"} {
			incomplete = incomplete || radio[key] == nil
		}
	}
	if incomplete {
		out = append(out, wireless.Record{"code": "incomplete_model", "model": r["model"]})
	}
	if slices.ContainsFunc(versions, incompleteVersion) {
		out = append(out, wireless.Record{"code": "incomplete_version", "model": r["model"]})
	}
	if parser.HasDuplicates(radios, []string{"radio_id"}) {
		out = append(out, wireless.Record{"code": "duplicate_identity", "model": r["model"]})
	}
	if parser.HasDuplicates(versions, []string{"hardware_version"}) {
		out = append(out, wireless.Record{"code": "duplicate_identity", "model": r["model"]})
	}
	return out
}

func incompleteVersion(version wireless.Record) bool {
	for _, key := range h3cModelVersion {
		if version[key] == nil {
			return true
		}
	}
	return false
}

// leadingColon is a value-side colon some model fields repeat.
var leadingColon = regexp.MustCompile(`^:\s*`)

// modeSeparator splits a radio's supported mode list.
var modeSeparator = regexp.MustCompile(`\s*,\s*`)

//nolint:nilnil // an absent field converts to JSON null
func convertModelField(field string, raw *string) (any, error) {
	switch field {
	case "vendor_id", "license_weight", "license_type", "radio_count", "bss_count":
		return fields.IntegerValue(raw)
	case "modes":
		if raw == nil {
			return nil, nil
		}
		return modeSeparator.Split(*raw, -1), nil
	case "type":
		if raw == nil {
			return nil, nil
		}
		return leadingColon.ReplaceAllString(*raw, ""), nil
	}
	return record.Scalar(raw), nil
}
