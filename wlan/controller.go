package wlan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/gatework/go-netops/evidence"
)

// Dimensions collected per controller.
const (
	DimAccessPoints     evidence.Dimension = "access_points"
	DimRadios           evidence.Dimension = "radios"
	DimBSSes            evidence.Dimension = "bsses"
	DimClients          evidence.Dimension = "clients"
	DimConfiguration    evidence.Dimension = "configuration"
	DimAPGroups         evidence.Dimension = "ap_groups"
	DimServiceTemplates evidence.Dimension = "service_templates"
	DimDot1x            evidence.Dimension = "dot1x_connections"
	DimMAB              evidence.Dimension = "mab_connections"
	DimBlockedClients   evidence.Dimension = "blocked_clients"
	DimIPConflicts      evidence.Dimension = "client_ip_conflicts"
	DimAPModels         evidence.Dimension = "ap_models"
	DimLLDP             evidence.Dimension = "lldp"
)

// Dimensions lists every controller dimension.
func Dimensions() []evidence.Dimension {
	return []evidence.Dimension{DimAccessPoints, DimRadios, DimBSSes, DimClients, DimConfiguration, DimAPGroups,
		DimServiceTemplates, DimDot1x, DimMAB, DimBlockedClients, DimIPConflicts, DimAPModels, DimLLDP}
}

// IssueUnmodeled marks a record containing a field the model does not know.
const IssueUnmodeled = "unmodeled_fact"

// Identity is how the inventory knows a controller.
type Identity struct {
	Host     string `json:"host"`
	Name     string `json:"name,omitempty"`
	Platform string `json:"platform,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Controller is one AC's typed evidence.
type Controller struct {
	Identity
	Configuration    *Configuration                          `json:"configuration,omitempty"`
	AccessPoints     []AccessPoint                           `json:"access_points,omitempty"`
	Radios           []Radio                                 `json:"radios,omitempty"`
	BSSes            []BSS                                   `json:"bsses,omitempty"`
	Clients          []Client                                `json:"clients,omitempty"`
	APGroups         []APGroup                               `json:"ap_groups,omitempty"`
	ServiceTemplates []ServiceTemplate                       `json:"service_templates,omitempty"`
	Dot1x            []AuthSession                           `json:"dot1x_connections,omitempty"`
	MAB              []AuthSession                           `json:"mab_connections,omitempty"`
	BlockedClients   []BlockedClient                         `json:"blocked_clients,omitempty"`
	IPConflicts      []IPConflict                            `json:"client_ip_conflicts,omitempty"`
	APModels         []APModel                               `json:"ap_models,omitempty"`
	LLDP             []LLDPNeighbor                          `json:"lldp,omitempty"`
	Coverage         evidence.Coverage                       `json:"coverage"`
	Issues           map[evidence.Dimension][]evidence.Issue `json:"issues,omitempty"`
}

// Has reports whether a dimension is complete.
func (c *Controller) Has(d evidence.Dimension) bool { return c.Coverage.Status(d).Complete() }

// Observed is the parser output for one dimension.
type Observed struct {
	Status  evidence.Status
	Records []json.RawMessage
	Issues  []json.RawMessage
}

// NewController decodes observed dimensions into a typed controller.
func NewController(id Identity, dims map[evidence.Dimension]Observed) *Controller {
	c := &Controller{Identity: id, Coverage: evidence.Coverage{}, Issues: map[evidence.Dimension][]evidence.Issue{}}
	for _, dim := range Dimensions() {
		o, ok := dims[dim]
		if !ok {
			continue
		}
		issues := make([]evidence.Issue, 0, len(o.Issues))
		for _, raw := range o.Issues {
			issue, err := DecodeIssue(raw)
			if err != nil {
				issue = evidence.Issue{Code: IssueUnmodeled, Detail: "issue: " + err.Error()}
			}
			issues = append(issues, issue)
		}
		if err := c.decode(dim, o.Records); err != nil {
			issues = append(issues, evidence.Issue{Code: IssueUnmodeled, Detail: err.Error()})
		}
		status := o.Status
		if status == evidence.StatusComplete && slices.ContainsFunc(issues, func(i evidence.Issue) bool { return i.Code == IssueUnmodeled }) {
			status = evidence.StatusPartial
		}
		c.Coverage[dim] = status
		if len(issues) > 0 {
			c.Issues[dim] = issues
		}
	}
	return c
}

func (c *Controller) decode(dim evidence.Dimension, records []json.RawMessage) error {
	switch dim {
	case DimConfiguration:
		switch len(records) {
		case 0:
			return nil
		case 1:
			var cfg Configuration
			if err := strict(records[0], &cfg); err != nil {
				return err
			}
			c.Configuration = &cfg
			return nil
		}
		return fmt.Errorf("configuration has %d records", len(records))
	case DimAccessPoints:
		return rows(records, &c.AccessPoints)
	case DimRadios:
		return rows(records, &c.Radios)
	case DimBSSes:
		return rows(records, &c.BSSes)
	case DimClients:
		return rows(records, &c.Clients)
	case DimAPGroups:
		return rows(records, &c.APGroups)
	case DimServiceTemplates:
		return rows(records, &c.ServiceTemplates)
	case DimDot1x:
		return rows(records, &c.Dot1x)
	case DimMAB:
		return rows(records, &c.MAB)
	case DimBlockedClients:
		return rows(records, &c.BlockedClients)
	case DimIPConflicts:
		return rows(records, &c.IPConflicts)
	case DimAPModels:
		return rows(records, &c.APModels)
	case DimLLDP:
		return rows(records, &c.LLDP)
	}
	return nil
}

func rows[T any](records []json.RawMessage, dst *[]T) error {
	out := make([]T, 0, len(records))
	for i, raw := range records {
		var v T
		if err := strict(raw, &v); err != nil {
			return fmt.Errorf("record %d: %w", i, err)
		}
		out = append(out, v)
	}
	*dst = out
	return nil
}

func strict(raw json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("unmodeled field: %w", err)
	}
	return nil
}

// DecodeIssue converts a parser issue object to evidence.Issue.
func DecodeIssue(raw json.RawMessage) (evidence.Issue, error) {
	var fields map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&fields); err != nil {
		return evidence.Issue{}, err
	}
	var issue evidence.Issue
	for k, v := range fields {
		switch k {
		case "code":
			s, _ := v.(string)
			issue.Code = s
		case "line", "first_line", "expected", "parsed", "observed", "count":
			n, isNumber := v.(json.Number)
			if !isNumber {
				return evidence.Issue{}, fmt.Errorf("issue %s", k)
			}
			i, err := strconv.Atoi(n.String())
			if err != nil {
				issue.Detail = strings.TrimSpace(issue.Detail + " " + k + "=" + n.String())
				continue
			}
			switch k {
			case "line", "first_line":
				issue.Line = i
			case "count":
				issue.Count = i
			case "expected":
				issue.Expected = &i
			default:
				issue.Observed = &i
			}
		default:
			s, ok := v.(string)
			if !ok {
				return evidence.Issue{}, fmt.Errorf("issue field %s", k)
			}
			if issue.Scope == nil {
				issue.Scope = map[string]string{}
			}
			issue.Scope[k] = s
		}
	}
	if issue.Code == "" {
		return evidence.Issue{}, errors.New("issue code")
	}
	return issue, nil
}
