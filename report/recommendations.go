package report

import (
	"context"
	"fmt"
	wireless "github.com/gatework/go-wireless"
	"regexp"
	"strings"
)

// RecommendationHook enriches collect's exact published document. No source
// hash is computed until that document (including advice) has been published.
func RecommendationHook(scripts bool) DeviceHook {
	return func(ctx context.Context, doc wireless.Record) (Enrichment, error) {
		if e := wireless.ValidateReviewDocument(doc); e != nil {
			return Enrichment{}, e
		}
		advice := reportAdvice(wireless.Recommendations(doc), scripts)
		return Enrichment{Recommendations: advice, Artifacts: func(ctx context.Context, r *Report, p PublishedDevice) error {
			_, e := r.WriteReview(ctx, doc, p.Basename, p.Path, p.SHA256, advice, scripts)
			return e
		}}, nil
	}
}
func reportAdvice(advice wireless.Record, scripts bool) wireless.Record {
	r := cloneRecord(advice)
	if scripts {
		return r
	}
	items := []wireless.Record{}
	for _, item := range adviceItems(advice) {
		c := cloneRecord(item)
		delete(c, "change")
		delete(c, "template")
		c["commands"] = []string{}
		c["rollback"] = []string{}
		items = append(items, c)
	}
	r["items"] = items
	return r
}
func cloneRecord(m wireless.Record) wireless.Record {
	n := wireless.Record{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
func adviceItems(m wireless.Record) []wireless.Record {
	switch v := m["items"].(type) {
	case []wireless.Record:
		return v
	case []any:
		r := []wireless.Record{}
		for _, x := range v {
			if t, ok := x.(wireless.Record); ok {
				r = append(r, t)
			}
		}
		return r
	}
	return nil
}
func textList(v any) []string {
	switch a := v.(type) {
	case []string:
		return a
	case []any:
		r := []string{}
		for _, v := range a {
			if s, ok := v.(string); ok {
				r = append(r, s)
			}
		}
		return r
	}
	return nil
}
func mapping(v any) wireless.Record { m, _ := v.(wireless.Record); return m }

var reviewBasename = regexp.MustCompile(`\A[A-Za-z0-9_.-]+\z`)

func (r *Report) WriteReview(ctx context.Context, doc wireless.Record, basename, source, sourceSHA string, advice wireless.Record, scripts bool) (wireless.Record, error) {
	if !reviewBasename.MatchString(basename) {
		return nil, wireless.ErrChangePlan
	}
	advice = reportAdvice(advice, scripts)
	advice["source"] = source
	advice["source_sha256"] = sourceSHA
	prefix := "recommendations/" + basename
	body, e := jsonBody(advice)
	if e != nil {
		return nil, e
	}
	if e = r.Publish(ctx, prefix+".json", body); e != nil {
		return nil, e
	}
	if e = r.Publish(ctx, prefix+".txt", []byte(reviewText(advice))); e != nil {
		return nil, e
	}
	verify := []string{}
	seen := map[string]bool{}
	for _, item := range adviceItems(advice) {
		for _, cmd := range textList(item["verify"]) {
			if !seen[cmd] {
				seen[cmd] = true
				verify = append(verify, cmd)
			}
		}
	}
	if e = r.Publish(ctx, prefix+".verify.cli", []byte(strings.Join(verify, "\n")+"\n")); e != nil {
		return nil, e
	}
	if scripts {
		for _, kind := range []string{"commands", "rollback"} {
			lines := []string{"REVIEW ONLY - 未执行；先审阅同名 TXT 报告中的条件、影响和证据 SHA256。", "回退仅在确认本次增量已生效、无其他变更时使用。"}
			for _, item := range adviceItems(advice) {
				commands := textList(item[kind])
				if len(commands) == 0 {
					continue
				}
				lines = append(lines, fmt.Sprint(item["code"])+" / "+fmt.Sprint(item["scope"]))
				lines = append(lines, commands...)
			}
			for i := range lines {
				lines[i] = "! " + lines[i]
			}
			suffix := "suggested.cfg"
			if kind == "rollback" {
				suffix = "rollback.cfg"
			}
			if e = r.Publish(ctx, prefix+"."+suffix, []byte(strings.Join(lines, "\n")+"\n")); e != nil {
				return nil, e
			}
		}
		config := firstConfiguration(doc)
		for i, item := range adviceItems(advice) {
			change := mapping(item["change"])
			if config == nil || item["status"] != "candidate" || !wireless.ChangeEligible(config, change) {
				continue
			}
			commands, rollback, e := wireless.RenderChange(change)
			if e != nil {
				return nil, e
			}
			fingerprint, e := wireless.Fingerprint(config)
			if e != nil {
				return nil, e
			}
			plan := wireless.Record{"schema_version": 1, "platform": "h3c_wireless", "device": doc["device"], "expected_config_sha256": fingerprint, "source": source, "source_sha256": sourceSHA, "change": change, "commands": commands, "rollback": rollback}
			for _, k := range []string{"scope", "title", "prerequisites", "impact", "verify"} {
				plan[k] = item[k]
			}
			body, e = jsonBody(plan)
			if e != nil {
				return nil, e
			}
			if e = r.Publish(ctx, fmt.Sprintf("%s.%d.plan.json", prefix, i+1), body); e != nil {
				return nil, e
			}
		}
	}
	return wireless.Record{"device": advice["device"], "counts": advice["counts"], "report": prefix + ".txt", "source": source, "source_sha256": sourceSHA}, nil
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
func reviewText(advice wireless.Record) string {
	device := mapping(advice["device"])
	observed := advice["observed_at"]
	if observed == nil {
		observed = "未知"
	}
	lines := []string{fmt.Sprintf("%v（%v）修复建议", device["name"], device["host"]), "", fmt.Sprintf("仅供审阅，未执行。证据时间：%v；来源：%v", observed, advice["source"]), "", fmt.Sprint("源文件 SHA256：", advice["source_sha256"]), "", "先重采确认配置未变化，再逐项确认前提；禁止整批直接粘贴。回退前确认本次变更实际生效且无并行修改。", ""}
	items := adviceItems(advice)
	if len(items) == 0 {
		lines = append(lines, "当前规则没有命中；不代表网络健康或已覆盖所有配置问题。")
	}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("%v — %v", item["title"], item["scope"]), "", fmt.Sprintf("状态：%v；规则：%v；类别：%v", item["status"], item["code"], item["category"]), "")
		if item["impact"] != nil {
			lines = append(lines, fmt.Sprint("影响：", item["impact"]), "")
		}
		evidence, _ := jsonBody(item["evidence"])
		lines = append(lines, "依据：", "", strings.TrimSuffix(string(evidence), "\n"), "")
		for _, field := range []struct{ key, label string }{{"prerequisites", "适用条件"}, {"blockers", "待补信息"}, {"commands", "候选配置（未执行）"}, {"rollback", "条件回退"}, {"verify", "只读核查 / 验收命令"}, {"template", "待补参数模板（不可执行）"}} {
			values := textList(item[field.key])
			if len(values) == 0 {
				continue
			}
			lines = append(lines, field.label+"：", "")
			for _, value := range values {
				lines = append(lines, "  "+value)
			}
			lines = append(lines, "")
		}
		lines = append(lines, fmt.Sprint("验收：", item["expected"]), "", "命令参考："+strings.Join(textList(item["references"]), " "), "")
	}
	return strings.Join(lines, "\n") + "\n"
}
