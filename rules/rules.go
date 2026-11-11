// Package rules is the wireless rule catalog over wlan.Controller,
// evaluated by the go-netops assessment engine.
package rules

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/drift"
	"github.com/gatework/go-netops/evidence"
	changes "github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/wlan"
)

const h3cGuide = "https://www.h3c.com/en/d_202210/1709621_294551_0.htm"

// ActionablePlatform is the only platform whose configuration rules and
// remediations are verified; others get evidence and runtime rules only.
const ActionablePlatform = "h3c_wireless"

type rule = assess.Rule[*wlan.Controller]
type input = assess.Input[*wlan.Controller]

func controller(c *wlan.Controller) assess.Subject { return assess.NewSubject("controller", c.Host) }

func h3c(c *wlan.Controller) bool { return c.Platform == ActionablePlatform }

// Rules returns the catalog in evaluation order.
func Rules() []assess.Rule[*wlan.Controller] {
	return []rule{
		evidenceIncomplete, platformLimited, legacySecurity, openSSID, templateBinding,
		ipConflictHistory, apNotRunning, radioAdminDown, firmwareDrift,
		channel24, wide24, utilization, noise, apClientLoad, backupAC,
	}
}

var evidenceIncomplete = assess.NewRule(assess.RuleMeta{
	ID: "wireless.evidence_incomplete", Title: "证据不完整，先补采", Severity: assess.SeverityMedium, Category: assess.CategoryEvidence,
}, func(in input) []assess.Finding {
	c := in.Model
	var out []assess.Finding
	if len(c.Coverage) == 0 {
		return []assess.Finding{{Subject: controller(c).Child("dimension", string(wlan.DimConfiguration)), Detail: "未采集任何维度"}}
	}
	for _, dim := range slices.Sorted(maps.Keys(c.Coverage)) {
		status := c.Coverage[dim]
		if status.Complete() {
			continue
		}
		var codes []string
		for _, i := range c.Issues[dim] {
			if !slices.Contains(codes, i.Code) {
				codes = append(codes, i.Code)
			}
		}
		out = append(out, assess.Finding{Subject: controller(c).Child("dimension", string(dim)), Actual: codes, Detail: "状态 " + string(status)})
	}
	return out
})

var platformLimited = assess.NewRule(assess.RuleMeta{
	ID: "wireless.platform_rules_limited", Title: "此平台尚未提供配置修复规则", Severity: assess.SeverityInfo, Category: assess.CategoryEvidence,
}, func(in input) []assess.Finding {
	if h3c(in.Model) {
		return nil
	}
	return []assess.Finding{{Subject: controller(in.Model).Child("platform", in.Model.Platform),
		Remediation: &assess.Remediation{Status: assess.RemediationBlocked, Blockers: []string{"仅 h3c_wireless 平台的配置规则经过验证"}}}}
})

func config(c *wlan.Controller) *wlan.Configuration {
	if !h3c(c) || !c.Has(wlan.DimConfiguration) {
		return nil
	}
	return c.Configuration
}

func templateSubject(c *wlan.Controller, name string) assess.Subject {
	return controller(c).Child("service_template", name)
}

var legacySecurity = assess.NewRule(assess.RuleMeta{
	ID: "wireless.legacy_wlan_security", Title: "服务模板保留 TKIP/WPA 兼容配置", Severity: assess.SeverityMedium, Category: assess.CategorySecurity,
	Requires: []evidence.Dimension{wlan.DimConfiguration}, References: []string{h3cGuide},
}, func(in input) []assess.Finding {
	cfg := config(in.Model)
	if cfg == nil {
		return nil
	}
	var out []assess.Finding
	for _, t := range cfg.ServiceTemplates {
		s := t.Configuration
		if !slices.Contains(s.CipherSuites, "tkip") && !slices.Contains(s.SecurityIEs, "wpa") {
			continue
		}
		f := assess.Finding{Subject: templateSubject(in.Model, t.Name),
			Actual: map[string]any{"enabled": t.Enabled, "cipher_suites": nonNil(s.CipherSuites), "security_ies": nonNil(s.SecurityIEs)},
			Verify: []string{"display current-configuration", "display wlan client verbose", "display wlan bss all verbose"}}
		r := &assess.Remediation{
			Prerequisites: []string{"此项是加固建议，不代表当前业务故障；确认所有受影响终端支持 CCMP/RSN，且业务允许移除旧协议。", "核对软件命令支持和模板绑定范围；维护窗口内操作，停用模板会断开相关终端。", "重新采集并确认配置未变化；保留回退脚本。出现异常先回读，勿盲目重放。"},
			Impact:        "停用服务模板会中断所绑定 WLAN 的客户端连接，旧终端可能无法重连。",
			Expected:      "保留 CCMP/RSN、模板恢复原启停状态；代表性终端重新关联和认证成功，业务验收通过。",
		}
		if t.Enabled == nil {
			r.Status = assess.RemediationBlocked
			r.Blockers = []string{"需确认模板启停状态、合法名称、CCMP/RSN 替代配置；缺少任一项不生成变更。"}
		} else {
			change := changes.TemplateSecurity{Name: t.Name, Enabled: *t.Enabled, CipherSuites: nonNil(s.CipherSuites), SecurityIEs: nonNil(s.SecurityIEs)}
			if script, err := change.Render(); err != nil {
				r.Status = assess.RemediationBlocked
				r.Blockers = []string{"需确认模板启停状态、合法名称、CCMP/RSN 替代配置；缺少任一项不生成变更。"}
			} else {
				r.Status = assess.RemediationCandidate
				r.Change = change
				r.Commands, r.Rollback = script.Commands, script.Rollback
			}
		}
		f.Remediation = r
		out = append(out, f)
	}
	return out
})

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

