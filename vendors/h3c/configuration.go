package h3c

import (
	"errors"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"

	wireless "github.com/gatework/go-wireless"
	recordvalue "github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/parser"
)

type configurationScope struct {
	kind   string
	record wireless.Record
	path   []any
}

// Scope heading and list patterns, compiled once.
var (
	groupTypeSuffix = regexp.MustCompile(`(?i)^type \S+$`)
	apModelSuffix   = regexp.MustCompile(`(?i)^model \S+$`)
	typePrefix      = regexp.MustCompile(`(?i)^type `)
	modelPrefix     = regexp.MustCompile(`(?i)^model `)
	staticListLine  = regexp.MustCompile(`^(?:undo )?wlan (?:client-security )?static-(black|white)list\b`)
)

func (s *configurationScope) settings() wireless.Record {
	if s.kind == "ap_groups" || s.kind == "service_templates" {
		return recordvalue.Map(s.record["configuration"])
	}
	return s.record
}
func (s *configurationScope) descend(kind string, keys []string, defaults wireless.Record) *configurationScope {
	parent := s.record
	path := append([]any{}, s.path...)
	for _, key := range keys[:len(keys)-1] {
		parent = ensureMap(parent, key)
		path = append(path, key)
	}
	key := keys[len(keys)-1]
	child, ok := parent[key].(wireless.Record)
	if !ok {
		child = defaults
		if child == nil {
			child = wireless.Record{}
		}
		parent[key] = child
	}
	path = append(path, key)
	return &configurationScope{kind, child, path}
}

type configAssignment struct {
	values     wireless.Record
	block      *sourceBlock
	conflicted bool
}
type configurationParser struct {
	source      *ConfigurationSource
	data        wireless.Record
	issues      []wireless.Record
	names       map[string]map[string]bool
	assignments map[uintptr]map[string]*configAssignment
	bindings    map[uintptr]map[string]wireless.Record
}

func newConfigurationParser(s *ConfigurationSource) *configurationParser {
	return &configurationParser{source: s, data: wireless.Record{"access_points": wireless.Record{}, "ap_groups": []wireless.Record{}, "service_templates": []wireless.Record{}, "global_settings": wireless.Record{}, "blacklist": []wireless.Record{}, "whitelist": []wireless.Record{}}, issues: append([]wireless.Record{}, s.issues...), names: map[string]map[string]bool{}, assignments: map[uintptr]map[string]*configAssignment{}, bindings: map[uintptr]map[string]wireless.Record{}}
}
func parseConfiguration(text string, complete bool) parser.Result {
	s, _ := ParseConfigurationSource(text, complete)
	o := s.Observation()
	return parser.Result{Status: o.Status(), Records: o.Records(), Issues: o.Issues()}
}
func (p *configurationParser) parse(complete bool) parser.Result {
	recognized := false
	for _, line := range p.source.lines {
		if line.text == "return" {
			recognized = true
		}
	}
	for _, block := range p.source.roots {
		if p.parseBlock(block, nil) {
			recognized = true
		}
	}
	if !complete {
		p.issues = append(p.issues, wireless.Record{"code": "missing_completeness_evidence"})
	}
	if !recognized {
		p.issues = append(p.issues, wireless.Record{"code": "unrecognized_configuration"})
	}
	status := wireless.Complete
	records := []wireless.Record{}
	if recognized {
		records = append(records, p.data)
	}
	if len(p.issues) > 0 {
		if recognized {
			status = wireless.Partial
		} else {
			status = wireless.Failed
		}
	}
	return parser.Result{Status: status, Records: records, Issues: p.issues}
}

