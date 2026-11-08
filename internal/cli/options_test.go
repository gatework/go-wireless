package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/internal/bootstrap"
)

func TestLayeredConfigurationPathsAndSecrets(t *testing.T) {
	d := t.TempDir()
	low, high := filepath.Join(d, "low"), filepath.Join(d, "high")
	write(t, filepath.Join(low, "net-connector.yml"), "ssh: {host_key_policy: accept_new, known_hosts: keys}\nnetdisco: {source: http}\nbackup: {parallel: 7}\n")
	write(t, filepath.Join(high, "wireless.yml"), "directory: reports\nparallel: 9\ndevices: [{host: '192.0.2.10'}]\n")
	write(t, filepath.Join(high, ".env"), "WIRELESS_PARALLEL=6\nNC_DEVICE_USERNAME=dotenv\nNC_DEVICE_PASSWORD='private#value$literal'\nNC_KNOWN_HOSTS=dotenv-keys\n")
	env := map[string]string{"NC_DEVICE_USERNAME": "process", "NC_PARALLEL": "8"}
	before := map[string]string{}
	for k, v := range env {
		before[k] = v
	}
	o, e := bootstrap.LoadConfiguration(bootstrap.Request{Overrides: map[string]any{"parallel": 3}}, env, bootstrap.Discovery{CWD: d, Home: d, Directories: []string{low, high}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if o.Values["parallel"] != 3 || o.Values["directory"] != filepath.Join(high, "reports") || o.Values["ssh"].(map[string]any)["known_hosts"] != filepath.Join(low, "keys") {
		t.Fatal(o.Values)
	}
	if o.Dynamic() {
		t.Fatal("dedicated static list inherited dynamic shared source")
	}
	if o.Environment["NC_DEVICE_USERNAME"] != "process" || o.Environment["NC_DEVICE_PASSWORD"] != "private#value$literal" {
		t.Fatal("credential precedence")
	}
	if !reflect.DeepEqual(env, before) {
		t.Fatal("mutated env")
	}
	b, _ := json.Marshal(o.Values)
	if bytes.Contains(b, []byte("private")) || bytes.Contains(b, []byte("process")) {
		t.Fatal("secrets leaked")
	}
}
func TestInvalidPublicConfiguration(t *testing.T) {
	bodies := []string{"parallel: 0", "parallel: 501", "parallel: false", "parallel: 1\nparallel: 2", "devices: []\n---\nparallel: 3", "devices: &d []\nap_names: *d", "devices: [{host: '192.0.2.10', password: private}]", "devices: [{host: '192.0.2.10'}, {host: '192.0.2.10'}]", "devices: [{host: '192.0.2.10/24'}]", "devices: [{host: '192.0.2.10', name: ''}]", "devices: [{host: '192.0.2.10', platform: unknown}]", "hosts: false", "exclude_names: null", "dimensions: []", "dimensions: [access_points, access_points]", "dimensions: [collection]", "ssh: {username: private}", "ssh: {host_key_policy: replace}", "ssh: {command_timeout: 0}", "ssh: {login_timeout: .inf}", "ssh: {port: 1.0}", "ssh: {port: 65536}", "platform: H3C", "directory: ''", "netdisco: null", "netdisco: {password: private}", "netdisco: {source: illegal}", "netdisco: {page_size: 0}", "netdisco: {cache_enabled: false, cache_mode: only}", "netdisco: {url: 'http://example.invalid'}", "netdisco: {url: 'https://name:private@example.invalid'}", "inventory: {include_models: WX}", "inventory: {model_match: invalid}", "inventory: {device_rules: [{vendor: H3C, connector: h3c_wireless, password: private}]}", "inventory: {host_overrides: {invalid: h3c_wireless}}", "netdisco: {}\ndevices: [{host: '192.0.2.10'}]", "netdisco_config: ''", "hosts: ['192.0.2.1-2']"}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			if _, e := options(t, body, map[string]string{}); e == nil {
				t.Fatal("accepted", body)
			}
		})
	}
}
func TestConfigSelectionEmptyAndExternalInventory(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "wireless.yml"), "parallel: 5\nnetdisco_config: inventory/config.yml\n")
	write(t, filepath.Join(d, "inventory/config.yml"), "netdisco: {source: postgres, query: 'SELECT ip FROM device', cache_directory: cache, db_sslrootcert: system}\ninventory: {include_names: [controller]}\nbackup: {parallel: 501}\nssh: unused-invalid\n")
	o, e := bootstrap.LoadConfiguration(bootstrap.Request{}, map[string]string{"WIRELESS_CONFIG": "", "ND_CACHE_TTL": "180", "NC_PROTOCOL": "invalid unrelated"}, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	if o.Values["parallel"] != 5 {
		t.Fatal(o.Values)
	}
	effective := o.Values["inventory_configuration"].(map[string]any)["netdisco"].(map[string]any)
	if effective["cache_directory"] != filepath.Join(d, "inventory/cache") || effective["cache_ttl"] != float64(180) || effective["db_sslrootcert"] != "system" {
		t.Fatal(effective)
	}
	if _, e = bootstrap.LoadConfiguration(bootstrap.Request{ConfigFile: "missing.yml", ExplicitConfig: true}, map[string]string{}, discovery(d), nil); e == nil {
		t.Fatal("missing explicit accepted")
	}
}
func TestInventoryEnvironmentMasking(t *testing.T) {
	for _, process := range []bool{false, true} {
		t.Run(map[bool]string{false: "dotenv", true: "process"}[process], func(t *testing.T) {
			d := t.TempDir()
			write(t, filepath.Join(d, "wireless.yml"), "netdisco: {cache_enabled: false, cache_mode: prefer}\n")
			env := map[string]string{}
			if process {
				env["ND_CACHE_MODE"] = "invalid"
			} else {
				write(t, filepath.Join(d, ".env"), "ND_CACHE_MODE=invalid\nND_PAGE_SIZE=invalid\n")
				env["ND_PAGE_SIZE"] = "10"
			}
			request := bootstrap.Request{InventoryOverrides: map[string]any{"cache_mode": "only"}}
			o, e := bootstrap.LoadConfiguration(request, env, discovery(d), nil)
			if e == nil {
				t.Fatal("cache disabled with only accepted", o)
			}
			write(t, filepath.Join(d, "wireless.yml"), "netdisco: {cache_mode: prefer}\n")
			o, e = bootstrap.LoadConfiguration(request, env, discovery(d), nil)
			if e != nil {
				t.Fatal(e)
			}
			if o.Values["netdisco"].(map[string]any)["cache_mode"] != "only" {
				t.Fatal(o.Values)
			}
		})
	}
}
func TestInitializePrivateNoOverwriteAndInheritance(t *testing.T) {
	d := t.TempDir()
	var out, diagnostic bytes.Buffer
	dependencies := Dependencies{Environment: map[string]string{"NC_PARALLEL": "8", "NC_HOST_KEY_POLICY": "strict", "ND_CACHE_TTL": "90", "NC_DEVICE_PASSWORD": "private"}, Input: panicReader{}, Output: &out, Error: &diagnostic, Discovery: discovery(d), SessionFactory: panicFactory}
	if code := Run(t.Context(), []string{"--init", "--non-interactive"}, dependencies); code != 0 {
		t.Fatal(code, diagnostic.String())
	}
	config := filepath.Join(d, ".config/net-connector/wireless.yml")
	body, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(body, []byte("parallel:")) || bytes.Contains(body, []byte("private")) {
		t.Fatalf("unexpected %s", body)
	}
	for _, path := range []string{config, filepath.Join(d, ".config/net-connector/.env")} {
		s, _ := os.Stat(path)
		if s.Mode().Perm() != 0600 {
			t.Fatal(s.Mode())
		}
	}
	write(t, config, "parallel: 12\n")
	if code := Run(t.Context(), []string{"--init", "--non-interactive"}, dependencies); code != 0 {
		t.Fatal(code, diagnostic.String())
	}
	body, _ = os.ReadFile(config)
	if string(body) != "parallel: 12\n" {
		t.Fatal("overwritten")
	}
	loaded, e := bootstrap.LoadConfiguration(bootstrap.Request{ConfigFile: config, ExplicitConfig: true}, map[string]string{"NC_PARALLEL": "13"}, discovery(d), nil)
	if e != nil {
		t.Fatal(e)
	}
	if loaded.Values["parallel"] != 13 {
		t.Fatal(loaded.Values)
	}
}
func TestInformationalAndMissingConfigurationDoNotConsumeInput(t *testing.T) {
	for _, args := range [][]string{{"collect", "--help-all"}, {"parse", "--help-all"}, {"review", "--help"}, {"apply", "--help"}, {"--help"}, {"--version"}, {"collect", "--examples"}, {"parse", "--examples"}} {
		var out, err bytes.Buffer
		d := t.TempDir()
		code := Run(context.Background(), args, Dependencies{Environment: map[string]string{"WIRELESS_PARALLEL": "invalid", "WIRELESS_CONFIG": "missing"}, Input: panicReader{}, Output: &out, Error: &err, Discovery: discovery(d), SessionFactory: panicFactory})
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, err.String())
		}
		files, _ := os.ReadDir(d)
		if len(files) != 0 {
			t.Fatal("informational wrote files")
		}
	}
	for _, args := range [][]string{{"--version", "--help"}, {"--help", "--version"}, {"-v", "--help-all"}} {
		var out, diagnostic bytes.Buffer
		if code := Run(t.Context(), args, Dependencies{Environment: map[string]string{}, Input: panicReader{}, Output: &out, Error: &diagnostic, Discovery: discovery(t.TempDir())}); code != 2 || out.Len() != 0 {
			t.Fatal("mixed version/help accepted", args, code, out.String())
		}
	}
	var out, err bytes.Buffer
	d := t.TempDir()
	code := Run(context.Background(), []string{"--non-interactive"}, Dependencies{Environment: map[string]string{}, Input: panicReader{}, Output: &out, Error: &err, Discovery: discovery(d)})
	if code != 2 || !strings.Contains(err.String(), "--init") {
		t.Fatal(code, err.String())
	}
	code = Run(context.Background(), []string{"collect", "--interactive"}, Dependencies{Environment: map[string]string{}, Input: panicReader{}, Output: &out, Error: &err, Discovery: discovery(d)})
	if code != 2 {
		t.Fatal(code)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("unexpected stdin read") }
