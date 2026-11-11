package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/bootstrap"
)

type Flags struct {
	Help, HelpAll, Examples, Init, InventoryOnly, ShowConfig, JSON, Quiet, Scripts, Verbose bool
	Interactive                                                                             *bool
}
type commandOptions struct {
	*bootstrap.Options
	Request bootstrap.Request
	Flags   Flags
}

// actionValue applies validated flags in occurrence order, including opposing
// switches and repeated selection lists. Configuration remains unloaded.
type actionValue struct {
	set  func(string) error
	kind string
	text string
}

func (v *actionValue) Set(s string) error {
	if err := v.set(s); err != nil {
		return err
	}
	v.text = s
	return nil
}
func (v *actionValue) String() string {
	if v.text == "" && v.kind == "bool" {
		return "false"
	}
	return v.text
}
func (v *actionValue) Type() string { return v.kind }
func valueFlag(fs *pflag.FlagSet, name, short string, set func(string) error) {
	fs.VarP(&actionValue{set: set, kind: "value"}, name, short, "")
}
func switchFlag(fs *pflag.FlagSet, name, short string, set func() error) {
	fs.VarP(&actionValue{set: func(string) error { return set() }, kind: "bool"}, name, short, "")
	fs.Lookup(name).NoOptDefVal = "true"
}
func setTrue(v *bool) func() error { return func() error { *v = true; return nil } }
func bindCollect(fs *pflag.FlagSet) *commandOptions {
	o := &commandOptions{Request: bootstrap.Request{Overrides: wireless.Record{}, InventoryOverrides: wireless.Record{}}}
	request, flags := &o.Request, &o.Flags
	valueFlag(fs, "config", "", func(v string) error { request.ConfigFile = v; request.ExplicitConfig = true; return nil })
	valueFlag(fs, "env-file", "", func(v string) error { request.EnvFile = v; request.ExplicitEnv = true; return nil })
	valueFlag(fs, "parallel", "p", func(v string) error {
		n, e := strconv.Atoi(v)
		if e != nil {
			return bootstrap.ErrOptions
		}
		request.Overrides["parallel"] = n
		return nil
	})
	for _, name := range []string{"directory", "platform"} {
		valueFlag(fs, name, "", func(v string) error { request.Overrides[name] = v; return nil })
	}
	valueFlag(fs, "cache-mode", "", func(v string) error { return cacheOverride(request.InventoryOverrides, v) })
	valueFlag(fs, "source", "", func(v string) error {
		if v != "http" && v != "postgres" {
			return bootstrap.ErrOptions
		}
		request.InventoryOverrides["source"] = v
		return nil
	})
	for _, field := range []struct{ flag, key string }{{"dimensions", "dimensions"}, {"ap-name", "ap_names"}, {"host", "hosts"}, {"hosts", "hosts"}, {"exclude-hosts", "exclude_hosts"}, {"name", "names"}, {"exclude-name", "exclude_names"}} {
		valueFlag(fs, field.flag, "", func(v string) error {
			list, e := splitList(v)
			if e != nil {
				return e
			}
			if field.key != "dimensions" {
				old, _ := request.Overrides[field.key].([]any)
				list = append(old, list...)
			}
			request.Overrides[field.key] = list
			return nil
		})
	}
	for _, v := range []struct {
		name, short string
		target      *bool
	}{{"init", "", &flags.Init}, {"plan", "", &flags.InventoryOnly}, {"inventory-only", "", &flags.InventoryOnly}, {"show-config", "", &flags.ShowConfig}, {"json", "", &flags.JSON}, {"quiet", "", &flags.Quiet}, {"no-progress", "", &flags.Quiet}, {"verbose", "", &flags.Verbose}, {"help", "h", &flags.Help}, {"help-all", "", &flags.HelpAll}, {"examples", "", &flags.Examples}} {
		switchFlag(fs, v.name, v.short, setTrue(v.target))
	}
	for _, name := range []string{"interactive", "non-interactive"} {
		switchFlag(fs, name, "", func() error { v := name == "interactive"; flags.Interactive = &v; return nil })
	}
	for _, name := range []string{"scripts", "no-scripts"} {
		switchFlag(fs, name, "", func() error { flags.Scripts = name == "scripts"; return nil })
	}
	switchFlag(fs, "cache-only", "", func() error { return cacheOverride(request.InventoryOverrides, "only") })
	switchFlag(fs, "refresh-inventory", "", func() error { return cacheOverride(request.InventoryOverrides, "refresh") })
	return o
}
func splitList(v string) ([]any, error) {
	list := []any{}
	for v := range strings.SplitSeq(v, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, bootstrap.ErrOptions
		}
		list = append(list, v)
	}
	return list, nil
}
func cacheOverride(overrides wireless.Record, v string) error {
	if v != "prefer" && v != "only" && v != "refresh" {
		return bootstrap.ErrOptions
	}
	if old, ok := overrides["cache_mode"]; ok && old != v {
		return bootstrap.ErrOptions
	}
	overrides["cache_mode"] = v
	return nil
}

// validateFlagSyntax preserves the established CLI grammar while pflag owns
// values, aliases, callbacks, and errors. Values are skipped using declarations.
func validateFlagSyntax(fs *pflag.FlagSet, args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, _, equal := strings.Cut(arg, "=")
		var flag *pflag.Flag
		if strings.HasPrefix(key, "--") && len(key) > 2 {
			flag = fs.Lookup(key[2:])
		} else if len(key) == 2 && key[0] == '-' {
			flag = fs.ShorthandLookup(key[1:])
		}
		if flag == nil {
			return fmt.Errorf("invalid flag %s", key)
		}
		if flag.NoOptDefVal != "" {
			if equal {
				return fmt.Errorf("switch does not accept a value: %s", key)
			}
		} else if !equal {
			i++
			if i == len(args) {
				return fmt.Errorf("missing value: %s", key)
			}
		}
	}
	return nil
}

type documentFlags struct {
	Values                                  map[string]string
	Help, Examples, Complete, JSON, Scripts bool
}

func bindDocument(fs *pflag.FlagSet, kind string) *documentFlags {
	o := &documentFlags{Values: map[string]string{}}
	switchFlag(fs, "help", "h", setTrue(&o.Help))
	switchFlag(fs, "help-all", "", setTrue(&o.Help))
	var names []string
	switch kind {
	case "parse":
		o.Values["--dimension"] = "configuration"
		o.Values["--platform"] = "h3c"
		names = []string{"file", "dimension", "platform", "ap-name", "model"}
		switchFlag(fs, "complete", "", setTrue(&o.Complete))
		switchFlag(fs, "json", "", setTrue(&o.JSON))
		switchFlag(fs, "examples", "", setTrue(&o.Examples))
	case "review":
		names = []string{"batch", "output", "rules-policy", "baseline", "previous"}
		for _, name := range []string{"scripts", "no-scripts"} {
			switchFlag(fs, name, "", func() error { o.Scripts = name == "scripts"; return nil })
		}
	case "apply":
		names = []string{"plan", "env-file", "directory", "port"}
	}
	for _, name := range names {
		valueFlag(fs, name, "", func(v string) error {
			if kind != "parse" && strings.TrimSpace(v) == "" {
				return bootstrap.ErrOptions
			}
			o.Values["--"+name] = v
			return nil
		})
	}
	return o
}
