// Package h3c parses H3C wireless controller and AP evidence.
package h3c

import (
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/parsetext"
	"github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/parser"
)

// Profile declares H3C query commands and all views produced by each response.
func Profile() parser.ProfileSpec {
	declarations := []parser.Query{
		{Command: "display wlan statistics client-ip-conflict", Dimensions: []wireless.Dimension{wireless.ClientIPConflicts}},
		{Command: "display wlan ap-model all", Dimensions: []wireless.Dimension{wireless.APModels}},
		{Command: "display wlan ap all verbose", Dimensions: []wireless.Dimension{wireless.AccessPoints, wireless.Radios}},
		{Command: "display wlan bss all verbose", Dimensions: []wireless.Dimension{wireless.BSSes}},
		{Command: "display wlan client verbose", Dimensions: []wireless.Dimension{wireless.Clients}},
		{Command: "display current-configuration", Dimensions: []wireless.Dimension{wireless.Configuration, wireless.APGroups, wireless.ServiceTemplates}},
		{Command: "display dot1x connection", Dimensions: []wireless.Dimension{wireless.Dot1xConnections}},
		{Command: "display mac-authentication connection", Dimensions: []wireless.Dimension{wireless.MABConnections}},
		{Command: "display wlan client-security block-mac", Dimensions: []wireless.Dimension{wireless.BlockedClients}},
		{Command: "display lldp neighbor-information verbose", Dimensions: []wireless.Dimension{wireless.LLDP}},
	}
	spec := parser.ProfileSpec{Platform: "h3c", ConnectorPlatform: "h3c_wireless", Preparation: "screen-length disable", Aliases: []string{"h3c_wireless"}, APNeighbors: true, APPreparation: "screen-length disable"}
	for _, query := range declarations {
		owned := parser.Query{Command: query.Command, Dimensions: append([]wireless.Dimension(nil), query.Dimensions...)}
		spec.Queries = append(spec.Queries, parser.QuerySpec{Query: query, PreserveNewlines: true, Parse: func(text string, complete bool) map[wireless.Dimension]parser.Result {
			return parseCommand(owned, text, complete)
		}})
	}
	return spec
}
func parseCommand(query parser.Query, text string, complete bool) map[wireless.Dimension]parser.Result {
	if query.Command == "display current-configuration" {
		return configurationViews(query, text, complete)
	}
	content, err := parsetext.NewContent(text)
	if err != nil {
		return commandViews(query, parser.FailedResult("source_limit_exceeded"))
	}
	for line := range content.All() {
		if parser.TerminalInvalid(line.Raw) {
			return commandViews(query, parser.Result{Status: wireless.Partial, Issues: []wireless.Record{{"code": "invalid_terminal_output", "line": line.Number}}})
		}
	}
	state := newParseState(content)
	var result parser.Result
	switch query.Command {
	case "display wlan ap all verbose":
		return state.parseAPs(complete)
	case "display wlan bss all verbose":
		result = state.parseRecords(wireless.BSSes, complete)
	case "display wlan client verbose":
		result = state.parseRecords(wireless.Clients, complete)
	case "display dot1x connection":
		result = state.parseRecords(wireless.Dot1xConnections, complete)
	case "display mac-authentication connection":
		result = state.parseRecords(wireless.MABConnections, complete)
	case "display wlan ap-model all":
		result = state.parseModels(complete)
	case "display wlan client-security block-mac":
		result = state.parseBlocked(complete)
	case "display wlan statistics client-ip-conflict":
		result = state.parseConflicts(complete)
	case "display lldp neighbor-information verbose":
		result = state.parseNeighbors(complete)
	}
	return commandViews(query, result)
}

func commandViews(query parser.Query, result parser.Result) map[wireless.Dimension]parser.Result {
	out := map[wireless.Dimension]parser.Result{}
	for _, name := range query.Dimensions {
		out[name] = result
	}
	return out
}

func configurationViews(query parser.Query, text string, complete bool) map[wireless.Dimension]parser.Result {
	result := parseConfiguration(text, complete)
	out := map[wireless.Dimension]parser.Result{}
	for _, name := range query.Dimensions {
		r := result
		if (name == wireless.APGroups || name == wireless.ServiceTemplates) && len(r.Records) > 0 {
			r.Records = record.Records(r.Records[0][string(name)])
		}
		out[name] = r
	}
	return out
}
