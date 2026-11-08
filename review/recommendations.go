package review

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"

	recordvalue "github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	changes "github.com/gatework/go-wireless/change"
)

// ObjectOrder retains source object key order for derived evidence arrays.
// Arrays always retain input order. Live Go maps use lexical key order.

// DecodeReviewJSON accepts historical generic reports, including snapshot:null;
// it intentionally does not impose live Snapshot construction invariants.
func DecodeReviewJSON(body []byte) (wireless.Record, wireless.ObjectOrder, error) {
	m, order, e := wireless.DecodeJSONDocument(body)
	if e != nil {
		return nil, nil, e
	}
	if e = ValidateReviewDocument(m); e != nil {
		return nil, nil, e
	}
	return m, order, nil
}
func ValidateReviewDocument(doc wireless.Record) error {
	device, ok := doc["device"].(wireless.Record)
	if !ok {
		return changes.ErrChangePlan
	}
	if _, ok = device["name"].(string); !ok {
		return changes.ErrChangePlan
	}
	if _, ok = device["host"].(string); !ok {
		return changes.ErrChangePlan
	}
	s, exists := doc["snapshot"]
	if !exists {
		return changes.ErrChangePlan
	}
	if s == nil {
		return nil
	}
	snapshot, ok := s.(wireless.Record)
	if !ok {
		return changes.ErrChangePlan
	}
	dims, ok := snapshot["dimensions"].(wireless.Record)
	if !ok {
		return changes.ErrChangePlan
	}
	for dimension, value := range dims {
		o, ok := value.(wireless.Record)
		if !ok {
			return changes.ErrChangePlan
		}
		status, _ := o["status"].(string)
		if !slices.Contains([]string{"complete", "partial", "failed", "unsupported", "not_requested"}, status) || !allRecords(o["records"]) {
			return changes.ErrChangePlan
		}
		if issues, present := o["issues"]; present && !allRecords(issues) {
			return changes.ErrChangePlan
		}
		for _, record := range recordvalue.Records(o["records"]) {
			if dimension == "access_points" && record["operational_status"] != nil {
				if _, ok := record["operational_status"].(string); !ok {
					return changes.ErrChangePlan
				}
			}
			if dimension != "configuration" {
				continue
			}
			if v, p := record["access_points"]; p {
				m, ok := v.(wireless.Record)
				if !ok {
					return changes.ErrChangePlan
				}
				for _, a := range m {
					if _, ok := a.(wireless.Record); !ok {
						return changes.ErrChangePlan
					}
				}
			}
			if v, p := record["ap_groups"]; p && !allRecords(v) {
				return changes.ErrChangePlan
			}
			if v, p := record["service_templates"]; p {
				if !allRecords(v) {
					return changes.ErrChangePlan
				}
				for _, t := range recordvalue.Records(v) {
					settings := wireless.Record{}
					if s, p := t["configuration"]; p {
						var ok bool
						settings, ok = s.(wireless.Record)
						if !ok {
							return changes.ErrChangePlan
						}
					}
					for _, key := range []string{"cipher_suites", "security_ies"} {
						if s, p := settings[key]; p {
							if _, ok := recordvalue.Strings(s); !ok {
								return changes.ErrChangePlan
							}
						}
					}
				}
			}
		}
	}
	return nil
}
func allRecords(v any) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) || rv.Kind() == reflect.Slice && rv.IsNil() {
		return false
	}
	for i := 0; i < rv.Len(); i++ {
		if _, ok := rv.Index(i).Interface().(wireless.Record); !ok {
			return false
		}
	}
	return true
}

const recommendationReference = "https://www.h3c.com/en/d_202210/1709621_294551_0.htm"

func recommendationItem(code, scope, title, category string, evidence any, verify []string) wireless.Record {
	if verify == nil {
		verify = []string{}
	}
	return wireless.Record{"code": code, "scope": scope, "title": title, "category": category, "status": "investigate", "evidence": evidence, "prerequisites": []string{}, "blockers": []string{}, "commands": []string{}, "rollback": []string{}, "verify": verify, "expected": "重采事实并核对业务预期；没有命中建议不代表无线网络健康。", "references": []string{recommendationReference}}
}

