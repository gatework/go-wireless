package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gatework/go-wireless/internal/bootstrap"
)

func TestFlatSharedConfigurationProjectsOnlyConsumedSettings(t *testing.T) {
	d := t.TempDir()
	shared := filepath.Join(d, "shared")
	write(t, filepath.Join(shared, "net-connector.yml"), `
netdisco: {source: http, cache_directory: inventory-cache}
inventory: {include_models: [WX], exclude_models: [WX100], model_match: contains}
ssh: {known_hosts: keys, protocol: telnet, vendor_protocols: {h3c: telnet}, log_level: debug, log_directory: logs}
backup: {parallel: 12, directory: backups}
topology: {parallel: 24, directory: topology}
`)
	write(t, filepath.Join(d, "wireless.yml"), "ssh: {port: 2222, command_timeout: 30}\n")
	o, err := bootstrap.LoadConfiguration(bootstrap.Request{}, map[string]string{}, bootstrap.Discovery{CWD: d, Home: d, Directories: []string{shared, d}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Dynamic() || o.Values["parallel"] != 4 || o.Values["directory"] != filepath.Join(d, "wireless-reports") {
		t.Fatal("imported connector workflow defaults", o.Values)
	}
	ssh := o.Values["ssh"].(map[string]any)
	if ssh["host_key_policy"] != "strict" || ssh["known_hosts"] != filepath.Join(shared, "keys") || ssh["port"] != 2222 || ssh["command_timeout"] != 30 {
		t.Fatal(ssh)
	}
	for _, key := range []string{"protocol", "vendor_protocols", "log_level", "log_directory"} {
		if _, ok := ssh[key]; ok {
			t.Fatal("unconsumed shared SSH field was projected", key)
		}
	}
	if o.Values["netdisco"].(map[string]any)["cache_directory"] != filepath.Join(shared, "inventory-cache") {
		t.Fatal(o.Values)
	}
	fields := o.Sources["fields"].(map[string]string)
	if fields["ssh.known_hosts"] != filepath.Join(shared, "net-connector.yml") || fields["ssh.port"] != filepath.Join(d, "wireless.yml") {
		t.Fatal(fields)
	}
}

func TestFlatSharedConfigurationCanSupplyDynamicInventory(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "net-connector.yml"), "netdisco: {source: http}\ninventory: {include_vendors: [h3c_wireless]}\n")
	o, err := bootstrap.LoadConfiguration(bootstrap.Request{}, map[string]string{}, discovery(d), nil)
	if err != nil || !o.Dynamic() {
		t.Fatal(o, err)
	}
	if o.Values["ssh"].(map[string]any)["host_key_policy"] != "strict" {
		t.Fatal("upstream trust defaults changed Wireless", o.Values)
	}
}

func TestLegacySharedConfigurationRequiresMigration(t *testing.T) {
	for _, body := range []string{"common: {ssh: {host_key_policy: strict}}", "wireless: {devices: []}"} {
		for _, name := range []string{"net-connector.yml", "wireless.yml"} {
			t.Run(name+body, func(t *testing.T) {
				d := t.TempDir()
				write(t, filepath.Join(d, name), body)
				_, err := bootstrap.LoadConfiguration(bootstrap.Request{}, map[string]string{}, discovery(d), nil)
				if !errors.Is(err, bootstrap.ErrLegacyConfiguration) || !errors.Is(err, bootstrap.ErrOptions) {
					t.Fatal(err)
				}
			})
		}
	}
	d := t.TempDir()
	write(t, filepath.Join(d, "wireless.yml"), "netdisco_config: inventory/net-connector.yml\n")
	write(t, filepath.Join(d, "inventory/net-connector.yml"), "common: {netdisco: {source: http}}\n")
	if _, err := bootstrap.LoadConfiguration(bootstrap.Request{}, map[string]string{}, discovery(d), nil); !errors.Is(err, bootstrap.ErrLegacyConfiguration) {
		t.Fatal(err)
	}
}

func TestSharedConfigurationRejectsUnknownOrInvalidFieldsBeforeProjection(t *testing.T) {
	for _, body := range []string{
		"netdisco: {password: private}",
		"unknown: {}",
		"ssh: {command_timeout: 30}",
		"ssh: {vendor_protocols: false}",
		"backup: {parallel: false}",
		"inventory: {include_models: WX}",
	} {
		t.Run(body, func(t *testing.T) {
			d := t.TempDir()
			write(t, filepath.Join(d, "net-connector.yml"), body)
			write(t, filepath.Join(d, "wireless.yml"), "devices: []\n")
			if _, err := bootstrap.LoadConfiguration(bootstrap.Request{}, map[string]string{}, discovery(d), nil); !errors.Is(err, bootstrap.ErrOptions) {
				t.Fatal(err)
			}
		})
	}
}

func TestInitializeCannotPublishWirelessFieldsIntoSharedConfiguration(t *testing.T) {
	d := t.TempDir()
	request := bootstrap.Request{Initialize: true, ConfigFile: "net-connector.yml", ExplicitConfig: true, EnvFile: ".env", ExplicitEnv: true}
	o, err := bootstrap.LoadConfiguration(request, map[string]string{}, discovery(d), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = o.Initialize(&out); !errors.Is(err, bootstrap.ErrLegacyConfiguration) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(d)
	if err != nil || len(entries) != 0 || out.Len() != 0 {
		t.Fatal("rejected initialization changed files", entries, out.String(), err)
	}
}
