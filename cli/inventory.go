package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	ncconfig "github.com/gatework/go-net-connector/config"
	"github.com/gatework/go-net-connector/fleet"
	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/netdisco"
	"github.com/gatework/go-net-connector/vendors/dialect"
	wireless "github.com/gatework/go-wireless"
)

func inventoryEnvironment(env map[string]string, higher wireless.Record) (wireless.Record, error) {
	result := wireless.Record{}
	for section, fields := range inventoryFields {
		for name, f := range fields {
			v, ok := env[f.Key]
			if !ok {
				continue
			}
			h, _ := higher[section].(map[string]any)
			if has(h, name) {
				continue
			}
			decoded, e := decodeEnvironment(v, f.Kind)
			if e != nil {
				return nil, ErrOptions
			}
			m, _ := result[section].(map[string]any)
			if m == nil {
				m = wireless.Record{}
				result[section] = m
			}
			m[name] = decoded
		}
	}
	return result, nil
}
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
	proc, e := inventoryEnvironment(o.process, wireless.Record{"netdisco": o.inventoryOverrides})
	if e != nil {
		return e
	}
	file, e := inventoryEnvironment(o.fileEnvironment, merge(data, proc, wireless.Record{"netdisco": o.inventoryOverrides}))
	if e != nil {
		return e
	}
	data = merge(file, data, proc, wireless.Record{"netdisco": o.inventoryOverrides})
	for section, v := range data {
		m, ok := v.(map[string]any)
		if !ok {
			return ErrOptions
		}
		for k := range m {
			if _, ok := inventoryFields[section][k]; !ok {
				return ErrOptions
			}
		}
	}
	// Reuse connector's strict scalar/list/JSON validation without discovery.
	_, e = ncconfig.Load(ncconfig.Options{Name: "backup", CWD: o.discovery.CWD, Home: o.discovery.Home, Directories: []string{}, Environment: map[string]string{}, Defaults: wireless.Record{}, Overrides: data})
	if e != nil {
		return ErrOptions
	}
	if _, e = inventoryRules(data, o.registry); e != nil {
		return e
	}
	n, _ := data["netdisco"].(map[string]any)
	for _, key := range []string{"cache_directory", "db_sslrootcert"} {
		if value, ok := n[key]; ok && strings.ContainsRune(stringValue(value), 0) {
			return ErrOptions
		}
	}
	if v, ok := n["source"]; ok && !in(stringValue(v), "http", "postgres") {
		return ErrOptions
	}
	if v, ok := n["cache_mode"]; ok && !in(stringValue(v), "prefer", "refresh", "only") {
		return ErrOptions
	}
	if v, ok := n["cache_enabled"]; ok && !boolValue(v) && n["cache_mode"] == "only" {
		return ErrOptions
	}
	if v, ok := n["url"]; ok {
		u, e := url.Parse(stringValue(v))
		if e != nil || u.Host == "" || !in(u.Scheme, "http", "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.IndexFunc(stringValue(v), unicode.IsControl) >= 0 {
			return ErrOptions
		}
		if !boolValue(n["allow_insecure_http"]) && u.Scheme != "https" {
			return ErrOptions
		}
	}
	if n["source"] == "postgres" {
		if strings.TrimSpace(stringValue(n["query"])) == "" || strings.ContainsRune(stringValue(n["query"]), 0) {
			return ErrOptions
		}
		if v, ok := n["db_port"]; ok && v.(int) > 65535 {
			return ErrOptions
		}
	}
	if v, ok := n["db_connect_timeout"]; ok && v.(int) > 2147483647 {
		return ErrOptions
	}
	if v, ok := n["db_sslmode"]; ok && !in(stringValue(v), "disable", "allow", "prefer", "require", "verify-ca", "verify-full") {
		return ErrOptions
	}
	if a, ok := n["query_params"].([]any); ok {
		for _, v := range a {
			switch v.(type) {
			case string:
				if strings.ContainsRune(v.(string), 0) {
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
			rule := fleet.Mapping{Vendor: stringValue(m["vendor"]), Connector: stringValue(m["connector"])}
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
			NameMatch: stringValue(i["name_match"]), ModelMatch: stringValue(i["model_match"]),
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
		normalized, normalizeErr := wireless.ParseIP(&host)
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
		if !in(p, "h3c", "h3c_wireless", "huawei") {
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

// SourceFactory is injected at the network-source boundary; cache and inventory
// rule behavior remain exercised when tests replace HTTP/Postgres with fixtures.
type SourceFactory func(*Options) (netdisco.Source, string, error)

func defaultSource(o *Options) (netdisco.Source, string, error) {
	data := o.inventoryData()
	n, _ := data["netdisco"].(map[string]any)
	limits, e := inventoryLimits(n)
	if e != nil {
		return nil, "", e
	}
	source := stringValue(n["source"])
	if source == "" {
		source = "http"
	}
	only := n["cache_mode"] == "only"
	var result netdisco.Source
	identity := wireless.Record{"source": source}
	if source == "http" {
		rawURL := stringValue(n["url"])
		if rawURL == "" {
			return nil, "", ErrOptions
		}
		username, password, key := secret(o.Environment, "ND_USERNAME"), secret(o.Environment, "ND_PASSWORD"), secret(o.Environment, "ND_API_KEY")
		if key == "" && username == "" {
			return nil, "", ErrOptions
		}
		u, err := url.Parse(rawURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !in(u.Scheme, "https", "http") {
			return nil, "", ErrOptions
		}
		u.Host = strings.ToLower(u.Host)
		u.Path = strings.TrimRight(u.Path, "/")
		identity["url"] = u.String()
		identity["username"] = username
		identity["authentication"] = "basic"
		namespace := stringValue(n["cache_namespace"])
		identity["namespace"] = namespace
		if key != "" {
			identity["authentication"] = "api_key"
			cacheEnabled := !has(n, "cache_enabled") || boolValue(n["cache_enabled"])
			if cacheEnabled && strings.TrimSpace(username) == "" && strings.TrimSpace(namespace) == "" {
				return nil, "", &netdisco.InventoryError{Code: "cache_namespace_required"}
			}
		}
		pageSize := limits.PageSize
		if pageSize == 0 {
			pageSize = 500
		}
		identity["query"] = wireless.Record{
			"endpoint": "api/v1/search/device", "fields": "ip,name,dns,vendor,os,model,os_ver,serial",
			"page_size": pageSize, "fallback_q": "%", "fallback_seeallcolumns": true,
		}
		if !only {
			result, e = netdisco.NewHTTP(netdisco.HTTPOptions{URL: rawURL, Username: username, Password: password, APIKey: key, RequireHTTPS: !boolValue(n["allow_insecure_http"]), Limits: limits})
		}
	} else {
		db := map[string]string{}
		for key, env := range map[string]string{"host": "ND_DB_HOST", "dbname": "ND_DB_NAME", "user": "ND_DB_USER", "password": "ND_DB_PASS"} {
			if v := secret(o.Environment, env); v != "" {
				db[key] = v
			}
		}
		if db["host"] == "" || db["dbname"] == "" || db["user"] == "" {
			return nil, "", ErrOptions
		}
		for field, key := range map[string]string{"db_port": "port", "db_sslmode": "sslmode", "db_sslrootcert": "sslrootcert", "db_connect_timeout": "connect_timeout"} {
			if v, ok := n[field]; ok {
				db[key] = fmt.Sprint(v)
			}
		}

		parts := []string{}
		public := wireless.Record{}
		for k, v := range db {
			parts = append(parts, k+"='"+strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `'`, `\'`)+"'")
			if k != "password" {
				public[k] = v
			}
		}
		identity["connection"] = public
		identity["query"] = n["query"]
		identity["query_params"] = n["query_params"]
		if !only {
			if db["password"] == "" {
				return nil, "", ErrOptions
			}
			params, _ := n["query_params"].([]any)
			result, e = netdisco.NewPostgres(netdisco.PostgresOptions{Connection: strings.Join(parts, " "), Query: stringValue(n["query"]), Parameters: params, Limits: limits})
		}
	}
	if e != nil {
		return nil, "", ErrOptions
	}
	b, e := json.Marshal(identity)
	if e != nil {
		return nil, "", ErrOptions
	}
	return result, string(b), nil
}
func (o *Options) inventory(ctx context.Context, factory SourceFactory) ([]wireless.Record, int, error) {
	rows, _ := o.Values["devices"].([]any)
	if o.Dynamic() {
		if factory == nil {
			factory = defaultSource
		}
		source, identity, e := factory(o)
		if e != nil {
			return nil, 0, e
		}
		data := o.inventoryData()
		n, _ := data["netdisco"].(map[string]any)
		cacheEnabled := true
		if v, ok := n["cache_enabled"]; ok {
			cacheEnabled = v.(bool)
		}
		var native []inventory.Device
		if cacheEnabled {
			directory := stringValue(n["cache_directory"])
			if directory == "" {
				directory = filepath.Join(o.discovery.CWD, ".cache/net-connector/inventory")
			}
			ttl := 72 * time.Hour
			if v, ok := n["cache_ttl"]; ok {
				f, _ := number(v)
				if f > float64(int64(^uint64(0)>>1))/1e9 {
					return nil, 0, ErrOptions
				}
				ttl = time.Duration(f * 1e9)
			}
			limits, _ := inventoryLimits(n)
			cache, e := netdisco.NewCache(source, netdisco.CacheOptions{Directory: directory, Identity: identity, TTL: &ttl, Limits: limits})
			if e != nil {
				return nil, 0, e
			}
			mode := stringValue(n["cache_mode"])
			if mode == "" {
				mode = "prefer"
			}
			r, e := cache.Fetch(ctx, mode)
			if e != nil {
				return nil, 0, e
			}
			native = r.Rows
		} else {
			if source == nil {
				return nil, 0, ErrOptions
			}
			native, e = source.Devices(ctx)
			if e != nil {
				return nil, 0, e
			}
		}
		rules, e := inventoryRules(data, o.registry)
		if e != nil {
			return nil, 0, e
		}
		rows = []any{}
		for _, d := range native {
			record := rules.Record(d)
			if record.Issue != "" || !in(record.Vendor, "h3c_wireless", "huawei") {
				continue
			}
			var model any
			if record.Model != "" {
				model = record.Model
			}
			name := record.Name
			if name == "" {
				name = record.Host
			}
			rows = append(rows, wireless.Record{"host": record.Host, "name": name, "model": model, "platform": record.Vendor})
		}
	}
	targets, e := NormalizeDevices(rows, stringValue(o.Values["platform"]))
	if e != nil {
		return nil, 0, e
	}
	total := len(targets)
	lists := map[string][]string{}
	for _, k := range []string{"hosts", "exclude_hosts", "names", "exclude_names"} {
		lists[k], _ = stringList(o.Values[k])
	}
	selection, e := inventory.NewSelection(inventory.SelectionOptions{IncludeHosts: lists["hosts"], ExcludeHosts: lists["exclude_hosts"], IncludeNames: lists["names"], ExcludeNames: lists["exclude_names"]})
	if e != nil {
		return nil, 0, ErrOptions
	}
	selected := []wireless.Record{}
	for _, d := range targets {
		if selection.Evaluate(inventory.Device{IP: stringValue(d["host"]), Name: stringValue(d["name"])}).Selected() {
			selected = append(selected, d)
		}
	}
	return selected, total, nil
}
func secret(env map[string]string, key string) string {
	v := env[key]
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return v
}

// LoadOptions freezes configuration and credentials without reading inventory,
// writing reports, opening sessions, or prompting. Missing config is an error.
func LoadOptions(argv []string, env map[string]string, d Discovery) (*Options, error) {
	o, e := parseOptions(argv, d)
	if e != nil {
		return nil, e
	}
	if e = o.loadEnvironment(env); e != nil {
		return nil, e
	}
	data, specific, e := o.loadFiles()
	if e != nil {
		return nil, e
	}
	if !o.applicable {
		return nil, ErrOptions
	}
	if e = o.load(data, specific); e != nil {
		return nil, e
	}
	return o, nil
}