// Recommendations produces review-only facts. Only the explicit h3c_wireless
// identity is actionable, matching Ruby; the h3c parsing alias is not widened.
func Recommendations(doc wireless.Record, orders ...wireless.ObjectOrder) wireless.Record {
	order := wireless.ObjectOrder{}
	if len(orders) > 0 {
		order = orders[0]
	}
	snapshot := recordvalue.Map(doc["snapshot"])
	dims := recordvalue.Map(snapshot["dimensions"])
	h3c := recordvalue.Map(doc["device"])["platform"] == "h3c_wireless"
	items := []wireless.Record{}
	keys := order.Keys(dims)
	selected := []string{}
	for _, name := range keys {
		if name != "collection" {
			selected = append(selected, name)
		}
	}
	if len(selected) == 0 {
		dims = wireless.Record{"configuration": wireless.Record{"status": "not_requested"}}
		selected = []string{"configuration"}
	}
	profile, _ := vendors.NewBuiltinRegistry().Fetch("h3c_wireless", "")
	for _, name := range selected {
		o := recordvalue.Map(dims[name])
		if o["status"] == "complete" {
			continue
		}
		issues := []any{}
		for _, issue := range recordvalue.Records(o["issues"]) {
			issues = append(issues, issue["code"])
		}
		verify := []string{}
		if h3c {
			if command, e := profile.Command(wireless.Dimension(name)); e == nil {
				verify = append(verify, command)
			}
		}
		items = append(items, recommendationItem("evidence_incomplete", name, "证据不完整，先补采", "evidence", wireless.Record{"status": o["status"], "issues": issues}, verify))
	}
	if h3c {
		config := recordvalue.Map(dims["configuration"])
		if config["status"] == "complete" {
			for _, c := range recordvalue.Records(config["records"]) {
				for _, t := range recordvalue.Records(c["service_templates"]) {
					if item := securityItem(t); item != nil {
						items = append(items, item)
					}
				}
			}
			for _, c := range recordvalue.Records(config["records"]) {
				items = append(items, bindingTasks(c, order)...)
			}
		}
		items = append(items, runtimeTasks(dims)...)
	} else {
		item := recommendationItem("platform_not_supported", "platform", "此平台尚未提供修复规则", "runtime", recordvalue.Map(doc["device"])["platform"], nil)
		item["status"] = "blocked"
		items = append(items, item)
	}
	counts := wireless.Record{}
	for _, item := range items {
		key := item["status"].(string)
		n, _ := counts[key].(int)
		counts[key] = n + 1
	}
	return wireless.CloneRecord(wireless.Record{"schema_version": 1, "review_only": true, "health_assessment": "not_performed", "device": doc["device"], "observed_at": snapshot["observed_at"], "items": items, "counts": counts})
}
func securityItem(t wireless.Record) wireless.Record {
	settings := recordvalue.Map(t["configuration"])
	c, _ := recordvalue.Strings(settings["cipher_suites"])
	ies, _ := recordvalue.Strings(settings["security_ies"])
	if !slices.Contains(c, "tkip") && !slices.Contains(ies, "wpa") {
		return nil
	}
	if c == nil {
		c = []string{}
	}
	if ies == nil {
		ies = []string{}
	}
	name := t["name"]
	scope := "service-template " + recommendationName(name)
	result := recommendationItem("legacy_wlan_security", scope, "服务模板保留 TKIP/WPA 兼容配置", "configuration", wireless.Record{"name": name, "enabled": t["enabled"], "cipher_suites": c, "security_ies": ies}, []string{"display current-configuration", "display wlan client verbose", "display wlan bss all verbose"})
	result["prerequisites"] = []string{"此项是加固建议，不代表当前业务故障；确认所有受影响终端支持 CCMP/RSN，且业务允许移除旧协议。", "核对软件命令支持和模板绑定范围；维护窗口内操作，停用模板会断开相关终端。", "重新采集并确认配置未变化；保留回退脚本。出现异常先回读，勿盲目重放。"}
	result["impact"] = "停用服务模板会中断所绑定 WLAN 的客户端连接，旧终端可能无法重连。"
	result["expected"] = "保留 CCMP/RSN、模板恢复原启停状态；代表性终端重新关联和认证成功，业务验收通过。"
	change := wireless.Record{"kind": "wireless_security", "name": name, "enabled": t["enabled"], "cipher_suites": c, "security_ies": ies}
	// Ruby recommendations permit scalar name.to_s; typed plans deliberately
	// require an actual string, so these historical candidates never become plans.
	rendered := wireless.Record{}
	for key, value := range change {
		rendered[key] = value
	}
	rendered["name"] = recommendationName(name)
	commands, rollback, e := changes.RenderChange(rendered)
	if e != nil {
		result["status"] = "blocked"
		result["blockers"] = []string{"需确认模板启停状态、合法名称、CCMP/RSN 替代配置；缺少任一项不生成变更。"}
		return result
	}
	result["status"] = "candidate"
	result["change"] = change
	result["commands"] = commands
	result["rollback"] = rollback
	return result
}
func bindingTasks(config wireless.Record, order wireless.ObjectOrder) []wireless.Record {
	templates := map[string]wireless.Record{}
	for _, t := range recordvalue.Records(config["service_templates"]) {
		key, _ := wireless.CanonicalJSON(t["name"])
		templates[string(key)] = t
	}
	refs := []wireless.Record{}
	for _, key := range []string{"access_points", "ap_groups"} {
		refs = appendTemplateReferences(refs, config[key], key, order)
	}
	names := []string{}
	groups := map[string][]wireless.Record{}
	for _, ref := range refs {
		encoded, _ := wireless.CanonicalJSON(ref["name"])
		name := string(encoded)
		if _, ok := groups[name]; !ok {
			names = append(names, name)
		}
		groups[name] = append(groups[name], ref)
	}
	items := []wireless.Record{}
	for _, name := range names {
		t := templates[name]
		if t != nil && t["enabled"] != false {
			continue
		}
		code, title, blocker := "bound_service_template_disabled", "已绑定的服务模板被明确禁用", "需确认是否有意停用及恢复窗口，不能仅凭绑定关系自动启用业务。"
		if t == nil {
			code, title, blocker = "service_template_missing", "AP/组引用了未定义的服务模板", "需提供模板 SSID、安全认证、转发位置及 VLAN 设计，不能自动创建开放 WLAN。"
		}
		item := recommendationItem(code, "service-template "+recommendationName(groups[name][0]["name"]), title, "configuration", groups[name], []string{"display current-configuration", "display wlan bss all verbose"})
		item["status"] = "blocked"
		item["blockers"] = []string{blocker}
		item["expected"] = "所有绑定引用存在且启停状态符合业务意图；运行 BSS 和实际认证验证通过。"
		items = append(items, item)
	}
	return items
}

