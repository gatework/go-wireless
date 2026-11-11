package collect

import (
	"context"
	"maps"
	"slices"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

// collectNeighbors reads LLDP neighbors from every selected AP through one AP
// session and returns the LLDP observation.
func (c *Collector) collectNeighbors(ctx context.Context, profile *parser.Profile, query parser.APQuery, session Session, aps wireless.Observation, names []string, credentials APCredentialsSource) wireless.Observation {
	if !aps.Available() {
		return c.unavailable(wireless.LLDP, wireless.Failed, "access_points_unavailable", nil)
	}
	start := c.clock()
	records, evidence := []wireless.Record{}, []wireless.Record{}
	selected, issues := selectAPs(aps, names)
	if len(selected) > 0 {
		reader, failed, ok := c.openAPReader(ctx, session, credentials)
		if !ok {
			return failed
		}
		for _, ap := range selected {
			r, i, e := c.readAP(ctx, profile, query, reader, ap)
			records = append(records, r...)
			issues = append(issues, i...)
			evidence = append(evidence, e)
		}
	}
	end := c.clock()
	obs, _ := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.LLDP, Status: neighborStatus(records, issues, evidence), Records: records, Issues: issues, Evidence: evidence, StartedAt: &start, CompletedAt: &end})
	return obs
}

// selectAPs returns the APs named in names (all when names is empty) and
// the coverage issues of that selection.
func selectAPs(aps wireless.Observation, names []string) (selected, issues []wireless.Record) {
	issues = []wireless.Record{}
	wanted := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}
	found := map[string]bool{}
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
	return selected, issues
}

// openAPReader resolves the AP credentials and opens the AP session; when ok
// is false, failed is the LLDP observation to return.
func (c *Collector) openAPReader(ctx context.Context, session Session, credentials APCredentialsSource) (reader APReader, failed wireless.Observation, ok bool) {
	if c.apFactory == nil {
		return nil, c.unavailable(wireless.LLDP, wireless.Failed, "ap_session_required", nil), false
	}
	var creds APCredentials
	if credentials != nil {
		var err error
		if creds, err = credentials(ctx); err != nil {
			return nil, c.unavailable(wireless.LLDP, wireless.Failed, "invalid_ap_credentials", nil), false
		}
	}
	reader, err := c.apFactory(ctx, session, creds)
	if err != nil {
		f := wireless.FailureFrom(err, "ap_session_setup_failed", "ap_setup")
		return nil, c.unavailable(wireless.LLDP, wireless.Failed, f.Code(), f.ToMap()), false
	}
	if reader == nil {
		return nil, c.unavailable(wireless.LLDP, wireless.Failed, "invalid_ap_session", nil), false
	}
	return reader, wireless.Observation{}, true
}

// neighborStatus is complete without issues; with issues it is partial when
// any neighbor or any complete AP result was obtained, otherwise failed.
func neighborStatus(records, issues, evidence []wireless.Record) wireless.Status {
	if len(issues) == 0 {
		return wireless.Complete
	}
	if len(records) > 0 || slices.ContainsFunc(evidence, func(e wireless.Record) bool { return e["status"] == string(wireless.Complete) }) {
		return wireless.Partial
	}
	return wireless.Failed
}

// readAP reads one AP's LLDP neighbors and returns them, the issues (each
// tagged with the AP name) and the AP's evidence record.
func (c *Collector) readAP(ctx context.Context, profile *parser.Profile, query parser.APQuery, reader APReader, ap wireless.Record) (records, issues []wireless.Record, source wireless.Record) {
	name, _ := ap["name"].(string)
	source = wireless.Record{"kind": "ap_lldp_result", "ap_name": name, "command": query.Command(), "attempted": true, "status": string(wireless.Failed), "neighbor_count": 0}
	records, issues = []wireless.Record{}, []wireless.Record{}
	outcome, err := reader.Read(ctx, APRequest{AccessPoint: wireless.CloneRecord(ap), Query: query})
	if err != nil {
		issues = append(issues, wireless.FailureFrom(err, "command_execution_failed", "execute").ToMap())
	}
	source["attempted"] = !outcome.Skipped
	captured := outcome.Result
	if captured != nil && (!captured.Valid() || captured.Command() != query.Command() || outcome.Skipped && captured.Success()) {
		issues = append(issues, wireless.Record{"code": "invalid_command_result", "stage": "response"})
		captured = nil
	}
	if captured != nil {
		var found []wireless.Record
		source, found, issues = parseAPCapture(profile, ap, name, captured, source, issues)
		records = append(records, found...)
	}
	for _, f := range []*wireless.FailureError{outcome.Error, outcome.CleanupError, outcome.CloseError} {
		if f == nil {
			continue
		}
		if !slices.ContainsFunc(issues, func(i wireless.Record) bool { return i["code"] == f.Code() }) {
			issues = append(issues, wireless.Record{"code": f.Code()})
		}
		source["status"] = string(wireless.Failed)
	}
	if err != nil {
		source["status"] = string(wireless.Failed)
	}
	if captured == nil && len(issues) == 0 {
		issues = append(issues, wireless.Record{"code": "ap_collection_failed"})
	}
	for _, i := range issues {
		i["ap_name"] = name
	}
	return records, issues, source
}

// parseAPCapture merges the capture evidence into source and parses a
// successful capture's neighbors.
func parseAPCapture(profile *parser.Profile, ap wireless.Record, name string, captured *wireless.Capture, source wireless.Record, issues []wireless.Record) (wireless.Record, []wireless.Record, []wireless.Record) {
	if ev := captured.Evidence(); len(ev) > 0 {
		merged := ev[0]
		maps.Copy(merged, source)
		source = merged
	}
	if captured.Skipped() {
		source["attempted"] = false
	}
	if !captured.Success() {
		return source, nil, append(issues, captured.Failure().ToMap())
	}
	model, _ := ap["model"].(string)
	neighbor, e := parser.NewNeighborParser(profile, model)
	var parsed wireless.Observation
	if e == nil {
		parsed, e = neighbor.Parse(captured.Body(), name, true)
	}
	if e != nil {
		return source, nil, append(issues, wireless.Record{"code": "command_processing_failed", "stage": "parse"})
	}
	records := parsed.Records()
	source["status"] = string(parsed.Status())
	source["neighbor_count"] = len(records)
	return source, records, append(issues, parsed.Issues()...)
}