var openSSID = assess.NewRule(assess.RuleMeta{
	ID: "wireless.open_ssid", Title: "启用的 SSID 未配置任何加密或接入认证", Severity: assess.SeverityHigh, Category: assess.CategorySecurity,
	Requires: []evidence.Dimension{wlan.DimConfiguration}, Verify: []evidence.Dimension{wlan.DimConfiguration, wlan.DimBSSes},
}, func(in input) []assess.Finding {
	cfg := config(in.Model)
	if cfg == nil {
		return nil
	}
	var out []assess.Finding
	for _, t := range cfg.ServiceTemplates {
		s := t.Configuration
		if t.Enabled == nil || !*t.Enabled || t.SSID == nil {
			continue
		}
		secured := len(s.AKMModes) > 0 || len(s.CipherSuites) > 0 || len(s.SecurityIEs) > 0 || s.Portal != nil ||
			(s.Dot1xEnabled != nil && *s.Dot1xEnabled) || (s.MACAuthenticationEnabled != nil && *s.MACAuthenticationEnabled) ||
			s.ClientSecurityAuthenticationMode != ""
		if !secured {
			out = append(out, assess.Finding{Subject: templateSubject(in.Model, t.Name), Actual: *t.SSID,
				Detail: "访客网络如需开放，应确认隔离、限速与准入策略"})
		}
	}
	return out
})

// templateRefs collects every service-template binding in a settings tree.
func templateRefs(s wlan.Settings, path string, out map[string][]string) {
	for _, b := range s.ServiceTemplates {
		out[b.Name] = append(out[b.Name], path)
	}
	for _, sub := range []struct {
		name string
		m    map[string]wlan.Settings
	}{{"band_defaults", s.BandDefaults}, {"radios", s.Radios}, {"ethernet_ports", s.EthernetPorts}, {"virtual_ap_group", s.VirtualAPGroup}} {
		for _, k := range slices.Sorted(maps.Keys(sub.m)) {
			templateRefs(sub.m[k], path+"/"+sub.name+"/"+k, out)
		}
	}
}

var templateBinding = assess.NewRule(assess.RuleMeta{
	ID: "wireless.template_binding", Title: "AP/组引用的服务模板缺失或被禁用", Severity: assess.SeverityHigh, Category: assess.CategoryConfiguration,
	Requires: []evidence.Dimension{wlan.DimConfiguration},
}, func(in input) []assess.Finding {
	cfg := config(in.Model)
	if cfg == nil {
		return nil
	}
	refs := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(cfg.AccessPoints)) {
		templateRefs(cfg.AccessPoints[name], "access_points/"+name, refs)
	}
	for _, g := range cfg.APGroups {
		templateRefs(g.Configuration, "ap_groups/"+g.Name, refs)
		for _, m := range slices.Sorted(maps.Keys(g.ModelProfiles)) {
			templateRefs(g.ModelProfiles[m], "ap_groups/"+g.Name+"/model_profiles/"+m, refs)
		}
	}
	defined := map[string]*wlan.ServiceTemplate{}
	for i := range cfg.ServiceTemplates {
		defined[cfg.ServiceTemplates[i].Name] = &cfg.ServiceTemplates[i]
	}
	var out []assess.Finding
	for _, name := range slices.Sorted(maps.Keys(refs)) {
		t := defined[name]
		switch {
		case t == nil:
			out = append(out, assess.Finding{Subject: templateSubject(in.Model, name), Detail: "service_template_missing", Actual: refs[name],
				Remediation: &assess.Remediation{Status: assess.RemediationBlocked, Blockers: []string{"需提供模板 SSID、安全认证、转发位置及 VLAN 设计，不能自动创建开放 WLAN。"}}})
		case t.Enabled != nil && !*t.Enabled:
			out = append(out, assess.Finding{Subject: templateSubject(in.Model, name), Detail: "bound_service_template_disabled", Actual: refs[name], Severity: assess.SeverityMedium,
				Remediation: &assess.Remediation{Status: assess.RemediationBlocked, Blockers: []string{"需确认是否有意停用及恢复窗口，不能仅凭绑定关系自动启用业务。"}}})
		}
	}
	return out
})

