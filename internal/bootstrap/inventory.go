package bootstrap

import (
	"context"
	"maps"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gatework/go-wireless/vendors"

	ncconfig "github.com/gatework/go-net-connector/config"
	"github.com/gatework/go-net-connector/fleet"
	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/netdisco"
	"github.com/gatework/go-net-connector/vendors/dialect"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

func (o *Options) loadInventoryPolicy(ctx context.Context) error {
	data, e := o.inventorySource(ctx)
	if e != nil {
		return e
	}
	// Effective higher layers mask invalid public dotenv values before decoding.
	proc, e := ncconfig.InventoryEnvironment(o.process, wireless.Record{"netdisco": o.inventoryOverrides})
	if e != nil {
		return e
	}
	file, e := ncconfig.InventoryEnvironment(o.fileEnvironment, merge(data, proc, wireless.Record{"netdisco": o.inventoryOverrides}))
	if e != nil {
		return e
	}
	data = merge(file, data, proc, wireless.Record{"netdisco": o.inventoryOverrides})
	// Reuse connector's strict scalar/list/JSON validation without discovery.
	if e = ncconfig.ValidateInventory(data); e != nil {
		return ErrOptions
	}
	if _, e = inventoryRules(data, o.registry); e != nil {
		return e
	}
	n, _ := data["netdisco"].(map[string]any)
	if e = validateNetdiscoSettings(n); e != nil {
		return e
	}
	if _, e = inventoryLimits(n); e != nil {
		return e
	}
	if has(o.Values, "netdisco_config") {
		o.Values["inventory_configuration"] = data
	} else {
		maps.Copy(o.Values, data)
	}
	return nil
}

// inventorySource returns the netdisco and inventory settings from the
// configured file, or the inline options when no file is configured.
func (o *Options) inventorySource(ctx context.Context) (wireless.Record, error) {
	data := wireless.Record{}
	path, ok := o.Values["netdisco_config"].(string)
	if !ok {
		for _, k := range []string{"netdisco", "inventory"} {
			if v, ok := o.Values[k]; ok {
				data[k] = v
			}
		}
		return data, nil
	}
	b, _, e := readConfigurationFile(ctx, path, false)
	if e != nil {
		return nil, e
	}
	all, e := ncconfig.ParseYAML(b)
	if e != nil {
		return nil, ErrOptions
	}
	if filepath.Base(path) == "net-connector.yml" {
		if all, e = o.sharedConfiguration(ctx, all, filepath.Dir(path)); e != nil {
			return nil, e
		}
	} else if has(all, "common") || has(all, "wireless") {
		return nil, ErrLegacyConfiguration
	}
	for k := range all {
		if !in(k, "netdisco", "inventory", "topology", "backup", "ssh", "tftp") {
			return nil, ErrOptions
		}
	}
	for _, k := range []string{"netdisco", "inventory"} {
		if v, ok := all[k]; ok {
			data[k] = v
		}
	}
	resolvePaths(data, filepath.Dir(path), o.discovery.Home, "")
	return data, nil
}

// validateNetdiscoSettings checks values the connector would otherwise reject
// late or accept unsafely: source and cache modes, paths, the HTTP URL, the
// Postgres query and connection settings and the query parameters.
func validateNetdiscoSettings(n map[string]any) error {
	for _, key := range []string{"cache_directory", "db_sslrootcert"} {
		if value, ok := n[key]; ok && strings.ContainsRune(StringValue(value), 0) {
			return ErrOptions
		}
	}
	if v, ok := n["source"]; ok && !in(StringValue(v), "http", "postgres") {
		return ErrOptions
	}
	if v, ok := n["cache_mode"]; ok && !in(StringValue(v), "prefer", "refresh", "only") {
		return ErrOptions
	}
	if v, ok := n["cache_enabled"]; ok && !boolValue(v) && n["cache_mode"] == "only" {
		return ErrOptions
	}
	if v, ok := n["url"]; ok {
		if e := validateNetdiscoURL(StringValue(v), boolValue(n["allow_insecure_http"])); e != nil {
			return e
		}
	}
	if n["source"] == "postgres" {
		if strings.TrimSpace(StringValue(n["query"])) == "" || strings.ContainsRune(StringValue(n["query"]), 0) {
			return ErrOptions
		}
		if v, ok := n["db_port"].(int); ok && v > 65535 {
			return ErrOptions
		}
	}
	if v, ok := n["db_connect_timeout"].(int); ok && v > 2147483647 {
		return ErrOptions
	}
	if v, ok := n["db_sslmode"]; ok && !in(StringValue(v), "disable", "allow", "prefer", "require", "verify-ca", "verify-full") {
		return ErrOptions
	}
	if a, ok := n["query_params"].([]any); ok && !scalarParams(a) {
		return ErrOptions
	}
	return nil
}

// validateNetdiscoURL requires an absolute http(s) URL without credentials,
// query or fragment, and https unless insecure HTTP was allowed.
func validateNetdiscoURL(raw string, allowInsecure bool) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || !in(u.Scheme, "http", "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return ErrOptions
	}
	if !allowInsecure && u.Scheme != "https" {
		return ErrOptions
	}
	return nil
}