func TestSourceFieldsMatchRubyDotPathsAndEmptyOverlayKeepsOrigin(t *testing.T) {
	d := t.TempDir()
	low := filepath.Join(d, "low")
	high := filepath.Join(d, "high")
	write(t, filepath.Join(low, "net-connector.yml"), "ssh: {known_hosts: keys}\n")
	write(t, filepath.Join(low, "wireless.yml"), "parallel: 3\n")
	write(t, filepath.Join(high, "wireless.yml"), "ssh: {}\n")
	o, e := bootstrap.LoadConfiguration(bootstrap.Request{}, map[string]string{}, bootstrap.Discovery{CWD: d, Home: d, Directories: []string{low, high}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	fields := o.Sources["fields"].(map[string]string)
	if fields["parallel"] != filepath.Join(low, "wireless.yml") || fields["ssh.known_hosts"] != filepath.Join(low, "net-connector.yml") {
		t.Fatal(fields)
	}
	if _, ok := fields["ssh"]; ok {
		t.Fatal("empty overlay changed inherited leaf origins", fields)
	}
}
func TestEmptyInventoryEnvironmentListIsExplicitEmptyButCLIListIsInvalid(t *testing.T) {
	o, e := options(t, "netdisco: {}\ninventory: {include_names: [old]}\n", map[string]string{"NC_INCLUDE_NAMES": ""})
	if e != nil {
		t.Fatal(e)
	}
	list := o.Values["inventory"].(map[string]any)["include_names"].([]any)
	if len(list) != 0 {
		t.Fatal(list)
	}
	d := t.TempDir()
	write(t, filepath.Join(d, "wireless.yml"), "devices: []")
	var out, diagnostic bytes.Buffer
	if code := Run(t.Context(), []string{"collect", "--show-config", "--name", ""}, Dependencies{Environment: map[string]string{}, Input: panicReader{}, Output: &out, Error: &diagnostic, Discovery: discovery(d), SessionFactory: panicFactory}); code != 2 {
		t.Fatal("empty CLI list accepted")
	}
}
func TestWhitespaceEnvironmentPathIsNotTurnedIntoValidDefault(t *testing.T) {
	if _, e := options(t, "devices: []", map[string]string{"WIRELESS_DIRECTORY": " "}); e == nil {
		t.Fatal("blank environment path was expanded into a directory")
	}
}