var ipConflictHistory = assess.NewRule(assess.RuleMeta{
	ID: "wireless.client_ip_conflict_history", Title: "存在历史客户端 IP 冲突记录", Severity: assess.SeverityLow, Category: assess.CategoryRuntime,
	Verify: []evidence.Dimension{wlan.DimIPConflicts, wlan.DimClients},
}, func(in input) []assess.Finding {
	if !h3c(in.Model) || len(in.Model.IPConflicts) == 0 {
		return nil
	}
	var out []assess.Finding
	seen := map[string]bool{}
	for _, e := range in.Model.IPConflicts {
		if seen[e.IPAddress] {
			continue
		}
		seen[e.IPAddress] = true
		out = append(out, assess.Finding{Subject: controller(in.Model).Child("client_ip", e.IPAddress), Actual: e,
			Detail: "事件时间不含年份，不据此判断当前仍冲突；在地址管理源修复重复分配"})
	}
	return out
})

// APState is an AP's normalized operational state.
type APState string

// AP states.
const (
	APRunning APState = "running"
	APDown    APState = "down"
	APUnknown APState = "unknown"
)

// NormalizeAPState maps vendor AP states: H3C "R/M", "Run", "R";
// Huawei "normal", "online".
func NormalizeAPState(raw string) APState {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case s == "":
		return APUnknown
	case s == "run" || s == "r" || strings.HasPrefix(s, "r/") || s == "normal" || s == "online":
		return APRunning
	}
	return APDown
}

var apNotRunning = assess.NewRule(assess.RuleMeta{
	ID: "wireless.ap_not_running", Title: "AP 未处于运行状态", Severity: assess.SeverityHigh, Category: assess.CategoryRuntime,
	Verify: []evidence.Dimension{wlan.DimAccessPoints},
}, func(in input) []assess.Finding {
	var out []assess.Finding
	for _, ap := range in.Model.AccessPoints {
		if NormalizeAPState(ap.OperationalStatus) == APDown {
			out = append(out, assess.Finding{Subject: controller(in.Model).Child("ap", apName(ap)), Actual: ap.OperationalStatus,
				Detail: "按供电/交换机端口、管理 VLAN/DHCP、CAPWAP 连通性、授权和版本顺序定位，不自动重启"})
		}
	}
	return out
})

func apName(ap wlan.AccessPoint) string {
	if ap.Name != "" {
		return ap.Name
	}
	return ap.APID
}

var radioAdminDown = assess.NewRule(assess.RuleMeta{
	ID: "wireless.radio_admin_down", Title: "存在管理状态 Down 的射频", Severity: assess.SeverityLow, Category: assess.CategoryRuntime,
	Verify: []evidence.Dimension{wlan.DimAccessPoints, wlan.DimConfiguration},
}, func(in input) []assess.Finding {
	if !h3c(in.Model) {
		return nil
	}
	var out []assess.Finding
	for _, r := range in.Model.Radios {
		if strings.EqualFold(r.AdminStatus, "down") {
			out = append(out, assess.Finding{Subject: radioSubject(in.Model, r), Actual: r.AdminStatus,
				Detail: "主动关闭频段可能是正常设计；核对 AP、AP 组及型号配置的继承关系，不批量开启"})
		}
	}
	return out
})

func radioSubject(c *wlan.Controller, r wlan.Radio) assess.Subject {
	return controller(c).Child("ap", r.APName).Child("radio", r.RadioID)
}

