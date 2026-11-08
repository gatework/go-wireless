package bootstrap

import (
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

func (o *Options) loadInventoryPolicy() error {
	data := wireless.Record{}
	if path, ok := o.Values["netdisco_config"].(string); ok {
		b, _, e := readFile(path, true, false)
		if e != nil {
			return e
		}
		all, e := ncconfig.ParseYAML(b)
		if e != nil {
			return ErrOptions
		}
		if filepath.Base(path) == "net-connector.yml" {
			all, e = o.sharedConfiguration(all, filepath.Dir(path))
			if e != nil {
				return e
			}
		} else if has(all, "common") || has(all, "wireless") {
			return ErrLegacyConfiguration
		}
		for k := range all {
			if !in(k, "netdisco", "inventory", "topology", "backup", "ssh", "tftp") {
				return ErrOptions
			}
		}
		for _, k := range []string{"netdisco", "inventory"} {
			if v, ok := all[k]; ok {
				data[k] = v
			}
		}
		resolvePaths(data, filepath.Dir(path), o.discovery.Home, "")
	} else {
		for _, k := range []string{"netdisco", "inventory"} {
			if v, ok := o.Values[k]; ok {
				data[k] = v
			}
		}
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
	e = ncconfig.ValidateInventory(data)
	if e != nil {
		return ErrOptions
	}
	if _, e = inventoryRules(data, o.registry); e != nil {
		return e
	}
	n, _ := data["netdisco"].(map[string]any)
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
		u, e := url.Parse(StringValue(v))
		if e != nil || u.Host == "" || !in(u.Scheme, "http", "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.IndexFunc(StringValue(v), unicode.IsControl) >= 0 {
			return ErrOptions
		}
		if !boolValue(n["allow_insecure_http"]) && u.Scheme != "https" {
			return ErrOptions
		}
	}
	if n["source"] == "postgres" {
		if strings.TrimSpace(StringValue(n["query"])) == "" || strings.ContainsRune(StringValue(n["query"]), 0) {
			return ErrOptions
		}
		if v, ok := n["db_port"]; ok && v.(int) > 65535 {
			return ErrOptions
		}
	}
	if v, ok := n["db_connect_timeout"]; ok && v.(int) > 2147483647 {
		return ErrOptions
	}
	if v, ok := n["db_sslmode"]; ok && !in(StringValue(v), "disable", "allow", "prefer", "require", "verify-ca", "verify-full") {
		return ErrOptions
	}
	if a, ok := n["query_params"].([]any); ok {
		for _, v := range a {
			switch v := v.(type) {
			case string:
				if strings.ContainsRune(v, 0) {
					return ErrOptions
				}
			case nil, bool, int, float64:
			default:
				return ErrOptions
			}
		}
	}
	if _, e = inventoryLimits(n); e != nil {
		return e
	}
	if has(o.Values, "netdisco_config") {
		o.Values["inventory_configuration"] = data
	} else {
		for k, v := range data {
			o.Values[k] = v
		}
	}
	return nil
}
func inventoryRules(data wireless.Record, registry dialect.Registry) (*fleet.Rules, error) {
	i, _ := data["inventory"].(map[string]any)
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
	mappings := []fleet.Mapping{}
	if v, ok := i["device_rules"]; ok {
		a, ok := v.([]any)
		if !ok {
			return nil, ErrOptions
		}
		for _, item := range a {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, ErrOptions
			}
			for k := range m {
				if !in(k, "vendor", "connector", "os", "model_prefix") {
					return nil, ErrOptions
				}
			}
			rule := fleet.Mapping{Vendor: StringValue(m["vendor"]), Connector: StringValue(m["connector"])}
			if strings.TrimSpace(rule.Vendor) == "" || strings.TrimSpace(rule.Connector) == "" {
				return nil, ErrOptions
			}
			for _, k := range []string{"os", "model_prefix"} {
				if v, ok := m[k]; ok && v != nil {
					value, ok := v.(string)
					if !ok {
						return nil, ErrOptions
					}
					if k == "os" {
						rule.OS = &value
					} else {
						rule.ModelPrefix = &value
					}
				}
			}
			mappings = append(mappings, rule)
		}
	}
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
func NormalizeDevices(rows []any, platform string) ([]wireless.Record, error) {
	result := []wireless.Record{}
	seen := map[string]bool{}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return nil, ErrOptions
		}
		for k := range m {
			if !in(k, "host", "name", "model", "platform") {
				return nil, ErrOptions
			}
		}
		host, ok := m["host"].(string)
		if !ok {
			return nil, ErrOptions
		}
		a, e := netip.ParseAddr(host)
		if e != nil || a.Zone() != "" {
			return nil, ErrOptions
		}
		normalized, normalizeErr := parser.ParseIP(&host)
		if normalizeErr != nil || normalized == nil {
			return nil, ErrOptions
		}
		host = *normalized
		if seen[host] {
			return nil, ErrOptions
		}
		seen[host] = true
		p := platform
		if v, ok := m["platform"]; ok && v != nil {
			p, ok = v.(string)
			if !ok {
				return nil, ErrOptions
			}
		}
		if !in(p, vendors.NewBuiltinRegistry().Platforms()...) {
			return nil, ErrOptions
		}
		name := host
		var model any
		for _, k := range []string{"name", "model"} {
			if v, ok := m[k]; ok && v != nil {
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
		}
		result = append(result, wireless.Record{"host": host, "name": name, "model": model, "platform": p})
	}
	return result, nil
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
			*p = v.(int)
		}
	}
	for k, p := range map[string]*int64{"max_response_bytes": &l.MaxResponseBytes, "max_inventory_bytes": &l.MaxInventoryBytes} {
		if v, ok := n[k]; ok {
			*p = int64(v.(int))
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
