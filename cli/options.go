package cli

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	ncconfig "github.com/gatework/go-net-connector/config"
	"github.com/gatework/go-net-connector/inventory"
	"github.com/gatework/go-net-connector/vendors"
	"github.com/gatework/go-net-connector/vendors/dialect"
	wireless "github.com/gatework/go-wireless"
	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
)

//go:embed templates/*
var templates embed.FS
var ErrOptions = errors.New("wireless: invalid public configuration or options")

// ErrLegacyConfiguration identifies the removed common/wireless shared layout.
var ErrLegacyConfiguration = fmt.Errorf("wireless: move common settings to the root of net-connector.yml and wireless settings to wireless.yml: %w", ErrOptions)

type Discovery struct {
	CWD, Home, Program string
	Directories        []string
}
type Flags struct {
	Help, HelpAll, Examples, Init, InventoryOnly, ShowConfig, JSON, Quiet, Scripts, Verbose bool
	Interactive                                                                             *bool
}
type Options struct {
	Values                                     wireless.Record
	Environment                                map[string]string
	Sources                                    wireless.Record
	Config, EnvFile                            string
	Flags                                      Flags
	discovery                                  Discovery
	process, fileEnvironment                   map[string]string
	overrides, inventoryOverrides              wireless.Record
	selectedConfig, selectedEnv, userDirectory string
	explicitConfig, explicitEnv                bool
	applicable                                 bool
	registry                                   dialect.Registry
}

func parseOptions(argv []string, d Discovery) (*Options, error) {
	o := &Options{discovery: d, overrides: wireless.Record{}, inventoryOverrides: wireless.Record{}, registry: vendors.NewBuiltinRegistry()}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		key, value, has := strings.Cut(arg, "=")
		take := func() (string, error) {
			if has {
				return value, nil
			}
			i++
			if i >= len(argv) {
				return "", ErrOptions
			}
			return argv[i], nil
		}
		boolean := func() error {
			if has {
				return ErrOptions
			}
			return nil
		}
		switch key {
		case "--config", "--env-file", "--parallel", "-p", "--directory", "--platform", "--dimensions", "--ap-name", "--host", "--hosts", "--exclude-hosts", "--name", "--exclude-name", "--cache-mode", "--source":
			v, e := take()
			if e != nil {
				return nil, e
			}
			switch key {
			case "--config":
				o.selectedConfig = v
				o.explicitConfig = true
			case "--env-file":
				o.selectedEnv = v
				o.explicitEnv = true
			case "--parallel", "-p":
				n, e := strconv.Atoi(v)
				if e != nil {
					return nil, ErrOptions
				}
				o.overrides["parallel"] = n
			case "--directory":
				o.overrides["directory"] = v
			case "--platform":
				o.overrides["platform"] = v
			case "--cache-mode":
				if e = o.cacheOverride(v); e != nil {
					return nil, e
				}
			case "--source":
				if v != "http" && v != "postgres" {
					return nil, ErrOptions
				}
				o.inventoryOverrides["source"] = v
			default:
				list, e := splitList(v)
				if e != nil {
					return nil, e
				}
				field := map[string]string{"--dimensions": "dimensions", "--ap-name": "ap_names", "--host": "hosts", "--hosts": "hosts", "--exclude-hosts": "exclude_hosts", "--name": "names", "--exclude-name": "exclude_names"}[key]
				if field != "dimensions" {
					old, _ := o.overrides[field].([]any)
					list = append(old, list...)
				}
				o.overrides[field] = list
			}
		default:
			if e := boolean(); e != nil {
				return nil, e
			}
			switch key {
			case "--init":
				o.Flags.Init = true
			case "--interactive", "--non-interactive":
				v := key == "--interactive"
				o.Flags.Interactive = &v
			case "--plan", "--inventory-only":
				o.Flags.InventoryOnly = true
			case "--cache-only":
				if e := o.cacheOverride("only"); e != nil {
					return nil, e
				}
			case "--refresh-inventory":
				if e := o.cacheOverride("refresh"); e != nil {
					return nil, e
				}
			case "--show-config":
				o.Flags.ShowConfig = true
			case "--json":
				o.Flags.JSON = true
			case "--quiet", "--no-progress":
				o.Flags.Quiet = true
			case "--scripts":
				o.Flags.Scripts = true
			case "--no-scripts":
				o.Flags.Scripts = false
			case "--verbose":
				o.Flags.Verbose = true
			case "-h", "--help":
				o.Flags.Help = true
			case "--help-all":
				o.Flags.HelpAll = true
			case "--examples":
				o.Flags.Examples = true
			default:
				return nil, ErrOptions
			}
		}
	}
	return o, nil
}
func (o *Options) cacheOverride(v string) error {
	if v != "prefer" && v != "only" && v != "refresh" {
		return ErrOptions
	}
	if old, ok := o.inventoryOverrides["cache_mode"]; ok && old != v {
		return ErrOptions
	}
	o.inventoryOverrides["cache_mode"] = v
	return nil
}
func splitList(v string) ([]any, error) {
	list := []any{}
	for _, v := range strings.Split(v, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, ErrOptions
		}
		list = append(list, v)
	}
	return list, nil
}
func copyMap(m wireless.Record) wireless.Record {
	out := wireless.Record{}
	for k, v := range m {
		switch x := v.(type) {
		case map[string]any:
			out[k] = copyMap(x)
		case []any:
			out[k] = append([]any{}, x...)
		default:
			out[k] = v
		}
	}
	return out
}
func merge(layers ...wireless.Record) wireless.Record {
	out := wireless.Record{}
	for _, m := range layers {
		for k, v := range m {
			a, aok := out[k].(map[string]any)
			b, bok := v.(map[string]any)
			if aok && bok {
				out[k] = merge(a, b)
			} else if bok {
				out[k] = copyMap(b)
			} else {
				out[k] = v
			}
		}
	}
	return out
}
func absolute(path, base, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}
func uniqueLast(paths []string, last string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range paths {
		if p != "" && p != last && !seen[p] {
			out = append(out, p)
			seen[p] = true
		}
	}
	if last != "" {
		out = append(out, last)
	}
	return out
}