var firmwareDrift = assess.NewRule(assess.RuleMeta{
	ID: "wireless.ap_firmware_drift", Title: "同型号 AP 的软件版本不一致", Severity: assess.SeverityMedium, Category: assess.CategoryConsistency,
	Requires: []evidence.Dimension{wlan.DimAccessPoints}, Verify: []evidence.Dimension{wlan.DimAccessPoints, wlan.DimAPModels},
}, func(in input) []assess.Finding {
	byModel := map[string][]drift.Member[string]{}
	for _, ap := range in.Model.AccessPoints {
		if ap.Model == "" || NormalizeAPState(ap.OperationalStatus) != APRunning {
			continue
		}
		byModel[ap.Model] = append(byModel[ap.Model], drift.Member[string]{
			Subject: controller(in.Model).Child("ap_model", ap.Model).Child("ap", apName(ap)), Value: ap.OSVersion, Present: ap.OSVersion != ""})
	}
	var out []assess.Finding
	for _, model := range slices.Sorted(maps.Keys(byModel)) {
		_, _, devs := drift.Consensus("os_version", byModel[model])
		for _, d := range devs {
			f := d.Finding("os_version " + string(d.Kind))
			f.Category = assess.CategoryConsistency
			out = append(out, f)
		}
	}
	return out
})

// band24 reports whether a radio channel is in the 2.4 GHz band.
func band24(r wlan.Radio) bool { return r.Channel != nil && *r.Channel >= 1 && *r.Channel <= 14 }

var channel24 = assess.NewRule(assess.RuleMeta{
	ID: "wireless.radio_24g_overlapping_channel", Title: "2.4 GHz 射频使用了非 1/6/11 的重叠信道", Severity: assess.SeverityLow, Category: assess.CategoryConfiguration,
	Requires: []evidence.Dimension{wlan.DimRadios}, Params: assess.Params{"allowed_channels": []int{1, 6, 11}},
}, func(in input) []assess.Finding {
	allowed := in.Params.Ints("allowed_channels", []int{1, 6, 11})
	var out []assess.Finding
	for _, r := range in.Model.Radios {
		if band24(r) && !slices.Contains(allowed, *r.Channel) {
			out = append(out, assess.Finding{Subject: radioSubject(in.Model, r), Actual: *r.Channel, Expected: allowed})
		}
	}
	return out
})

var wide24 = assess.NewRule(assess.RuleMeta{
	ID: "wireless.radio_24g_wide_channel", Title: "2.4 GHz 射频使用 40 MHz 及以上信道带宽", Severity: assess.SeverityMedium, Category: assess.CategoryConfiguration,
	Requires: []evidence.Dimension{wlan.DimRadios},
}, func(in input) []assess.Finding {
	var out []assess.Finding
	for _, r := range in.Model.Radios {
		if !band24(r) || r.ChannelWidth == nil {
			continue
		}
		if w, err := r.ChannelWidth.Int64(); err == nil && w >= 40 {
			out = append(out, assess.Finding{Subject: radioSubject(in.Model, r), Actual: w, Expected: 20})
		}
	}
	return out
})

var utilization = assess.NewRule(assess.RuleMeta{
	ID: "wireless.radio_utilization_high", Title: "射频信道利用率过高", Severity: assess.SeverityMedium, Category: assess.CategoryCapacity,
	Requires: []evidence.Dimension{wlan.DimRadios}, Params: assess.Params{"max_percent": 70},
}, func(in input) []assess.Finding {
	limit := in.Params.Int("max_percent", 70)
	var out []assess.Finding
	for _, r := range in.Model.Radios {
		if r.Utilization != nil && *r.Utilization > limit {
			out = append(out, assess.Finding{Subject: radioSubject(in.Model, r), Actual: *r.Utilization, Expected: fmt.Sprintf("<= %d%%", limit)})
		}
	}
	return out
})

var noise = assess.NewRule(assess.RuleMeta{
	ID: "wireless.radio_noise_high", Title: "射频底噪过高", Severity: assess.SeverityLow, Category: assess.CategoryCapacity,
	Requires: []evidence.Dimension{wlan.DimRadios}, Params: assess.Params{"max_dbm": -80},
}, func(in input) []assess.Finding {
	limit := in.Params.Float("max_dbm", -80)
	var out []assess.Finding
	for _, r := range in.Model.Radios {
		if r.NoiseDBm != nil && *r.NoiseDBm > limit {
			out = append(out, assess.Finding{Subject: radioSubject(in.Model, r), Actual: *r.NoiseDBm, Expected: fmt.Sprintf("<= %g dBm", limit)})
		}
	}
	return out
})

