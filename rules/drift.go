package rules

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/drift"
	"github.com/gatework/go-netops/evidence"
	"github.com/gatework/go-wireless/wlan"
)

// BaselineScope is one controller with its effective declared intent.
type BaselineScope struct {
	Controller *wlan.Controller
	Expect     wlan.Expectation
}

// Change is one controller observed in two batches.
type Change struct {
	Previous, Current *wlan.Controller
}

// Coverage is complete for a dimension only when both snapshots have it.
func (c *Change) Coverage() evidence.Coverage {
	out := evidence.Coverage{}
	for _, dim := range wlan.Dimensions() {
		if c.Previous.Has(dim) && c.Current.Has(dim) {
			out[dim] = evidence.StatusComplete
		} else {
			out[dim] = evidence.StatusPartial
		}
	}
	return out
}

func meta(id assess.RuleID, title string, sev assess.Severity, dims ...evidence.Dimension) assess.RuleMeta {
	return assess.RuleMeta{ID: id, Title: title, Severity: sev, Category: assess.CategoryDrift, Requires: dims, Verify: dims}
}

func df(d drift.Deviation) assess.Finding { return d.Finding(d.Path + " " + string(d.Kind)) }

// BaselineRules compare each controller with its declared intent.
func BaselineRules() []assess.Rule[*BaselineScope] {
	return []assess.Rule[*BaselineScope]{baselineTemplates, baselineFirmware, baselineBackupAC}
}

var baselineTemplates = assess.NewRule(meta("wireless.baseline_service_template", "服务模板（SSID/VLAN/安全）偏离基线", assess.SeverityHigh, wlan.DimConfiguration), func(in assess.Input[*BaselineScope]) []assess.Finding {
	c, want := in.Model.Controller, in.Model.Expect.ServiceTemplates
	if c.Configuration == nil || len(want) == 0 {
		return nil
	}
	have := map[string]wlan.ServiceTemplate{}
	for _, t := range c.Configuration.ServiceTemplates {
		have[t.Name] = t
	}
	var out []assess.Finding
	for _, name := range slices.Sorted(maps.Keys(want)) {
		e, subject := want[name], templateSubject(c, name)
		t, ok := have[name]
		if !ok {
			out = append(out, assess.Finding{Category: assess.CategoryDrift, Subject: subject, Detail: "service_template missing"})
			continue
		}
		str := func(p *string) (string, bool) {
			if p == nil {
				return "", false
			}
			return *p, true
		}
		if e.SSID != nil {
			got, present := str(t.SSID)
			if d, ok := drift.Expect(subject, "ssid", *e.SSID, got, present); ok {
				out = append(out, df(d))
			}
		}
		if e.VLAN != nil {
			got, present := 0, t.VLANID != nil
			if present {
				got = *t.VLANID
			}
			if d, ok := drift.Expect(subject, "vlan", *e.VLAN, got, present); ok {
				out = append(out, df(d))
			}
		}
		if e.Enabled != nil {
			got := t.Enabled != nil && *t.Enabled
			if d, ok := drift.Expect(subject, "enabled", *e.Enabled, got, t.Enabled != nil); ok {
				out = append(out, df(d))
			}
		}
		for _, set := range []struct {
			path       string
			want, have []string
		}{{"akm_modes", e.AKMModes, t.Configuration.AKMModes}, {"cipher_suites", e.CipherSuites, t.Configuration.CipherSuites}, {"security_ies", e.SecurityIEs, t.Configuration.SecurityIEs}} {
			if set.want != nil {
				for _, d := range drift.Sets(subject, set.path, set.want, set.have) {
					out = append(out, df(d))
				}
			}
		}
	}
	return out
})

var baselineFirmware = assess.NewRule(meta("wireless.baseline_ap_firmware", "AP 软件版本偏离基线", assess.SeverityMedium, wlan.DimAccessPoints), func(in assess.Input[*BaselineScope]) []assess.Finding {
	want := in.Model.Expect.APFirmware
	var out []assess.Finding
	for _, ap := range in.Model.Controller.AccessPoints {
		v, declared := want[ap.Model]
		if !declared || NormalizeAPState(ap.OperationalStatus) != APRunning {
			continue
		}
		if d, ok := drift.Expect(controller(in.Model.Controller).Child("ap", apName(ap)), "os_version", v, ap.OSVersion, ap.OSVersion != ""); ok {
			out = append(out, df(d))
		}
	}
	return out
})

