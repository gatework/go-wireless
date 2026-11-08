package collect

import (
	"context"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

const LLDPCommand = "display lldp neighbor-information verbose"

func (c *Collector) collectNeighbors(ctx context.Context, profile *parser.Profile, session Session, aps wireless.Observation, names []string, credentials APCredentialsSource) wireless.Observation {
	if !aps.Available() {
		return c.unavailable(wireless.LLDP, wireless.Failed, "access_points_unavailable", nil)
	}
	start := c.clock()
	records, issues, evidence := []wireless.Record{}, []wireless.Record{}, []wireless.Record{}
	selected := []wireless.Record{}
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
		issues = append(issues, wireless.Record{"code": "partial_access_point_coverage"})
	}
	if len(selected) != len(all) {
		issues = append(issues, wireless.Record{"code": "ap_scope_filtered"})
	}
	missing := map[string]bool{}
	for _, n := range names {
		if !found[n] && !missing[n] {
			issues = append(issues, wireless.Record{"code": "ap_not_found", "ap_name": n})
			missing[n] = true
		}
	}
	if len(selected) > 0 {
		if c.apFactory == nil {
			return c.unavailable(wireless.LLDP, wireless.Failed, "ap_session_required", nil)
		}
		var creds APCredentials
		var err error
		if credentials != nil {
			creds, err = credentials(ctx)
			if err != nil {
				return c.unavailable(wireless.LLDP, wireless.Failed, "invalid_ap_credentials", nil)
			}
		}
		reader, err := c.apFactory(ctx, session, creds)
		if err != nil {
			f := wireless.FailureFrom(err, "ap_session_setup_failed", "ap_setup")
			return c.unavailable(wireless.LLDP, wireless.Failed, f.Code(), f.ToMap())
		}
		if reader == nil {
			return c.unavailable(wireless.LLDP, wireless.Failed, "invalid_ap_session", nil)
		}
		for _, ap := range selected {
			r, i, e := c.readAP(ctx, profile, reader, ap)
			records = append(records, r...)
			issues = append(issues, i...)
			evidence = append(evidence, e)
		}
	}
	status := wireless.Complete
	if len(issues) > 0 {
		status = wireless.Failed
		if len(records) > 0 {
			status = wireless.Partial
		}
		for _, entry := range evidence {
			if entry["status"] == string(wireless.Complete) {
				status = wireless.Partial
			}
		}
	}
	end := c.clock()
	obs, _ := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.LLDP, Status: status, Records: records, Issues: issues, Evidence: evidence, StartedAt: &start, CompletedAt: &end})
	return obs
}
func (c *Collector) readAP(ctx context.Context, profile *parser.Profile, reader APReader, ap wireless.Record) (records, issues []wireless.Record, source wireless.Record) {
	name, _ := ap["name"].(string)
	source = wireless.Record{"kind": "ap_lldp_result", "ap_name": name, "command": LLDPCommand, "attempted": true, "status": string(wireless.Failed), "neighbor_count": 0}
	records, issues = []wireless.Record{}, []wireless.Record{}
	outcome, err := reader.Read(ctx, wireless.CloneRecord(ap))
	if err != nil {
		issues = append(issues, wireless.FailureFrom(err, "command_execution_failed", "execute").ToMap())
	}
	source["attempted"] = !outcome.Skipped
	cap := outcome.Result
	if cap != nil && (!cap.Valid() || cap.Command() != LLDPCommand || outcome.Skipped && cap.Success()) {
		issues = append(issues, wireless.Record{"code": "invalid_command_result", "stage": "response"})
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
			neighbor, e := parser.NewNeighborParser(profile, model)
			var parsed wireless.Observation
			if e == nil {
				parsed, e = neighbor.Parse(cap.Body(), name, true)
			}
			if e != nil {
				issues = append(issues, wireless.Record{"code": "command_processing_failed", "stage": "parse"})
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
	for _, f := range []*wireless.Failure{outcome.Error, outcome.CleanupError, outcome.CloseError} {
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
			issues = append(issues, wireless.Record{"code": f.Code()})
		}
		source["status"] = string(wireless.Failed)
	}
	if err != nil {
		source["status"] = string(wireless.Failed)
	}
	if cap == nil && len(issues) == 0 {
		issues = append(issues, wireless.Record{"code": "ap_collection_failed"})
	}
	for _, i := range issues {
		i["ap_name"] = name
	}
	return
}
