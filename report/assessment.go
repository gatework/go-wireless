package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/gatework/go-netops/assess"
	wireless "github.com/gatework/go-wireless"
	changes "github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/wlan"
)

// ErrReviewName reports a review basename that is not a safe file name.
var ErrReviewName = errors.New("wireless report: unsafe review name")

// AssessmentHook embeds collect's per-controller assessment in the published
// device document and writes its review artifacts next to it. Without
// scripts, no change, command or rollback is embedded or published.
func AssessmentHook(scripts bool) DeviceHook {
	return func(ctx context.Context, doc wireless.Record) (Enrichment, error) {
		body, err := wireless.MarshalJSON(doc)
		if err != nil {
			return Enrichment{}, err
		}
		c, err := wlan.DecodeDocument(body)
		if err != nil {
			return Enrichment{}, err
		}
		assessor, err := rules.NewAssessor(assess.Policy{}, nil)
		if err != nil {
			return Enrichment{}, err
		}
		result, err := assessor.Assess(ctx, []*wlan.Controller{c})
		if err != nil {
			return Enrichment{}, err
		}
		a := result.Controllers[0]
		embedded := a
		if !scripts {
			embedded = withoutScripts(a)
		}
		return Enrichment{Assessment: embedded, Artifacts: func(ctx context.Context, r *Report, p PublishedDevice) error {
			_, err := r.WriteAssessment(ctx, doc, c, a, p.Basename, p.Path, p.SHA256, scripts)
			return err
		}}, nil
	}
}

// withoutScripts drops every executable part of a's remediations.
func withoutScripts(a rules.ControllerAssessment) rules.ControllerAssessment {
	findings := slices.Clone(a.Assessment.Findings)
	for i := range findings {
		if r := findings[i].Remediation; r != nil {
			c := *r
			c.Change, c.Commands, c.Rollback = nil, nil, nil
			findings[i].Remediation = &c
		}
	}
	a.Assessment.Findings = findings
	return a
}

func firstConfiguration(doc wireless.Record) wireless.Record {
	v := mapping(mapping(mapping(doc["snapshot"])["dimensions"])["configuration"])["records"]
	switch records := v.(type) {
	case []any:
		if len(records) > 0 {
			return mapping(records[0])
		}
	case []wireless.Record:
		if len(records) > 0 {
			return records[0]
		}
	}
	return nil
}

func mapping(v any) wireless.Record { m, _ := v.(wireless.Record); return m }

// candidate is one executable remediation of a controller.
type candidate struct {
	finding assess.Finding
	change  changes.TemplateSecurity
}

// candidates are the findings whose typed change still applies to the
// controller's configuration; only they become plans.
func candidates(c *wlan.Controller, a assess.Assessment) []candidate {
	var out []candidate
	for _, f := range a.Findings {
		r := f.Remediation
		if r == nil || r.Status != assess.RemediationCandidate {
			continue
		}
		t, ok := r.Change.(changes.TemplateSecurity)
		if !ok || c.Configuration == nil || t.Eligible(c.Configuration) != nil {
			continue
		}
		out = append(out, candidate{f, t})
	}
	return out
}

// planRecord renders one candidate as the plan file apply authenticates.
// config is the collected configuration record whose fingerprint apply
// compares with the live readback.
func planRecord(doc, config wireless.Record, source, sourceSHA string, c candidate) (wireless.Record, error) {
	script, err := c.change.Render()
	if err != nil {
		return nil, err
	}
	fingerprint, err := wireless.Fingerprint(config)
	if err != nil {
		return nil, err
	}
	change := wireless.Record{"kind": string(changes.KindTemplateSecurity), "name": c.change.Name, "enabled": c.change.Enabled,
		"cipher_suites": slices.Clone(c.change.CipherSuites), "security_ies": slices.Clone(c.change.SecurityIEs)}
	r := c.finding.Remediation
	return wireless.Record{"schema_version": 1, "platform": rules.ActionablePlatform, "device": doc["device"], "expected_config_sha256": fingerprint,
		"source": source, "source_sha256": sourceSHA, "change": change, "commands": script.Commands, "rollback": script.Rollback,
		"scope": "service-template " + c.change.Name, "title": c.finding.Title, "prerequisites": slices.Clone(r.Prerequisites),
		"impact": r.Impact, "verify": slices.Clone(c.finding.Verify)}, nil
}

var reviewBasename = regexp.MustCompile(`\A[A-Za-z0-9_.-]+\z`)