var pathFields = map[string]bool{"directory": true, "netdisco_config": true, "ssh.known_hosts": true, "netdisco.cache_directory": true, "netdisco.db_sslrootcert": true}
var envPathFields = map[string]bool{"WIRELESS_CONFIG": true, "WIRELESS_DIRECTORY": true, "NC_KNOWN_HOSTS": true, "ND_CACHE_DIRECTORY": true, "ND_DB_SSLROOTCERT": true}

func resolvePaths(m wireless.Record, base, home, prefix string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if child, ok := v.(map[string]any); ok {
			resolvePaths(child, base, home, key)
		} else if pathFields[key] {
			if s, ok := v.(string); ok && s != "" && !(key == "netdisco.db_sslrootcert" && s == "system") {
				m[k] = absolute(s, base, home)
			}
		}
	}
}
func resolveEnvironment(m map[string]string, base, home string) {
	for k := range envPathFields {
		if v := m[k]; strings.TrimSpace(v) != "" && !(k == "ND_DB_SSLROOTCERT" && v == "system") {
			m[k] = absolute(v, base, home)
		}
	}
}
func readFile(path string, required, allowMissing bool) ([]byte, bool, error) {
	return readRegularFile(path, required, allowMissing, ncconfig.MaxBytes)
}
func readRegularFile(path string, required, allowMissing bool, limit int64) ([]byte, bool, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(e, os.ErrNotExist) && (!required || allowMissing) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, ErrOptions
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || limit > 0 && info.Size() > limit {
		return nil, false, ErrOptions
	}
	var reader io.Reader = f
	if limit > 0 {
		reader = io.LimitReader(f, limit+1)
	}
	b, e := io.ReadAll(reader)
	if e != nil || limit > 0 && int64(len(b)) > limit {
		return nil, false, ErrOptions
	}
	return b, true, nil
}
func (o *Options) loadEnvironment(env map[string]string) error {
	if o.explicitConfig && strings.TrimSpace(o.selectedConfig) == "" || o.explicitEnv && strings.TrimSpace(o.selectedEnv) == "" {
		return ErrOptions
	}
	d := &o.discovery
	actual, e := os.Getwd()
	if e != nil {
		return ErrOptions
	}
	if d.CWD == "" {
		d.CWD = actual
	}
	if d.Home == "" {
		d.Home, e = os.UserHomeDir()
		if e != nil {
			return ErrOptions
		}
	}
	d.Home = absolute(d.Home, actual, d.Home)
	d.CWD = absolute(d.CWD, actual, d.Home)
	o.process = map[string]string{}
	for k, v := range env {
		o.process[k] = v
	}
	resolveEnvironment(o.process, d.CWD, d.Home)
	user := o.process["XDG_CONFIG_HOME"]
	if user == "" {
		user = filepath.Join(d.Home, ".config")
	}
	o.userDirectory = filepath.Join(absolute(user, d.CWD, d.Home), "net-connector")
	if d.Directories == nil {
		program := d.Program
		if program == "" {
			program = os.Args[0]
		}
		d.Directories = []string{"/etc/net-connector", "/usr/local/etc/net-connector", o.userDirectory, filepath.Dir(absolute(program, d.CWD, d.Home)), d.CWD}
	}
	dirs := []string{}
	for _, p := range d.Directories {
		dirs = append(dirs, absolute(p, d.CWD, d.Home))
	}
	d.Directories = uniqueLast(dirs, "")
	if strings.TrimSpace(o.selectedEnv) != "" {
		o.selectedEnv = absolute(o.selectedEnv, d.CWD, d.Home)
	}
	o.fileEnvironment = map[string]string{}
	envFiles := []string{}
	candidates := []string{}
	for _, dir := range d.Directories {
		candidates = append(candidates, filepath.Join(dir, ".env"))
	}
	for _, p := range uniqueLast(candidates, o.selectedEnv) {
		b, ok, e := readFile(p, p == o.selectedEnv, o.Flags.Init)
		if e != nil {
			return e
		}
		if !ok {
			continue
		}
		values, e := ncconfig.ParseEnvironment(string(b), o.fileEnvironment, o.process)
		if e != nil {
			return ErrOptions
		}
		resolveEnvironment(values, filepath.Dir(p), d.Home)
		for k, v := range values {
			o.fileEnvironment[k] = v
		}
		envFiles = append(envFiles, p)
	}
	o.Environment = map[string]string{}
	for _, m := range []map[string]string{o.fileEnvironment, o.process} {
		for k, v := range m {
			o.Environment[k] = v
		}
	}
	selected := o.selectedConfig
	if strings.TrimSpace(selected) == "" {
		selected = o.process["WIRELESS_CONFIG"]
		if strings.TrimSpace(selected) == "" {
			selected = o.fileEnvironment["WIRELESS_CONFIG"]
		}
	}
	if strings.TrimSpace(selected) != "" {
		o.selectedConfig = absolute(selected, d.CWD, d.Home)
	} else {
		o.selectedConfig = ""
	}
	o.Config = o.selectedConfig
	if o.Config == "" {
		o.Config = filepath.Join(o.userDirectory, "wireless.yml")
	}
	o.EnvFile = o.selectedEnv
	if o.EnvFile == "" {
		o.EnvFile = filepath.Join(o.userDirectory, ".env")
	}
	o.Sources = wireless.Record{"configuration_files": []string{}, "environment_files": envFiles, "fields": map[string]string{}}
	return nil
}
func recordFields(fields map[string]string, m, previous wireless.Record, source, prefix string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		before, _ := previous[k].(map[string]any)
		child, ok := v.(map[string]any)
		if ok && len(child) == 0 && len(before) > 0 {
			continue
		}
		if ok && len(child) > 0 {
			delete(fields, key)
			recordFields(fields, child, before, source, key)
		} else {
			for field := range fields {
				if field == key || strings.HasPrefix(field, key+".") {
					delete(fields, field)
				}
			}
			fields[key] = source
		}
	}
}