var apClientLoad = assess.NewRule(assess.RuleMeta{
	ID: "wireless.ap_client_load_high", Title: "单 AP 关联客户端数超出阈值", Severity: assess.SeverityMedium, Category: assess.CategoryCapacity,
	Requires: []evidence.Dimension{wlan.DimAccessPoints}, Params: assess.Params{"max_clients": 64},
}, func(in input) []assess.Finding {
	limit := int64(in.Params.Int("max_clients", 64))
	var out []assess.Finding
	for _, ap := range in.Model.AccessPoints {
		if ap.ClientCount == nil {
			continue
		}
		if n, err := ap.ClientCount.Int64(); err == nil && n > limit {
			out = append(out, assess.Finding{Subject: controller(in.Model).Child("ap", apName(ap)), Actual: n, Expected: fmt.Sprintf("<= %d", limit)})
		}
	}
	return out
})

var backupAC = assess.NewRule(assess.RuleMeta{
	ID: "wireless.ac_backup_missing", Title: "未配置备份 AC，AC 单点故障", Severity: assess.SeverityMedium, Category: assess.CategoryConfiguration,
	Requires: []evidence.Dimension{wlan.DimConfiguration},
}, func(in input) []assess.Finding {
	cfg := config(in.Model)
	if cfg == nil || len(cfg.AccessPoints)+len(cfg.APGroups) == 0 {
		return nil
	}
	if cfg.GlobalSettings.BackupACIP != "" || (cfg.GlobalSettings.APLicenseGroup != nil && len(cfg.GlobalSettings.APLicenseGroup.MemberIPs) > 0) {
		return nil
	}
	for _, g := range cfg.APGroups {
		if g.Configuration.BackupACIP != "" {
			return nil
		}
	}
	for _, ap := range cfg.AccessPoints {
		if ap.BackupACIP != "" {
			return nil
		}
	}
	return []assess.Finding{{Subject: controller(in.Model), Detail: "全局、AP 组和 AP 均未配置 backup-ac，且无 License 同步组成员"}}
})

// Commands resolves collection commands for verification hints.
type Commands interface {
	Command(dimension evidence.Dimension) (string, error)
}

// Report is the schema 3 wireless assessment.
type Report struct {
	SchemaVersion int                    `json:"schema_version"`
	Controllers   []ControllerAssessment `json:"controllers"`
	// Baseline holds drift from the declared intent, when a baseline is given.
	Baseline []ControllerAssessment `json:"baseline,omitempty"`
	// Changes holds per-controller changes since a previous batch.
	Changes     []ControllerAssessment  `json:"changes,omitempty"`
	Appeared    []string                `json:"appeared,omitempty"`
	Disappeared []string                `json:"disappeared,omitempty"`
	Counts      map[assess.Severity]int `json:"counts"`
	Highest     assess.Severity         `json:"highest,omitempty"`
	Incomplete  bool                    `json:"incomplete"`
}

// ControllerAssessment is one controller's result.
type ControllerAssessment struct {
	Controller wlan.Identity     `json:"controller"`
	Coverage   evidence.Coverage `json:"coverage"`
	Assessment assess.Assessment `json:"assessment"`
}

// Inputs are optional comparison points for an assessment.
type Inputs struct {
	Baseline *wlan.Baseline
	Previous []*wlan.Controller
}

// Assessor runs the catalogs under one policy.
type Assessor struct {
	engine   *assess.Engine[*wlan.Controller]
	baseline *assess.Engine[*BaselineScope]
	temporal *assess.Engine[*Change]
}

func subset[M any](p assess.Policy, c *assess.Catalog[M]) assess.Policy {
	out := assess.Policy{OnlyEnabled: p.OnlyEnabled, Rules: map[assess.RuleID]assess.RulePolicy{}}
	for id, rp := range p.Rules {
		if _, ok := c.Lookup(id); ok {
			out.Rules[id] = rp
		}
	}
	return out
}