var rootEntity = regexp.MustCompile(`(?i)^wlan (ap-group|service-template|ap) (\S+)(?: (.+))?$`)
var rootGlobal = regexp.MustCompile(`(?i)^wlan (global-configuration|virtual-ap-group(?: \S+)?|ap-license-group|vip-client-group)$`)
var rootWireless = regexp.MustCompile(`(?i)^(?:undo )?(?:wlan|dot1x|mac-authentication)\b`)
var rootWLAN = regexp.MustCompile(`(?i)^(?:undo )?wlan\b`)
var hostnamePattern = regexp.MustCompile(`^sysname (\S+)$`)
var nestedModel = regexp.MustCompile(`^ap-model (.+)$`)
var nestedRadio = regexp.MustCompile(`(?i)^radio (\d+|2\.4g|5g|6g)$`)
var nestedEthernet = regexp.MustCompile(`(?i)^(?:gigabitethernet|smartrate-ethernet|ethernet|ten-gigabitethernet) \d+$`)

func (p *configurationParser) globalScope(kind string, identity *string) *configurationScope {
	record := recordvalue.Map(p.data["global_settings"])
	path := []any{"global_settings"}
	if kind != "global" && kind != "global_configuration" {
		record = ensureMap(record, kind)
		path = append(path, kind)
	}
	if kind == "virtual_ap_group" {
		key := ""
		var id any
		if identity != nil {
			key = *identity
			id = *identity
		}
		record = ensureMap(record, key)
		path = append(path, id)
	}
	return &configurationScope{kind, record, path}
}

//nolint:nilnil // a line that opens no scope yields no scope
func (p *configurationParser) rootScope(line string) (*configurationScope, error) {
	if m := rootEntity.FindStringSubmatch(line); m != nil {
		kind := map[string]string{"ap-group": "ap_groups", "service-template": "service_templates", "ap": "access_points"}[strings.ToLower(m[1])]
		name, suffix := m[2], m[3]
		if kind == "ap_groups" && suffix != "" && !groupTypeSuffix.MatchString(suffix) {
			return nil, errors.New("invalid AP group type")
		}
		if kind == "service_templates" && suffix != "" {
			return nil, errors.New("invalid service template")
		}
		if kind == "access_points" && !apModelSuffix.MatchString(suffix) {
			return nil, errors.New("missing AP model")
		}
		if p.names[kind] == nil {
			p.names[kind] = map[string]bool{}
		}
		if p.names[kind][name] {
			return nil, errors.New("duplicate wireless configuration identity")
		}
		p.names[kind][name] = true
		var record wireless.Record
		var identity any
		switch kind {
		case "ap_groups":
			settings := wireless.Record{}
			if suffix != "" {
				settings["type"] = typePrefix.ReplaceAllString(suffix, "")
			}
			record = wireless.Record{"name": name, "ap_names": []any{}, "serial_ids": []any{}, "vlan_ids": []any{}, "model_profiles": wireless.Record{}, "configuration": settings}
		case "service_templates":
			record = wireless.Record{"name": name, "ssid": nil, "vlan_id": nil, "enabled": nil, "configuration": wireless.Record{"akm_modes": []any{}, "cipher_suites": []any{}, "security_ies": []any{}}}
		case "access_points":
			record = wireless.Record{"name": name, "model": modelPrefix.ReplaceAllString(suffix, ""), "radios": wireless.Record{}}
		}
		if kind == "access_points" {
			recordvalue.Map(p.data[kind])[name] = record
			identity = name
		} else {
			rows, _ := p.data[kind].([]wireless.Record)
			identity = len(rows)
			p.data[kind] = append(rows, record)
		}
		return &configurationScope{kind, record, []any{kind, identity}}, nil
	}
	if m := rootGlobal.FindStringSubmatch(line); m != nil {
		words := parsetext.SplitWords(m[1])
		key := strings.ReplaceAll(words[0], "-", "_")
		var identity *string
		if len(words) > 1 {
			identity = &words[1]
		}
		return p.globalScope(key, identity), nil
	}
	return nil, nil
}