func (o *Options) loadFiles() (wireless.Record, wireless.Record, error) {
	data, specific := wireless.Record{}, wireless.Record{}
	paths := []string{}
	loaded := []string{}
	fields := map[string]string{}
	for _, dir := range o.discovery.Directories {
		paths = append(paths, filepath.Join(dir, "net-connector.yml"), filepath.Join(dir, "wireless.yml"))
	}
	for _, p := range uniqueLast(paths, o.selectedConfig) {
		b, ok, e := readFile(p, p == o.selectedConfig, o.Flags.Init)
		if e != nil {
			return nil, nil, e
		}
		if !ok {
			continue
		}
		m, e := ncconfig.ParseYAML(b)
		if e != nil {
			return nil, nil, ErrOptions
		}
		native := m
		if filepath.Base(p) == "net-connector.yml" {
			native, e = o.sharedConfiguration(m, filepath.Dir(p))
			if e != nil {
				return nil, nil, e
			}
			o.applicable = o.applicable || has(native, "netdisco") || has(native, "inventory")
		} else {
			if has(m, "common") || has(m, "wireless") {
				return nil, nil, ErrLegacyConfiguration
			}
			o.applicable = true
			resolvePaths(native, filepath.Dir(p), o.discovery.Home, "")
			specific = merge(specific, native)
		}
		recordFields(fields, native, data, p, "")
		data = merge(data, native)
		loaded = append(loaded, p)
	}
	if o.selectedConfig == "" && len(loaded) > 0 {
		o.Config = loaded[len(loaded)-1]
	}
	o.Sources["configuration_files"] = loaded
	o.Sources["fields"] = fields
	return data, specific, nil
}