// WriteAssessment publishes one controller's assessment, report text and
// verification commands under recommendations/<basename>; with scripts it
// also publishes the review-only suggested and rollback scripts and one
// executable plan per eligible candidate. It returns the review entry.
func (r *Report) WriteAssessment(ctx context.Context, doc wireless.Record, c *wlan.Controller, a rules.ControllerAssessment, basename, source, sourceSHA string, scripts bool) (wireless.Record, error) {
	if !reviewBasename.MatchString(basename) {
		return nil, ErrReviewName
	}
	planned := []candidate{}
	if scripts {
		planned = candidates(c, a.Assessment)
	} else {
		a = withoutScripts(a)
	}
	prefix := "recommendations/" + basename
	body, err := json.MarshalIndent(struct {
		rules.ControllerAssessment
		Source       string `json:"source"`
		SourceSHA256 string `json:"source_sha256"`
	}{a, source, sourceSHA}, "", "  ")
	if err != nil {
		return nil, &Error{Relative: prefix + ".json", Phase: "encode"}
	}
	if err := r.Publish(ctx, prefix+".json", append(body, '\n')); err != nil {
		return nil, err
	}
	if err := r.Publish(ctx, prefix+".txt", []byte(assessmentText(doc, a, source, sourceSHA, len(planned)))); err != nil {
		return nil, err
	}
	verify := []string{}
	for _, f := range a.Assessment.Findings {
		for _, cmd := range f.Verify {
			if !slices.Contains(verify, cmd) {
				verify = append(verify, cmd)
			}
		}
	}
	if err := r.Publish(ctx, prefix+".verify.cli", []byte(strings.Join(verify, "\n")+"\n")); err != nil {
		return nil, err
	}
	if scripts {
		if err := r.writeScripts(ctx, prefix, a.Assessment); err != nil {
			return nil, err
		}
		if config := firstConfiguration(doc); config != nil {
			for i, cand := range planned {
				plan, err := planRecord(doc, config, source, sourceSHA, cand)
				if err != nil {
					return nil, err
				}
				body, err := jsonBody(plan)
				if err != nil {
					return nil, err
				}
				if err := r.Publish(ctx, fmt.Sprintf("%s.%d.plan.json", prefix, i+1), body); err != nil {
					return nil, err
				}
			}
		}
	}
	counts := wireless.Record{}
	for sev, n := range a.Assessment.Counts {
		counts[sev.String()] = n
	}
	return wireless.Record{"device": doc["device"], "counts": counts, "highest": a.Assessment.Highest().String(), "report": prefix + ".txt", "source": source, "source_sha256": sourceSHA}, nil
}

// writeScripts publishes the commented, review-only candidate and rollback
// scripts: every line is a comment, so nothing can be pasted as a batch.
func (r *Report) writeScripts(ctx context.Context, prefix string, a assess.Assessment) error {
	for _, rollback := range []bool{false, true} {
		lines := []string{"REVIEW ONLY - 未执行；先审阅同名 TXT 报告中的条件、影响和证据 SHA256。", "回退仅在确认本次增量已生效、无其他变更时使用。"}
		for _, f := range a.Findings {
			if f.Remediation == nil {
				continue
			}
			commands := f.Remediation.Commands
			if rollback {
				commands = f.Remediation.Rollback
			}
			if len(commands) == 0 {
				continue
			}
			lines = append(lines, string(f.Rule)+" / "+f.Subject.String())
			lines = append(lines, commands...)
		}
		for i := range lines {
			lines[i] = "! " + lines[i]
		}
		suffix := ".suggested.cfg"
		if rollback {
			suffix = ".rollback.cfg"
		}
		if err := r.Publish(ctx, prefix+suffix, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
			return err
		}
	}
	return nil
}

func assessmentText(doc wireless.Record, a rules.ControllerAssessment, source, sourceSHA string, plans int) string {
	device := mapping(doc["device"])
	observed := mapping(doc["snapshot"])["observed_at"]
	if observed == nil {
		observed = "未知"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%v（%v）风险评估与修复建议\n\n仅供审阅，未执行。证据时间：%v；来源：%v\n\n源文件 SHA256：%v\n\n", device["name"], device["host"], observed, source, sourceSHA)
	b.WriteString("先重采确认配置未变化，再逐项确认前提；禁止整批直接粘贴。回退前确认本次变更实际生效且无并行修改。\n证据完整性不代表网络健康；证据不完整的规则不会给出结论。\n\n")
	skipped := 0
	for _, o := range a.Assessment.Rules {
		if o.State == assess.RuleSkipped {
			skipped++
		}
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "因证据不完整而跳过的规则：%d 项\n\n", skipped)
	}
	if len(a.Assessment.Findings) == 0 {
		b.WriteString("当前规则没有命中；不代表网络健康或已覆盖所有配置问题。\n")
	}
	for i, f := range a.Assessment.Findings {
		fmt.Fprintf(&b, "%d. [%s] %s — %s\n规则：%s；类别：%s\n", i+1, strings.ToUpper(f.Severity.String()), f.Title, f.Subject.String(), f.Rule, f.Category)
		if f.Detail != "" {
			fmt.Fprintf(&b, "说明：%s\n", f.Detail)
		}
		if f.Expected != nil || f.Actual != nil {
			e, _ := json.Marshal(f.Expected)
			x, _ := json.Marshal(f.Actual)
			fmt.Fprintf(&b, "期望：%s\n实际：%s\n", e, x)
		}
		if r := f.Remediation; r != nil {
			fmt.Fprintf(&b, "修复：%s\n", r.Status)
			for _, field := range []struct {
				label  string
				values []string
			}{{"适用条件", r.Prerequisites}, {"待补信息", r.Blockers}, {"候选配置（未执行）", r.Commands}, {"条件回退", r.Rollback}} {
				if len(field.values) == 0 {
					continue
				}
				fmt.Fprintf(&b, "%s：\n", field.label)
				for _, v := range field.values {
					fmt.Fprintf(&b, "  %s\n", v)
				}
			}
			if r.Impact != "" {
				fmt.Fprintf(&b, "影响：%s\n", r.Impact)
			}
			if r.Expected != "" {
				fmt.Fprintf(&b, "验收：%s\n", r.Expected)
			}
		}
		if len(f.Verify) > 0 {
			fmt.Fprintf(&b, "只读核查 / 验收命令：%s\n", strings.Join(f.Verify, "；"))
		}
		if len(f.References) > 0 {
			fmt.Fprintf(&b, "命令参考：%s\n", strings.Join(f.References, " "))
		}
		b.WriteString("\n")
	}
	if plans > 0 {
		fmt.Fprintf(&b, "可执行计划：%d 项（同名 .<序号>.plan.json，apply 前会重新核对配置指纹）\n", plans)
	}
	return b.String()
}
