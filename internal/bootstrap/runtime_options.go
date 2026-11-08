package bootstrap

import (
	"maps"
	"strings"
	"time"

	"github.com/gatework/go-wireless/vendors"

	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-net-connector/fleet"
	"github.com/gatework/go-net-connector/inventory"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
	"github.com/gatework/go-wireless/parser"
)

type sshSettings struct {
	port                         int
	policy                       connector.HostKeyPolicy
	knownHosts                   string
	commandTimeout, loginTimeout time.Duration
}

// runtimeOptions is constructed once after all configuration layers validate.
// Its maps and slices are never lent to public views or injected fetchers.
type runtimeOptions struct {
	workers             int
	directory, platform string
	dimensions          []wireless.Dimension
	apNames             []string
	apCredentials       collect.APCredentials
	ssh                 sshSettings
	environment         map[string]string
	dynamic             bool
	devices             []wireless.Record
	selection           inventory.Selection
	rules               *fleet.Rules
	inventory           InventorySettings
}

func (o *Options) freeze() error {
	values := copyMap(o.Values)
	environment := maps.Clone(o.Environment)
	workers, ok := values["parallel"].(int)
	if !ok {
		return ErrOptions
	}
	ssh, ok := values["ssh"].(map[string]any)
	if !ok {
		return ErrOptions
	}
	command, _ := number(ssh["command_timeout"])
	login, _ := number(ssh["login_timeout"])
	port, _ := ssh["port"].(int)
	r := &runtimeOptions{workers: workers, directory: StringValue(values["directory"]), platform: StringValue(values["platform"]), environment: environment,
		ssh:           sshSettings{port: port, policy: connector.HostKeyPolicy(StringValue(ssh["host_key_policy"])), knownHosts: StringValue(ssh["known_hosts"]), commandTimeout: time.Duration(command * 1e9), loginTimeout: time.Duration(login * 1e9)},
		apCredentials: collect.APCredentials{Username: secret(environment, "WIRELESS_AP_USERNAME"), Password: secret(environment, "WIRELESS_AP_PASSWORD")},
		dynamic:       has(values, "netdisco") || has(values, "inventory") || has(values, "netdisco_config"),
	}
	if values["dimensions"] != nil {
		names, err := stringList(values["dimensions"])
		if err != nil {
			return err
		}
		r.dimensions = make([]wireless.Dimension, len(names))
		for i, name := range names {
			r.dimensions[i] = wireless.Dimension(name)
		}
	}
	var err error
	r.apNames, err = stringList(values["ap_names"])
	if err != nil {
		return err
	}
	rows, _ := values["devices"].([]any)
	r.devices, err = NormalizeDevices(rows, r.platform)
	if err != nil {
		return err
	}
	lists := map[string][]string{}
	for _, name := range []string{"hosts", "exclude_hosts", "names", "exclude_names"} {
		lists[name], err = stringList(values[name])
		if err != nil {
			return err
		}
	}
	r.selection, err = inventory.NewSelection(inventory.SelectionOptions{IncludeHosts: lists["hosts"], ExcludeHosts: lists["exclude_hosts"], IncludeNames: lists["names"], ExcludeNames: lists["exclude_names"]})
	if err != nil {
		return ErrOptions
	}
	if r.dynamic {
		data := copyMap(o.inventoryData())
		r.rules, err = inventoryRules(data, o.registry)
		if err != nil {
			return err
		}
		r.inventory, err = compileInventory(data, environment, o.discovery.CWD)
		if err != nil {
			return err
		}
	}
	o.runtime = r
	return nil
}

func (r *runtimeOptions) connection(device wireless.Record) (connector.ConnectionConfig, error) {
	profile, err := vendors.NewBuiltinRegistry().Fetch(StringValue(device["platform"]), StringValue(device["model"]))
	if err != nil {
		return connector.ConnectionConfig{}, ErrOptions
	}
	return r.connectionForProfile(device, profile)
}

func (r *runtimeOptions) connectionForProfile(device wireless.Record, profile *parser.Profile) (connector.ConnectionConfig, error) {
	if r == nil || !profile.Valid() {
		return connector.ConnectionConfig{}, ErrOptions
	}
	prefix := "NC_" + strings.ToUpper(profile.ConnectorPlatform())
	username := secret(r.environment, prefix+"_USERNAME")
	if username == "" {
		username = secret(r.environment, "NC_DEVICE_USERNAME")
	}
	if username == "" {
		return connector.ConnectionConfig{}, ErrOptions
	}
	password := secret(r.environment, prefix+"_PASSWORD")
	if password == "" {
		password = secret(r.environment, "NC_DEVICE_PASSWORD")
	}
	return connector.ConnectionConfig{Host: StringValue(device["host"]), Username: username, Password: password,
		EnablePassword: secret(r.environment, "NC_ENABLE_PASSWORD"), Protocol: connector.ProtocolSSH, Port: r.ssh.port,
		HostKeyPolicy: r.ssh.policy, KnownHosts: r.ssh.knownHosts, CommandTimeout: r.ssh.commandTimeout, LoginTimeout: r.ssh.loginTimeout, LogLevel: "info"}, nil
}