var baselineBackupAC = assess.NewRule(meta("wireless.baseline_backup_ac", "备份 AC 偏离基线", assess.SeverityMedium, wlan.DimConfiguration), func(in assess.Input[*BaselineScope]) []assess.Finding {
	want, c := in.Model.Expect.BackupAC, in.Model.Controller
	if want == nil || c.Configuration == nil {
		return nil
	}
	got := c.Configuration.GlobalSettings.BackupACIP
	if d, ok := drift.Expect(controller(c), "backup_ac", *want, got, got != ""); ok {
		return []assess.Finding{df(d)}
	}
	return nil
})

// TemporalRules report what changed since the previous batch.
func TemporalRules() []assess.Rule[*Change] {
	return []assess.Rule[*Change]{apsLost, apsDown, uplinkChanged, firmwareChanged, templatesChanged}
}

func apsByName(c *wlan.Controller) map[string]wlan.AccessPoint {
	out := map[string]wlan.AccessPoint{}
	for _, ap := range c.AccessPoints {
		out[apName(ap)] = ap
	}
	return out
}

var apsLost = assess.NewRule(meta("wireless.change_ap_disappeared", "上次在线的 AP 已不在 AP 列表中", assess.SeverityHigh, wlan.DimAccessPoints), func(in assess.Input[*Change]) []assess.Finding {
	before, after := apsByName(in.Model.Previous), apsByName(in.Model.Current)
	var out []assess.Finding
	for _, name := range slices.Sorted(maps.Keys(before)) {
		if _, ok := after[name]; !ok && NormalizeAPState(before[name].OperationalStatus) == APRunning {
			out = append(out, assess.Finding{Subject: controller(in.Model.Current).Child("ap", name), Expected: "present", Actual: "absent"})
		}
	}
	return out
})

var apsDown = assess.NewRule(meta("wireless.change_ap_down", "AP 自上次采集后由运行转为非运行", assess.SeverityHigh, wlan.DimAccessPoints), func(in assess.Input[*Change]) []assess.Finding {
	before, after := apsByName(in.Model.Previous), apsByName(in.Model.Current)
	var out []assess.Finding
	for _, name := range slices.Sorted(maps.Keys(after)) {
		p, ok := before[name]
		if ok && NormalizeAPState(p.OperationalStatus) == APRunning && NormalizeAPState(after[name].OperationalStatus) == APDown {
			out = append(out, assess.Finding{Subject: controller(in.Model.Current).Child("ap", name), Expected: p.OperationalStatus, Actual: after[name].OperationalStatus})
		}
	}
	return out
})

func uplinks(c *wlan.Controller) map[string]string {
	out := map[string]string{}
	for _, n := range c.LLDP {
		if n.APName == "" {
			continue
		}
		key := n.APName + "/" + n.LocalInterface
		out[key] = strings.TrimSpace(n.RemoteName + " " + n.RemoteInterface)
	}
	return out
}

var uplinkChanged = assess.NewRule(meta("wireless.change_ap_uplink", "AP 上联交换机或端口发生变化", assess.SeverityMedium, wlan.DimLLDP), func(in assess.Input[*Change]) []assess.Finding {
	subject := func(k string) assess.Subject { return controller(in.Model.Current).Child("ap_port", k) }
	var out []assess.Finding
	for _, d := range drift.Changes(subject, "lldp.uplink", uplinks(in.Model.Previous), uplinks(in.Model.Current)) {
		if d.Kind == drift.KindChanged { // appearance/disappearance is covered by AP rules
			out = append(out, df(d))
		}
	}
	return out
})

func versions(c *wlan.Controller) map[string]string {
	out := map[string]string{}
	for _, ap := range c.AccessPoints {
		if ap.OSVersion != "" {
			out[apName(ap)] = ap.OSVersion
		}
	}
	return out
}