//nolint:nilnil // a line that opens no scope yields no scope
func (p *configurationParser) nestedScope(line string, owner *configurationScope) (*configurationScope, error) {
	if m := nestedModel.FindStringSubmatch(line); m != nil {
		if owner.kind != "ap_groups" {
			return nil, errors.New("AP model outside group")
		}
		return owner.descend("model", []string{"model_profiles", m[1]}, wireless.Record{"radios": wireless.Record{}}), nil
	}
	if m := nestedRadio.FindStringSubmatch(line); m != nil {
		switch owner.kind {
		case "access_points", "ap_groups", "model", "global_configuration", "virtual_ap_group":
		default:
			return nil, errors.New("invalid Radio owner")
		}
		id := strings.ToLower(m[1])
		if owner.kind == "ap_groups" && parsetext.IsDecimal(id) {
			return nil, errors.New("group radio requires a model")
		}
		collection := "radios"
		if strings.HasSuffix(id, "g") {
			collection = "band_defaults"
		}
		keys := []string{collection, id}
		if owner.kind == "ap_groups" {
			keys = append([]string{"configuration"}, keys...)
		}
		return owner.descend("radio", keys, wireless.Record{"service_templates": []wireless.Record{}}), nil
	}
	if nestedEthernet.MatchString(line) {
		if owner.kind != "access_points" && owner.kind != "model" {
			return nil, errors.New("invalid Ethernet owner")
		}
		return owner.descend("ethernet", []string{"ethernet_ports", line}, wireless.Record{}), nil
	}
	return nil, nil
}
func (p *configurationParser) parseBlock(block *sourceBlock, owner *configurationScope) bool {
	recognized, err := p.statement(block, owner)
	if err != nil {
		p.source.states[block.index] = "invalid"
		p.issues = append(p.issues, wireless.Record{"code": "invalid_configuration_line", "line": block.index + 1})
		for _, child := range block.children {
			p.unsupported(child)
		}
		return false
	}
	if p.source.states[block.index] == "pending" {
		p.source.states[block.index] = "parsed"
	}
	if p.source.states[block.index] == "parsed" {
		for _, child := range block.children {
			if p.source.states[child.index] == "pending" {
				p.unsupported(child)
			}
		}
	}
	return recognized
}
func (p *configurationParser) statement(block *sourceBlock, owner *configurationScope) (bool, error) {
	line := p.source.lines[block.index].text
	var selected *configurationScope
	var err error
	if owner != nil {
		selected, err = p.nestedScope(line, owner)
	} else {
		selected, err = p.rootScope(line)
	}
	if err != nil {
		return false, err
	}
	if selected != nil {
		p.parseScope(block, selected)
		return true, nil
	}
	if owner == nil {
		if m := hostnamePattern.FindStringSubmatch(line); m != nil {
			settings := recordvalue.Map(p.data["global_settings"])
			p.assign(settings, "hostname", m[1], block)
			if value, ok := settings["hostname"]; ok {
				p.source.capture(block, value, []any{"global_settings", "hostname"})
			}
			return true, nil
		}
	}
	if owner != nil {
		return p.applyBlock(block, owner)
	}
	if rootWireless.MatchString(line) {
		_, err := p.applyBlock(block, p.globalScope("global", nil))
		return true, err
	}
	p.ignore(block)
	return false, nil
}
func (p *configurationParser) unsupported(block *sourceBlock) {
	pending := []*sourceBlock{block}
	for len(pending) > 0 {
		b := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		p.source.states[b.index] = "unsupported"
		p.issues = append(p.issues, wireless.Record{"code": "unrecognized_wireless_configuration", "line": b.index + 1})
		for _, v := range slices.Backward(b.children) {
			pending = append(pending, v)
		}
	}
}
func (p *configurationParser) ignore(block *sourceBlock) {
	pending := []*sourceBlock{block}
	for len(pending) > 0 {
		b := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if rootWLAN.MatchString(p.source.lines[b.index].text) {
			p.unsupported(b)
		} else {
			p.source.states[b.index] = "ignored"
			for _, v := range slices.Backward(b.children) {
				pending = append(pending, v)
			}
		}
	}
}
func (p *configurationParser) parseScope(block *sourceBlock, owner *configurationScope) {
	previous := p.assignments
	p.assignments = map[uintptr]map[string]*configAssignment{}
	for _, child := range block.children {
		p.parseBlock(child, owner)
	}
	if owner.kind == "service_templates" {
		conflicted := false
		for _, fields := range p.assignments {
			for _, entry := range fields {
				if entry.conflicted {
					conflicted = true
				}
			}
		}
		var authentication any
		if !conflicted {
			settings := owner.settings()
			if mode := settings["client_security_authentication_mode"]; mode != nil {
				authentication = mode
				if mode == "mac" {
					authentication = "mab"
				}
			} else if arrayIncludes(settings["akm_modes"], "dot1x") {
				authentication = "dot1x"
			} else if arrayIncludes(settings["akm_modes"], "psk") || settings["has_preshared_key"] == true {
				authentication = "psk"
			} else if _, ok := recordvalue.Map(settings["portal"])["enable_method"]; ok {
				authentication = "portal"
			}
		}
		owner.record["authentication_type"] = authentication
	}
	p.source.capture(block, owner.record, owner.path)
	p.assignments = previous
}
func arrayIncludes(v any, s string) bool {
	switch a := v.(type) {
	case []any:
		return slices.Contains(a, any(s))
	case []string:
		return slices.Contains(a, s)
	}
	return false
}
func (p *configurationParser) assign(record wireless.Record, key string, value any, block *sourceBlock) {
	p.assignMany(record, key, wireless.Record{key: value}, block)
}
func (p *configurationParser) assignMany(record wireless.Record, identity string, values wireless.Record, block *sourceBlock) {
	ptr := reflect.ValueOf(record).Pointer()
	fields := p.assignments[ptr]
	if fields == nil {
		fields = map[string]*configAssignment{}
		p.assignments[ptr] = fields
	}
	previous := fields[identity]
	if previous != nil && (previous.conflicted || !reflect.DeepEqual(previous.values, values)) {
		for k := range previous.values {
			delete(record, k)
		}
		for k := range values {
			delete(record, k)
		}
		p.source.states[previous.block.index] = "invalid"
		p.source.states[block.index] = "invalid"
		p.issues = append(p.issues, wireless.Record{"code": "conflicting_configuration_value", "line": block.index + 1, "first_line": previous.block.index + 1})
		previous.conflicted = true
	} else {
		if previous == nil {
			fields[identity] = &configAssignment{values, block, false}
		}
		maps.Copy(record, values)
	}
}
func (p *configurationParser) applyBlock(block *sourceBlock, scope *configurationScope) (bool, error) {
	ok, err := p.applySetting(scope, block)
	if err != nil {
		return false, err
	}
	if !ok {
		p.unsupported(block)
		return false, nil
	}
	if scope.kind == "global" {
		line := p.source.lines[block.index].text
		if staticListLine.MatchString(line) {
			key := "whitelist"
			if strings.Contains(line, "static-blacklist") {
				key = "blacklist"
			}
			rows, _ := p.data[key].([]wireless.Record)
			p.source.capture(block, rows[len(rows)-1], []any{key, len(rows) - 1})
		} else {
			p.source.capture(block, scope.record, scope.path)
		}
	}
	return true, nil
}
func appendValue(record wireless.Record, key string, value any) {
	a, _ := record[key].([]any)
	record[key] = append(a, value)
}
func (p *configurationParser) bindTemplate(record wireless.Record, name string, vlan any) error {
	ptr := reflect.ValueOf(record).Pointer()
	bindings := p.bindings[ptr]
	if bindings == nil {
		bindings = map[string]wireless.Record{}
		p.bindings[ptr] = bindings
	}
	binding := wireless.Record{"name": name, "vlan_id": vlan}
	if previous, ok := bindings[name]; ok {
		if !reflect.DeepEqual(previous, binding) {
			return errors.New("conflicting template binding")
		}
		return nil
	}
	bindings[name] = binding
	templates, _ := record["service_templates"].([]wireless.Record)
	record["service_templates"] = append(templates, binding)
	return nil
}
