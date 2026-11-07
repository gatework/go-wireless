package wireless

import ()

var hardwareVersionLabel = rx(`^hardware version (.+)$`)
var hardwareVersionSource = rx(`(?i)^Hardware version (.+?)\s*:`)

func (p *parseState) parseModels(text string, complete bool) parseResult {
	records, issues := []Record{}, []Record{}
	lines := sourceLines(text)
	var model, section Record
	mapping := h3cModel
	for _, f := range readFields(text, &issues) {
		if f.label == "ap model" {
			if f.raw == nil {
				issues = append(issues, Record{"code": "invalid_field", "line": f.line})
				continue
			}
			model = Record{"model": *f.raw, "radios": []Record{}, "versions": []Record{}}
			records = append(records, model)
			section, mapping = model, h3cModel
		} else if m := radioLabel.FindStringSubmatch(f.label); model != nil && m != nil && f.raw == nil {
			id, e := ParseIntegerExact(&m[1], nil)
			if e != nil {
				issues = append(issues, Record{"code": "invalid_field", "line": f.line})
				continue
			}
			section = Record{"radio_id": id.String()}
			model["radios"] = append(model["radios"].([]Record), section)
			mapping = h3cModelRadio
		} else if model != nil && f.label == "version support list" && f.raw == nil {
			section, mapping = nil, h3cModelVersion
		} else if m := hardwareVersionLabel.FindStringSubmatch(f.label); model != nil && m != nil && f.raw == nil {
			m = hardwareVersionSource.FindStringSubmatch(strip(lines[f.line-1]))
			var version any
			if m != nil {
				version = m[1]
			}
			section = Record{"hardware_version": version}
			model["versions"] = append(model["versions"].([]Record), section)
			mapping = h3cModelVersion
		} else if field := mapping[f.label]; section != nil && field != "" {
			v, e := convertModelField(field, f.raw)
			if e != nil {
				issues = append(issues, Record{"code": "invalid_field", "line": f.line})
			} else {
				p.assign(section, field, v, &issues, f.line)
			}
		} else {
			issues = append(issues, Record{"code": "unrecognized_field", "line": f.line})
		}
	}
	for _, r := range records {
		radios := r["radios"].([]Record)
		incomplete := r["radio_count"] == nil || stringValue(r["radio_count"]) != stringValue(len(radios))
		for _, radio := range radios {
			for _, key := range []string{"modes", "default_mode", "bss_count"} {
				if radio[key] == nil {
					incomplete = true
				}
			}
		}
		if incomplete {
			issues = append(issues, Record{"code": "incomplete_model", "model": r["model"]})
		}
		for _, version := range r["versions"].([]Record) {
			bad := false
			for _, key := range h3cModelVersion {
				if version[key] == nil {
					bad = true
				}
			}
			if bad {
				issues = append(issues, Record{"code": "incomplete_version", "model": r["model"]})
				break
			}
		}
		if hasDuplicates(radios, []string{"radio_id"}) {
			issues = append(issues, Record{"code": "duplicate_identity", "model": r["model"]})
		}
		if hasDuplicates(r["versions"].([]Record), []string{"hardware_version"}) {
			issues = append(issues, Record{"code": "duplicate_identity", "model": r["model"]})
		}
	}
	total := reportedTotal(text, "AP models", &issues, nil, "")
	return fieldsResult(records, issues, complete, total, []string{"model"})
}
func convertModelField(field string, raw *string) (any, error) {
	switch field {
	case "vendor_id", "license_weight", "license_type", "radio_count", "bss_count":
		return integerValue(raw)
	case "modes":
		if raw == nil {
			return nil, nil
		}
		return rx(`\s*,\s*`).Split(*raw, -1), nil
	case "type":
		if raw == nil {
			return nil, nil
		}
		return rx(`^:\s*`).ReplaceAllString(*raw, ""), nil
	}
	return optional(raw), nil
}