var firmwareChanged = assess.NewRule(meta("wireless.change_ap_firmware", "AP 软件版本自上次采集后变化", assess.SeverityInfo, wlan.DimAccessPoints), func(in assess.Input[*Change]) []assess.Finding {
	subject := func(k string) assess.Subject { return controller(in.Model.Current).Child("ap", k) }
	var out []assess.Finding
	for _, d := range drift.Changes(subject, "os_version", versions(in.Model.Previous), versions(in.Model.Current)) {
		if d.Kind == drift.KindChanged {
			out = append(out, df(d))
		}
	}
	return out
})

func templateStates(c *wlan.Controller) map[string]string {
	out := map[string]string{}
	if c.Configuration == nil {
		return out
	}
	for _, t := range c.Configuration.ServiceTemplates {
		ssid, vlan, enabled := "", "", ""
		if t.SSID != nil {
			ssid = *t.SSID
		}
		if t.VLANID != nil {
			vlan = strconv.Itoa(*t.VLANID)
		}
		if t.Enabled != nil {
			enabled = map[bool]string{true: "enabled", false: "disabled"}[*t.Enabled]
		}
		out[t.Name] = strings.Join([]string{"ssid=" + ssid, "vlan=" + vlan, enabled,
			"akm=" + strings.Join(t.Configuration.AKMModes, ","), "cipher=" + strings.Join(t.Configuration.CipherSuites, ","), "ie=" + strings.Join(t.Configuration.SecurityIEs, ",")}, " ")
	}
	return out
}

var templatesChanged = assess.NewRule(meta("wireless.change_service_template", "服务模板新增、删除或 SSID/VLAN/安全变化", assess.SeverityMedium, wlan.DimConfiguration), func(in assess.Input[*Change]) []assess.Finding {
	subject := func(k string) assess.Subject { return templateSubject(in.Model.Current, k) }
	var out []assess.Finding
	for _, d := range drift.Changes(subject, "service_template", templateStates(in.Model.Previous), templateStates(in.Model.Current)) {
		f := df(d)
		if d.Kind == drift.KindMissing {
			f.Severity = assess.SeverityHigh
		}
		out = append(out, f)
	}
	return out
})

// DeriveBaseline proposes a baseline from a controller's current state: its
// service templates as configured and the majority AP firmware per model.
// It is a draft for operators to review, not proof the state is correct.
func DeriveBaseline(controllers []*wlan.Controller) wlan.Baseline {
	b := wlan.Baseline{SchemaVersion: wlan.BaselineSchemaVersion, Controllers: map[string]wlan.Expectation{}}
	for _, c := range controllers {
		e := wlan.Expectation{}
		if h3c(c) && c.Has(wlan.DimConfiguration) && c.Configuration != nil {
			e.ServiceTemplates = map[string]wlan.TemplateExpectation{}
			for _, t := range c.Configuration.ServiceTemplates {
				e.ServiceTemplates[t.Name] = wlan.TemplateExpectation{SSID: t.SSID, VLAN: t.VLANID, Enabled: t.Enabled,
					AKMModes: nonNil(t.Configuration.AKMModes), CipherSuites: nonNil(t.Configuration.CipherSuites), SecurityIEs: nonNil(t.Configuration.SecurityIEs)}
			}
			if ip := c.Configuration.GlobalSettings.BackupACIP; ip != "" {
				e.BackupAC = &ip
			}
		}
		if c.Has(wlan.DimAccessPoints) {
			byModel := map[string][]drift.Member[string]{}
			for _, ap := range c.AccessPoints {
				if ap.Model != "" && ap.OSVersion != "" {
					byModel[ap.Model] = append(byModel[ap.Model], drift.Member[string]{Value: ap.OSVersion, Present: true})
				}
			}
			for model, members := range byModel {
				if v, ok, _ := drift.Consensus("", members); ok {
					if e.APFirmware == nil {
						e.APFirmware = map[string]string{}
					}
					e.APFirmware[model] = v
				}
			}
		}
		b.Controllers[c.Host] = e
	}
	return b
}