// scalarParams reports whether every query parameter is a scalar without NUL.
func scalarParams(params []any) bool {
	for _, v := range params {
		switch v := v.(type) {
		case string:
			if strings.ContainsRune(v, 0) {
				return false
			}
		case nil, bool, int, float64:
		default:
			return false
		}
	}
	return true
}

// inventoryRules builds the fleet rules from the inventory settings.
func inventoryRules(data wireless.Record, registry dialect.Registry) (*fleet.Rules, error) {
	i, _ := data["inventory"].(map[string]any)
	lists, e := selectionLists(i)
	if e != nil {
		return nil, e
	}
	mappings, e := deviceMappings(i)
	if e != nil {
		return nil, e
	}
	overrides, e := inventoryOverrides(i)
	if e != nil {
		return nil, e
	}
	rules, e := fleet.NewRules(fleet.RuleOptions{
		Selection: inventory.SelectionOptions{
			IncludeHosts: lists["include_hosts"], ExcludeHosts: lists["exclude_hosts"],
			IncludeNames: lists["include_names"], ExcludeNames: lists["exclude_names"],
			IncludeModels: lists["include_models"], ExcludeModels: lists["exclude_models"],
			IncludeVendors: lists["include_vendors"], ExcludeVendors: lists["exclude_vendors"],
			NameMatch: StringValue(i["name_match"]), ModelMatch: StringValue(i["model_match"]),
		},
		Mappings: mappings, VendorOverrides: overrides["vendor_overrides"], HostOverrides: overrides["host_overrides"],
	}, registry)
	if e != nil {
		return nil, ErrOptions
	}
	return rules, nil
}

func selectionLists(i map[string]any) (map[string][]string, error) {
	lists := map[string][]string{}
	for _, k := range []string{"include_hosts", "exclude_hosts", "include_names", "exclude_names", "include_models", "exclude_models", "include_vendors", "exclude_vendors"} {
		if v, ok := i[k]; ok {
			a, e := stringList(v)
			if e != nil {
				return nil, e
			}
			lists[k] = a
		}
	}
	return lists, nil
}

// deviceMappings decodes device_rules: a vendor and connector are required,
// os and model_prefix optional.
func deviceMappings(i map[string]any) ([]fleet.Mapping, error) {
	mappings := []fleet.Mapping{}
	v, ok := i["device_rules"]
	if !ok {
		return mappings, nil
	}
	a, ok := v.([]any)
	if !ok {
		return nil, ErrOptions
	}
	for _, item := range a {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, ErrOptions
		}
		rule, e := deviceMapping(m)
		if e != nil {
			return nil, e
		}
		mappings = append(mappings, rule)
	}
	return mappings, nil
}