// NewAssessor validates the policy against all catalogs; a rule named in the
// policy must exist in one of them.
func NewAssessor(policy assess.Policy, commands Commands) (*Assessor, error) {
	catalog, err := assess.NewCatalog(Rules()...)
	if err != nil {
		return nil, err
	}
	baselineCatalog, err := assess.NewCatalog(BaselineRules()...)
	if err != nil {
		return nil, err
	}
	temporalCatalog, err := assess.NewCatalog(TemporalRules()...)
	if err != nil {
		return nil, err
	}
	for id := range policy.Rules {
		_, a := catalog.Lookup(id)
		_, b := baselineCatalog.Lookup(id)
		_, c := temporalCatalog.Lookup(id)
		if !a && !b && !c {
			return nil, fmt.Errorf("%w: policy names unknown rule %s", assess.ErrInvalid, id)
		}
	}
	var verify assess.VerifyCommands
	if commands != nil {
		verify = commands
	}
	engine, err := assess.NewEngine(catalog, subset(policy, catalog), verify)
	if err != nil {
		return nil, err
	}
	be, err := assess.NewEngine(baselineCatalog, subset(policy, baselineCatalog), verify)
	if err != nil {
		return nil, err
	}
	te, err := assess.NewEngine(temporalCatalog, subset(policy, temporalCatalog), verify)
	if err != nil {
		return nil, err
	}
	return &Assessor{engine: engine, baseline: be, temporal: te}, nil
}

// Assess evaluates every controller.
func (a *Assessor) Assess(ctx context.Context, controllers []*wlan.Controller) (Report, error) {
	return a.AssessWith(ctx, controllers, Inputs{})
}

// AssessWith also compares controllers with a baseline and a previous batch.
func (a *Assessor) AssessWith(ctx context.Context, controllers []*wlan.Controller, in Inputs) (Report, error) {
	r := Report{SchemaVersion: assess.SchemaVersion, Controllers: []ControllerAssessment{}, Counts: map[assess.Severity]int{}}
	for _, c := range controllers {
		as, err := a.engine.Run(ctx, c, c.Coverage)
		if err != nil {
			return Report{}, err
		}
		r.add(as)
		r.Controllers = append(r.Controllers, ControllerAssessment{Controller: c.Identity, Coverage: c.Coverage, Assessment: as})
	}
	if in.Baseline != nil {
		if err := a.assessBaseline(ctx, &r, controllers, in.Baseline); err != nil {
			return Report{}, err
		}
	}
	if in.Previous != nil {
		if err := a.assessChanges(ctx, &r, controllers, in.Previous); err != nil {
			return Report{}, err
		}
	}
	return r, nil
}

// add folds one assessment into the report totals.
func (r *Report) add(as assess.Assessment) {
	for sev, n := range as.Counts {
		r.Counts[sev] += n
	}
	r.Highest = max(r.Highest, as.Highest())
	r.Incomplete = r.Incomplete || as.Incomplete()
}

// assessBaseline compares every controller with its baseline expectation.
func (a *Assessor) assessBaseline(ctx context.Context, r *Report, controllers []*wlan.Controller, b *wlan.Baseline) error {
	if err := b.Validate(); err != nil {
		return err
	}
	for _, c := range controllers {
		as, err := a.baseline.Run(ctx, &BaselineScope{Controller: c, Expect: b.For(c.Host)}, c.Coverage)
		if err != nil {
			return err
		}
		r.add(as)
		r.Baseline = append(r.Baseline, ControllerAssessment{Controller: c.Identity, Coverage: c.Coverage, Assessment: as})
	}
	return nil
}

// assessChanges compares every controller with its previous snapshot and
// records controllers that appeared or disappeared; a disappeared controller
// makes the report incomplete.
func (a *Assessor) assessChanges(ctx context.Context, r *Report, controllers, previous []*wlan.Controller) error {
	before := map[string]*wlan.Controller{}
	for _, c := range previous {
		before[c.Host] = c
	}
	now := map[string]bool{}
	for _, c := range controllers {
		now[c.Host] = true
		p, ok := before[c.Host]
		if !ok {
			r.Appeared = append(r.Appeared, c.Host)
			continue
		}
		change := &Change{Previous: p, Current: c}
		as, err := a.temporal.Run(ctx, change, change.Coverage())
		if err != nil {
			return err
		}
		r.add(as)
		r.Changes = append(r.Changes, ControllerAssessment{Controller: c.Identity, Coverage: change.Coverage(), Assessment: as})
	}
	for _, c := range previous {
		if !now[c.Host] {
			r.Disappeared = append(r.Disappeared, c.Host)
		}
	}
	slices.Sort(r.Appeared)
	slices.Sort(r.Disappeared)
	r.Incomplete = r.Incomplete || len(r.Disappeared) > 0
	return nil
}
