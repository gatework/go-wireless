package h3c

import (
	"regexp"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
	"github.com/gatework/go-wireless/internal/record"
	fields "github.com/gatework/go-wireless/vendors/internal/fields"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

var hardwareVersionLabel = regexp.MustCompile(`^hardware version (.+)$`)
var hardwareVersionSource = regexp.MustCompile(`(?i)^Hardware version (.+?)\s*:`)

func (p *parseState) parseModels(text string, complete bool) parser.Result {
	records, issues := []wireless.Record{}, []wireless.Record{}
	lines := parser.SourceLines(text)
	var model, section wireless.Record
	mapping := h3cModel
	for _, f := range readFields(text, &issues) {
		if f.label == "ap model" {
			if f.raw == nil {
				issues = append(issues, wireless.Record{"code": "invalid_field", "line": f.line})
				continue
			}
			model = wireless.Record{"model": *f.raw, "radios": []wireless.Record{}, "versions": []wireless.Record{}}
			records = append(records, model)
			section, mapping = model, h3cModel
		} else if m := radioLabel.FindStringSubmatch(f.label); model != nil && m != nil && f.raw == nil {
			id, e := parser.ParseIntegerExact(&m[1], nil)
			if e != nil {
				issues = append(issues, wireless.Record{"code": "invalid_field", "line": f.line})
				continue
			}
			section = wireless.Record{"radio_id": id.String()}
			model["radios"] = append(model["radios"].([]wireless.Record), section)
			mapping = h3cModelRadio
		} else if model != nil && f.label == "version support list" && f.raw == nil {
			section, mapping = nil, h3cModelVersion
		} else if m := hardwareVersionLabel.FindStringSubmatch(f.label); model != nil && m != nil && f.raw == nil {
			m = hardwareVersionSource.FindStringSubmatch(parsetext.Strip(lines[f.line-1]))
			var version any
			if m != nil {
				version = m[1]
			}
			section = wireless.Record{"hardware_version": version}
			model["versions"] = append(model["versions"].([]wireless.Record), section)
			mapping = h3cModelVersion
		} else if field := mapping[f.label]; section != nil && field != "" {
			v, e := convertModelField(field, f.raw)
			if e != nil {
				issues = append(issues, wireless.Record{"code": "invalid_field", "line": f.line})
			} else {
				p.assign(section, field, v, &issues, f.line)
			}
		} else {
			issues = append(issues, wireless.Record{"code": "unrecognized_field", "line": f.line})
		}
	}
	for _, r := range records {
		radios := r["radios"].([]wireless.Record)
		incomplete := r["radio_count"] == nil || record.StringValue(r["radio_count"]) != record.StringValue(len(radios))
		for _, radio := range radios {
			for _, key := range []string{"modes", "default_mode", "bss_count"} {
				if radio[key] == nil {
					incomplete = true
				}
			}
		}
		if incomplete {
			issues = append(issues, wireless.Record{"code": "incomplete_model", "model": r["model"]})
		}
		for _, version := range r["versions"].([]wireless.Record) {
			bad := false
			for _, key := range h3cModelVersion {
				if version[key] == nil {
					bad = true
				}
			}
			if bad {
				issues = append(issues, wireless.Record{"code": "incomplete_version", "model": r["model"]})
				break
			}
		}
		if parser.HasDuplicates(radios, []string{"radio_id"}) {
			issues = append(issues, wireless.Record{"code": "duplicate_identity", "model": r["model"]})
		}
		if parser.HasDuplicates(r["versions"].([]wireless.Record), []string{"hardware_version"}) {
			issues = append(issues, wireless.Record{"code": "duplicate_identity", "model": r["model"]})
		}
	}
	total := reportedTotal(text, "AP models", &issues, nil, "")
	return parser.FieldsResult(records, issues, complete, total, []string{"model"})
}
func convertModelField(field string, raw *string) (any, error) {
	switch field {
	case "vendor_id", "license_weight", "license_type", "radio_count", "bss_count":
		return fields.IntegerValue(raw)
	case "modes":
		if raw == nil {
			return nil, nil
		}
		return regexp.MustCompile(`\s*,\s*`).Split(*raw, -1), nil
	case "type":
		if raw == nil {
			return nil, nil
		}
		return regexp.MustCompile(`^:\s*`).ReplaceAllString(*raw, ""), nil
	}
	return record.Scalar(raw), nil
}