func deviceMapping(m map[string]any) (fleet.Mapping, error) {
	for k := range m {
		if !in(k, "vendor", "connector", "os", "model_prefix") {
			return fleet.Mapping{}, ErrOptions
		}
	}
	rule := fleet.Mapping{Vendor: StringValue(m["vendor"]), Connector: StringValue(m["connector"])}
	if strings.TrimSpace(rule.Vendor) == "" || strings.TrimSpace(rule.Connector) == "" {
		return fleet.Mapping{}, ErrOptions
	}
	for _, k := range []string{"os", "model_prefix"} {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		value, ok := v.(string)
		if !ok {
			return fleet.Mapping{}, ErrOptions
		}
		if k == "os" {
			rule.OS = &value
		} else {
			rule.ModelPrefix = &value
		}
	}
	return rule, nil
}

// inventoryOverrides decodes vendor_overrides and host_overrides as string maps.
func inventoryOverrides(i map[string]any) (map[string]map[string]string, error) {
	overrides := map[string]map[string]string{}
	for _, k := range []string{"vendor_overrides", "host_overrides"} {
		values := map[string]string{}
		if v, ok := i[k]; ok {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, ErrOptions
			}
			for key, v := range m {
				s, ok := v.(string)
				if !ok {
					return nil, ErrOptions
				}
				values[key] = s
			}
		}
		overrides[k] = values
	}
	return overrides, nil
}

// NormalizeDevices validates static device rows and normalizes their
// addresses; hosts must be unique.
func NormalizeDevices(rows []any, platform string) ([]wireless.Record, error) {
	result := []wireless.Record{}
	seen := map[string]bool{}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return nil, ErrOptions
		}
		device, e := normalizeDevice(m, platform)
		if e != nil {
			return nil, e
		}
		host, _ := device["host"].(string)
		if seen[host] {
			return nil, ErrOptions
		}
		seen[host] = true
		result = append(result, device)
	}
	return result, nil
}

// normalizeDevice validates one static device row.
func normalizeDevice(m map[string]any, platform string) (wireless.Record, error) {
	for k := range m {
		if !in(k, "host", "name", "model", "platform") {
			return nil, ErrOptions
		}
	}
	host, ok := m["host"].(string)
	if !ok {
		return nil, ErrOptions
	}
	if a, e := netip.ParseAddr(host); e != nil || a.Zone() != "" {
		return nil, ErrOptions
	}
	normalized, normalizeErr := parser.ParseIP(&host)
	if normalizeErr != nil || normalized == nil {
		return nil, ErrOptions
	}
	host = *normalized
	p := platform
	if v, ok := m["platform"]; ok && v != nil {
		if p, ok = v.(string); !ok {
			return nil, ErrOptions
		}
	}
	if !in(p, vendors.NewBuiltinRegistry().Platforms()...) {
		return nil, ErrOptions
	}
	name := host
	var model any
	for _, k := range []string{"name", "model"} {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, ErrOptions
		}
		if k == "name" {
			name = s
		} else {
			model = s
		}
	}
	return wireless.Record{"host": host, "name": name, "model": model, "platform": p}, nil
}
func (o *Options) inventoryData() wireless.Record {
	if v, ok := o.Values["inventory_configuration"].(map[string]any); ok {
		return v
	}
	return o.Values
}
func inventoryLimits(n wireless.Record) (netdisco.Limits, error) {
	l := netdisco.Limits{PageSize: 500, MaxPages: 10000, MaxDevices: 100000, MaxResponseBytes: 16 << 20, MaxInventoryBytes: 128 << 20, Timeout: 300 * time.Second}
	for k, p := range map[string]*int{"page_size": &l.PageSize, "max_pages": &l.MaxPages, "max_devices": &l.MaxDevices} {
		if v, ok := n[k]; ok {
			*p, _ = v.(int)
		}
	}
	for k, p := range map[string]*int64{"max_response_bytes": &l.MaxResponseBytes, "max_inventory_bytes": &l.MaxInventoryBytes} {
		if v, ok := n[k]; ok {
			n, _ := v.(int)
			*p = int64(n)
		}
	}
	if v, ok := n["inventory_timeout"]; ok {
		f, _ := number(v)
		if f <= 0 || f > float64(int64(^uint64(0)>>1))/1e9 {
			return l, ErrOptions
		}
		l.Timeout = time.Duration(f * 1e9)
	}
	return l, nil
}

func secret(env map[string]string, key string) string {
	v := env[key]
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return v
}