// sharedConfiguration validates the flat connector schema without importing its
// defaults or environment. Wireless consumes only its own inventory and trust
// settings; connection timeouts, device targets and report policy stay local.
func (o *Options) sharedConfiguration(data wireless.Record, directory string) (wireless.Record, error) {
	if has(data, "common") || has(data, "wireless") {
		return nil, ErrLegacyConfiguration
	}
	validated, err := ncconfig.Load(ncconfig.Options{
		Name: "backup", CWD: directory, Home: o.discovery.Home,
		Directories: []string{}, Environment: map[string]string{},
		Defaults: wireless.Record{}, Overrides: data,
	})
	if err != nil {
		return nil, ErrOptions
	}
	public := validated.Public()
	projected := wireless.Record{}
	for _, name := range []string{"netdisco", "inventory"} {
		if value, ok := public[name]; ok {
			projected[name] = value
		}
	}
	if ssh, ok := public["ssh"].(map[string]any); ok {
		trust := wireless.Record{}
		for _, name := range []string{"host_key_policy", "known_hosts"} {
			if value, ok := ssh[name]; ok {
				trust[name] = value
			}
		}
		if len(trust) > 0 {
			projected["ssh"] = trust
		}
	}
	return projected, nil
}
func in(s string, values ...string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}
func boolValue(v any) bool                   { b, _ := v.(bool); return b }
func has(m wireless.Record, key string) bool { _, ok := m[key]; return ok }
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
func stringValue(v any) string { s, _ := v.(string); return s }
func stringList(v any) ([]string, error) {
	a, ok := v.([]any)
	if !ok {
		return nil, ErrOptions
	}
	out := []string{}
	for _, v := range a {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return nil, ErrOptions
		}
		out = append(out, s)
	}
	return out, nil
}
func (o *Options) load(data, specific wireless.Record) error {
	dynamic := has(data, "netdisco") || has(data, "inventory")
	if rows, ok := specific["devices"].([]any); ok && len(rows) > 0 && !has(specific, "netdisco") && !has(specific, "inventory") && !has(specific, "netdisco_config") {
		delete(data, "netdisco")
		delete(data, "inventory")
		dynamic = false
	}
	if has(specific, "netdisco_config") && !has(specific, "netdisco") && !has(specific, "inventory") {
		delete(data, "netdisco")
		delete(data, "inventory")
		dynamic = false
	}
	defaults := wireless.Record{"platform": "h3c", "ap_names": []any{}, "devices": []any{}, "parallel": 4, "directory": filepath.Join(o.discovery.CWD, "wireless-reports"), "dimensions": nil, "ssh": wireless.Record{"host_key_policy": "strict", "command_timeout": 60, "login_timeout": 15}}
	proc, e := o.publicEnvironment(o.process, dynamic, o.overrides)
	if e != nil {
		return e
	}
	file, e := o.publicEnvironment(o.fileEnvironment, dynamic, merge(data, proc, o.overrides))
	if e != nil {
		return e
	}
	resolvePaths(o.overrides, o.discovery.CWD, o.discovery.Home, "")
	o.Values = merge(defaults, file, data, proc, o.overrides)
	if e = o.validate(); e != nil {
		return e
	}
	if o.Dynamic() {
		return o.loadInventoryPolicy()
	}
	return nil
}
func (o *Options) Dynamic() bool {
	return has(o.Values, "netdisco") || has(o.Values, "inventory") || has(o.Values, "netdisco_config")
}
func (o *Options) publicEnvironment(env map[string]string, dynamic bool, higher wireless.Record) (wireless.Record, error) {
	result := wireless.Record{}
	for key, field := range map[string]string{"WIRELESS_PLATFORM": "platform", "WIRELESS_DIRECTORY": "directory"} {
		if v, ok := env[key]; ok {
			result[field] = v
		}
	}
	if v, ok := env["WIRELESS_PARALLEL"]; ok {
		result["parallel"] = v
	} else if v, ok := env["NC_PARALLEL"]; ok {
		result["parallel"] = v
	}
	for key, field := range map[string]string{"NC_HOST_KEY_POLICY": "host_key_policy", "NC_KNOWN_HOSTS": "known_hosts"} {
		if v, ok := env[key]; ok {
			ssh, _ := result["ssh"].(map[string]any)
			if ssh == nil {
				ssh = wireless.Record{}
				result["ssh"] = ssh
			}
			ssh[field] = v
		}
	}
	if dynamic {
		project, e := inventoryEnvironment(env, merge(higher, wireless.Record{"netdisco": o.inventoryOverrides}))
		if e != nil {
			return nil, e
		}
		result = merge(result, project)
	}
	return result, nil
}
func (o *Options) validate() error {
	for k := range o.Values {
		if !in(k, "platform", "ap_names", "devices", "netdisco", "inventory", "netdisco_config", "parallel", "directory", "dimensions", "ssh", "hosts", "exclude_hosts", "names", "exclude_names") {
			return ErrOptions
		}
	}
	if s, ok := o.Values["parallel"].(string); ok {
		n, e := strconv.Atoi(s)
		if e != nil || s == "" || strings.Trim(s, "0123456789") != "" {
			return ErrOptions
		}
		o.Values["parallel"] = n
	}
	if n, ok := o.Values["parallel"].(int); !ok || n < 1 || n > 500 {
		return ErrOptions
	}
	if !in(stringValue(o.Values["platform"]), "h3c", "h3c_wireless", "huawei") || strings.TrimSpace(stringValue(o.Values["directory"])) == "" || strings.ContainsRune(stringValue(o.Values["directory"]), 0) {
		return ErrOptions
	}
	if o.Values["dimensions"] != nil {
		a, e := stringList(o.Values["dimensions"])
		if e != nil || len(a) == 0 {
			return ErrOptions
		}
		seen := map[string]bool{}
		for _, s := range a {
			valid := false
			for _, d := range wireless.SupportedDimensions() {
				valid = valid || s == string(d)
			}
			if !valid || seen[s] {
				return ErrOptions
			}
			seen[s] = true
		}
	}
	rows, ok := o.Values["devices"].([]any)
	if !ok {
		return ErrOptions
	}
	devices, e := NormalizeDevices(rows, stringValue(o.Values["platform"]))
	if e != nil {
		return e
	}
	normalized := []any{}
	for _, d := range devices {
		normalized = append(normalized, d)
	}
	o.Values["devices"] = normalized
	inline := has(o.Values, "netdisco") || has(o.Values, "inventory")
	for _, k := range []string{"netdisco", "inventory"} {
		if has(o.Values, k) {
			if _, ok := o.Values[k].(map[string]any); !ok {
				return ErrOptions
			}
		}
	}
	if inline && (len(devices) > 0 || has(o.Values, "netdisco_config")) {
		return ErrOptions
	}
	if has(o.Values, "netdisco_config") && (strings.TrimSpace(stringValue(o.Values["netdisco_config"])) == "" || len(devices) > 0) {
		return ErrOptions
	}
	if len(o.inventoryOverrides) > 0 {
		if !o.Dynamic() {
			return ErrOptions
		}
		if inline {
			m, _ := o.Values["netdisco"].(map[string]any)
			o.Values["netdisco"] = merge(m, o.inventoryOverrides)
		}
	}
	for _, k := range []string{"hosts", "exclude_hosts", "names", "exclude_names", "ap_names"} {
		v, ok := o.Values[k]
		if !ok {
			v = []any{}
			o.Values[k] = v
		}
		a, e := stringList(v)
		if e != nil {
			return e
		}
		if in(k, "hosts", "exclude_hosts") {
			for _, s := range a {
				if strings.Contains(s, "-") {
					return ErrOptions
				}
			}
			if _, e = inventory.NewAddressSet(a); e != nil {
				return ErrOptions
			}
		}
	}
	ssh, ok := o.Values["ssh"].(map[string]any)
	if !ok {
		return ErrOptions
	}
	for k := range ssh {
		if !in(k, "port", "command_timeout", "login_timeout", "host_key_policy", "known_hosts") {
			return ErrOptions
		}
	}
	if !in(stringValue(ssh["host_key_policy"]), "strict", "accept_new") {
		return ErrOptions
	}
	for _, k := range []string{"command_timeout", "login_timeout"} {
		n, ok := number(ssh[k])
		if !ok || n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) || n > float64(math.MaxInt64)/1e9 {
			return ErrOptions
		}
	}
	if v, ok := ssh["port"]; ok {
		n, ok := v.(int)
		if !ok || n < 1 || n > 65535 {
			return ErrOptions
		}
	}
	if v, ok := ssh["known_hosts"]; ok {
		if strings.TrimSpace(stringValue(v)) == "" || strings.ContainsRune(stringValue(v), 0) {
			return ErrOptions
		}
	}
	return nil
}

