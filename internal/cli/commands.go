package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gatework/go-wireless/internal/bootstrap"
	"github.com/gatework/go-wireless/internal/buildinfo"
)

type exitError struct{ code int }

func (e exitError) Error() string { return "wireless command failed" }
func exitStatus(code int) error {
	if code == 0 {
		return nil
	}
	return exitError{code}
}

// argumentErrors are the per-command messages for invalid arguments.
var argumentErrors = map[string]string{"collect": "WIRELESS 参数错误", "parse": "离线解析参数错误", "review": "建议参数错误", "apply": "下发参数错误"}

// Run parses argv, runs the selected command and returns its exit code.
func (r *Runner) Run(ctx context.Context, argv []string) int {
	root := &cobra.Command{Use: "wireless", SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetIn(r.Input)
	root.SetOut(r.Output)
	root.SetErr(r.Error)
	r.bindRoot(root, argv)
	commands := r.addSubcommands(root)
	// The historical CLI has no implicit help/completion command.
	root.SetHelpCommand(&cobra.Command{Use: "help", Hidden: true, RunE: func(*cobra.Command, []string) error { return exitError{r.failure("未知命令或多余参数")} }})
	selected, name, args := root, "collect", argv
	if len(argv) > 0 {
		if cmd, ok := commands[argv[0]]; ok {
			selected, name, args = cmd, argv[0], argv[1:]
		}
	}
	if custom := r.Commands[name]; custom != nil {
		selected.DisableFlagParsing = true
		selected.Args = nil
		selected.RunE = func(cmd *cobra.Command, raw []string) error { return exitStatus(custom(cmd.Context(), raw, r)) }
	} else if err := validateFlagSyntax(selected.Flags(), args); err != nil {
		return r.failure(argumentErrors[name])
	}
	helpCode := 0
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) { helpCode = r.helpFor(root, cmd, argv) })
	root.SetArgs(argv)
	if err := root.ExecuteContext(ctx); err != nil {
		if status, ok := errors.AsType[exitError](err); ok {
			return status.code
		}
		return r.failure(argumentErrors[name])
	}
	return helpCode
}

// bindRoot makes the root command collect, and handle --version and the
// top-level usage.
func (r *Runner) bindRoot(root *cobra.Command, argv []string) {
	collectOptions := bindCollect(root.Flags())
	version := false
	switchFlag(root.Flags(), "version", "v", func() error {
		if len(argv) != 1 {
			return bootstrap.ErrOptions
		}
		version = true
		return nil
	})
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if version {
			if len(argv) != 1 {
				return exitError{r.failure("未知命令或多余参数")}
			}
			if _, err := fmt.Fprintln(r.Output, buildinfo.String(Version)); err != nil {
				return exitError{2}
			}
			return nil
		}
		if len(argv) == 1 && (collectOptions.Flags.Help || collectOptions.Flags.HelpAll) {
			return exitStatus(r.usage())
		}
		collectOptions.Request.Initialize = collectOptions.Flags.Init
		collectOptions.Options = bootstrap.NewOptions(collectOptions.Request, r.Discovery, r.Registry)
		return exitStatus(r.collect(cmd.Context(), collectOptions))
	}
}

// addSubcommands adds collect, parse, review and apply and returns every
// command by name (the root also collects).
func (r *Runner) addSubcommands(root *cobra.Command) map[string]*cobra.Command {
	commands := map[string]*cobra.Command{"collect": root}
	for _, name := range []string{"collect", "parse", "review", "apply"} {
		child := &cobra.Command{Use: name, Args: cobra.NoArgs}
		root.AddCommand(child)
		commands[name] = child
		if name == "collect" {
			o := bindCollect(child.Flags())
			child.RunE = func(cmd *cobra.Command, args []string) error {
				o.Request.Initialize = o.Flags.Init
				o.Options = bootstrap.NewOptions(o.Request, r.Discovery, r.Registry)
				return exitStatus(r.collect(cmd.Context(), o))
			}
			continue
		}
		o := bindDocument(child.Flags(), name)
		child.RunE = func(cmd *cobra.Command, args []string) error {
			switch name {
			case "parse":
				return exitStatus(r.parse(cmd.Context(), o))
			case "review":
				return exitStatus(reviewCommand(cmd.Context(), o, r))
			default:
				return exitStatus(applyCommand(cmd.Context(), o, r))
			}
		}
	}
	return commands
}

// helpFor prints help for cmd: the usage overview for a bare --help, the
// collect help for the root, or the command's own help.
func (r *Runner) helpFor(root, cmd *cobra.Command, argv []string) int {
	if cmd == root && len(argv) == 1 {
		return r.usage()
	}
	name := cmd.Name()
	if cmd == root {
		name = "collect"
	}
	all := false
	if flag := cmd.Flags().Lookup("help-all"); flag != nil {
		all = flag.Changed
	}
	return r.help(name, all)
}
