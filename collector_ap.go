package wireless

import "context"

const LLDPCommand = "display lldp neighbor-information verbose"

func (c *Collector) collectNeighbors(ctx context.Context, session Session, aps Observation, names []string, credentials APCredentialsSource) Observation {
	if !aps.Available() {
		return c.unavailable(LLDP, Failed, "access_points_unavailable", nil)
	}
	start := c.clock()
	records, issues, evidence := []Record{}, []Record{}, []Record{}
	selected := []Record{}
	wanted := map[string]bool{}
	found := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}
	all := aps.Records()
	for _, ap := range all {
		name, _ := ap["name"].(string)
		if len(wanted) == 0 || wanted[name] {
			selected = append(selected, ap)
			found[name] = true
		}
	}
	if !aps.Complete() {
		issues = append(issues, Record{"code": "partial_access_point_coverage"})
	}
	if len(selected) != len(all) {
		issues = append(issues, Record{"code": "ap_scope_filtered"})
	}
	missing := map[string]bool{}
	for _, n := range names {
		if !found[n] && !missing[n] {
			issues = append(issues, Record{"code": "ap_not_found", "ap_name": n})
			missing[n] = true
		}
	}
	if len(selected) > 0 {
		if c.apFactory == nil {
			return c.unavailable(LLDP, Failed, "ap_session_required", nil)
		}
		var creds APCredentials
		var err error
		if credentials != nil {
			creds, err = credentials(ctx)
			if err != nil {
				return c.unavailable(LLDP, Failed, "invalid_ap_credentials", nil)
			}
		}
		reader, err := c.apFactory(ctx, session, creds)
		if err != nil {
			f := safeFailure(err, "ap_session_setup_failed", "ap_setup")
			return c.unavailable(LLDP, Failed, f.Code(), f.ToMap())
		}
		if reader == nil {
			return c.unavailable(LLDP, Failed, "invalid_ap_session", nil)
		}
		for _, ap := range selected {
			r, i, e := c.readAP(ctx, reader, ap)
			records = append(records, r...)
			issues = append(issues, i...)
			evidence = append(evidence, e)
		}
	}
	status := Complete
	if len(issues) > 0 {
		status = Failed
		if len(records) > 0 {
			status = Partial
		}
		for _, entry := range evidence {
			if entry["status"] == string(Complete) {
				status = Partial
			}
		}
	}
	end := c.clock()
	obs, _ := NewObservation(ObservationOptions{Name: LLDP, Status: status, Records: records, Issues: issues, Evidence: evidence, StartedAt: &start, CompletedAt: &end})
	return obs
}
func (c *Collector) readAP(ctx context.Context, reader APReader, ap Record) (records, issues []Record, source Record) {
	name, _ := ap["name"].(string)
	source = Record{"kind": "ap_lldp_result", "ap_name": name, "command": LLDPCommand, "attempted": true, "status": string(Failed), "neighbor_count": 0}
	records, issues = []Record{}, []Record{}
	outcome, err := reader.Read(ctx, cloneRecord(ap))
	if err != nil {
		issues = append(issues, safeFailure(err, "command_execution_failed", "execute").ToMap())
	}
	source["attempted"] = !outcome.Skipped
	cap := outcome.Result
	if cap != nil && (!cap.valid || cap.Command() != LLDPCommand || outcome.Skipped && cap.Success()) {
		issues = append(issues, Record{"code": "invalid_command_result", "stage": "response"})
		cap = nil
	}
	if cap != nil {
		ev := cap.Evidence()
		if len(ev) > 0 {
			merged := ev[0]
			for k, v := range source {
				merged[k] = v
			}
			source = merged
		}
		if cap.Skipped() {
			source["attempted"] = false
		}
		if cap.Success() {
			model, _ := ap["model"].(string)
			parser, _ := NewNeighborParser("h3c", model)
			parsed, e := parser.Parse(cap.Body(), name, true)
			if e != nil {
				issues = append(issues, Record{"code": "command_processing_failed", "stage": "parse"})
			} else {
				records = parsed.Records()
				issues = append(issues, parsed.Issues()...)
				source["status"] = string(parsed.Status())
				source["neighbor_count"] = len(records)
			}
		} else {
			issues = append(issues, cap.Failure().ToMap())
		}
	}
	for _, f := range []*Failure{outcome.Error, outcome.CleanupError, outcome.CloseError} {
		if f == nil {
			continue
		}
		duplicate := false
		for _, i := range issues {
			if i["code"] == f.Code() {
				duplicate = true
			}
		}
		if !duplicate {
			issues = append(issues, Record{"code": f.Code()})
		}
		source["status"] = string(Failed)
	}
	if err != nil {
		source["status"] = string(Failed)
	}
	if cap == nil && len(issues) == 0 {
		issues = append(issues, Record{"code": "ap_collection_failed"})
	}
	for _, i := range issues {
		i["ap_name"] = name
	}
	return
}