// Initialize writes only missing templates with exclusive hard-link publication.
func (o *Options) Initialize(out io.Writer) error {
	// The initializer writes Wireless-specific fields, which are not valid in
	// the shared connector schema. Reject before creating either template.
	if filepath.Base(o.selectedConfig) == "net-connector.yml" {
		return ErrLegacyConfiguration
	}
	b, _ := templates.ReadFile("templates/wireless.yml")
	data, e := ncconfig.ParseYAML(b)
	if e != nil {
		return ErrOptions
	}
	proc, e := o.publicEnvironment(o.process, true, o.overrides)
	if e != nil {
		return e
	}
	file, e := o.publicEnvironment(o.fileEnvironment, true, merge(proc, o.overrides))
	if e != nil {
		return e
	}
	resolvePaths(data, o.discovery.CWD, o.discovery.Home, "")
	resolvePaths(o.overrides, o.discovery.CWD, o.discovery.Home, "")
	o.Values = merge(data, wireless.Record{"directory": filepath.Join(o.discovery.CWD, "wireless-reports"), "parallel": 4}, file, proc, o.overrides)
	if e = o.validate(); e != nil {
		return e
	}
	if e = o.loadInventoryPolicy(); e != nil {
		return e
	}
	// NC_PARALLEL is inherited, unless this tool explicitly overrides it.
	source := o.process
	if _, a := source["WIRELESS_PARALLEL"]; !a {
		if _, b := source["NC_PARALLEL"]; !b {
			source = o.fileEnvironment
		}
	}
	if !has(o.overrides, "parallel") {
		if _, ok := source["WIRELESS_PARALLEL"]; !ok {
			delete(o.Values, "parallel")
		}
	}
	document := o.Values
	path := o.selectedConfig
	if path == "" {
		path = filepath.Join(o.userDirectory, "wireless.yml")
	}
	body, e := yaml.Marshal(document)
	if e != nil {
		return ErrOptions
	}
	envBody, _ := templates.ReadFile("templates/env.example")
	envPath := o.selectedEnv
	if envPath == "" {
		envPath = filepath.Join(o.userDirectory, ".env")
	}
	for _, item := range []struct {
		path string
		body []byte
	}{{path, body}, {envPath, envBody}} {
		created, e := initializeFile(item.path, item.body)
		if e != nil {
			return e
		}
		verb := "已保留"
		if created {
			verb = "已创建"
		}
		if _, e = fmt.Fprintf(out, "%s：%s\n", verb, item.path); e != nil {
			return e
		}
	}
	_, e = fmt.Fprintln(out, "请检查配置及凭据后再次运行；本次未读取清单、未连接设备。")
	return e
}
func initializeFile(path string, body []byte) (bool, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return false, ErrOptions
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".wireless-init-*.tmp")
	if e != nil {
		return false, ErrOptions
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		var n int
		n, e = f.Write(body)
		if e == nil && n != len(body) {
			e = io.ErrShortWrite
		}
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return false, ErrOptions
	}
	e = os.Link(f.Name(), path)
	if errors.Is(e, os.ErrExist) {
		return false, nil
	}
	if e != nil {
		return false, ErrOptions
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return false, ErrOptions
	}
	defer dir.Close()
	if e = dir.Sync(); e != nil {
		return false, ErrOptions
	}
	return true, nil
}
func decodeEnvironment(s, kind string) (any, error) {
	switch kind {
	case "integer":
		n, e := strconv.Atoi(s)
		return n, e
	case "number":
		n, e := strconv.ParseFloat(s, 64)
		return n, e
	case "boolean":
		if s != "true" && s != "false" {
			return nil, ErrOptions
		}
		return s == "true", nil
	case "list":
		if s == "" {
			return []any{}, nil
		}
		return splitList(s)
	case "json", "json_array":
		var v any
		d := json.NewDecoder(strings.NewReader(s))
		if e := d.Decode(&v); e != nil {
			return nil, ErrOptions
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, ErrOptions
		}
		return v, nil
	default:
		return s, nil
	}
}

// LoadEnvironment freezes discovery's .env layers plus process credentials
// without loading YAML. Apply uses this boundary after explicit confirmation.
func LoadEnvironment(environment map[string]string, envFile string, d Discovery) (map[string]string, wireless.Record, error) {
	argv := []string{}
	if envFile != "" {
		argv = []string{"--env-file", envFile}
	}
	o, e := parseOptions(argv, d)
	if e != nil {
		return nil, nil, e
	}
	if e = o.loadEnvironment(environment); e != nil {
		return nil, nil, e
	}
	return o.Environment, o.Sources, nil
}