// appendTemplateReferences keeps one ordered destination across AP/radio/group
// traversal, avoiding a temporary slice and copy at every nested scope.
func appendTemplateReferences(result []wireless.Record, value any, path string, order wireless.ObjectOrder) []wireless.Record {
	if m, ok := value.(wireless.Record); ok {
		for _, key := range order.Keys(m) {
			child := m[key]
			rv := reflect.ValueOf(child)
			if key == "service_templates" && rv.IsValid() && (rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice) {
				for _, entry := range recordvalue.Records(child) {
					if entry["name"] != nil && entry["name"] != false {
						result = append(result, wireless.Record{"name": entry["name"], "path": path, "vlan_id": entry["vlan_id"]})
					}
				}
			} else {
				result = appendTemplateReferences(result, child, path+"/"+key, order)
			}
		}
		return result
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice) {
		for i := 0; i < rv.Len(); i++ {
			child := rv.Index(i).Interface()
			label := fmt.Sprint(i)
			if m, ok := child.(wireless.Record); ok {
				if name, p := m["name"]; p {
					label = ""
					if name != nil {
						label = recommendationName(name)
					}
				}
			}
			result = appendTemplateReferences(result, child, path+"/"+label, order)
		}
	}
	return result
}
func runtimeTasks(dims wireless.Record) []wireless.Record {
	records := func(name string) []wireless.Record {
		return recordvalue.Records(recordvalue.Map(dims[name])["records"])
	}
	items := []wireless.Record{}
	conflicts := records("client_ip_conflicts")
	if len(conflicts) > 0 {
		item := recommendationItem("client_ip_conflict_history", "client_ip_conflicts", "存在历史客户端 IP 冲突记录", "runtime", conflicts, []string{"display wlan statistics client-ip-conflict", "display wlan client verbose", "display arp"})
		item["prerequisites"] = []string{"事件时间不含年份，不据此判断当前仍冲突；先重采并对照新旧 MAC/AP、DHCP 租约、静态地址和漫游记录。", "定位具体地址的两个使用者后，在地址管理源修复重复分配；不自动清表、踢客户端或关闭冲突检测。"}
		item["expected"] = "复核窗口内不再新增同一地址冲突，DHCP/静态地址分配唯一且终端通信正常。"
		items = append(items, item)
	}
	offline := []wireless.Record{}
	for _, ap := range records("access_points") {
		state, ok := ap["operational_status"].(string)
		if ok && !slices.Contains([]string{"run", "r"}, strings.ToLower(state)) {
			offline = append(offline, pickFields(ap, "name", "ap_id", "operational_status", "management_ip"))
		}
	}
	if len(offline) > 0 {
		item := recommendationItem("aps_not_running", "access_points", "AP 未处于 Run 状态", "runtime", offline, []string{"display wlan ap all verbose", "display wlan ap all"})
		item["prerequisites"] = []string{"核对这些 AP 是否应在线；按供电/交换机端口、管理 VLAN/DHCP、CAPWAP 连通性、授权和版本顺序定位，不自动重启。"}
		items = append(items, item)
	}
	down := []wireless.Record{}
	for _, radio := range records("radios") {
		state, _ := radio["admin_status"].(string)
		if strings.EqualFold(state, "down") {
			down = append(down, pickFields(radio, "ap_name", "radio_id", "admin_status", "protocol"))
		}
	}
	if len(down) > 0 {
		item := recommendationItem("radios_admin_down", "radios", "存在管理状态 Down 的射频", "runtime", down, []string{"display wlan ap all verbose", "display current-configuration"})
		item["prerequisites"] = []string{"核对 AP 自身、AP 组及型号配置的继承关系和射频规划；主动关闭频段可能是正常设计，不批量开启。"}
		items = append(items, item)
	}
	return items
}
func pickFields(m wireless.Record, keys ...string) wireless.Record {
	r := wireless.Record{}
	for _, k := range keys {
		if v, p := m[k]; p {
			r[k] = v
		}
	}
	return r
}

func recommendationName(value any) string {
	if value == nil {
		return ""
	}
	switch n := value.(type) {
	case json.Number:
		if strings.ContainsAny(string(n), ".eE") {
			if f, e := n.Float64(); e == nil {
				return recommendationFloat(f)
			}
		}
		if integer, ok := new(big.Int).SetString(string(n), 10); ok {
			return integer.String()
		}
	case float64:
		return recommendationFloat(n)
	case float32:
		return recommendationFloat(float64(n))
	}
	return fmt.Sprint(value)
}

// Ruby Float#to_s, used in historical names, has different display thresholds
// from JSON gem fpconv. This is advisory interpolation, never a fingerprint.
func recommendationFloat(value float64) string {
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(scientific, "e")
	exp, _ := strconv.Atoi(exponent)
	if exp >= -4 && exp < 15 {
		s := strconv.FormatFloat(value, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	if !strings.Contains(mantissa, ".") {
		mantissa += ".0"
	}
	return mantissa + "e" + exponent
}
