package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"unicode"

	collectrun "github.com/gatework/go-wireless/internal/workflow/collect"

	"github.com/gatework/go-wireless/internal/bootstrap"

	"golang.org/x/term"

	"github.com/gatework/go-net-connector/vendors/dialect"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/collect"
	"github.com/gatework/go-wireless/report"
)

const Version = "0.4.0"

type Executor func(context.Context, int, []wireless.Record, func(context.Context, wireless.Record) error) error
type Command func(context.Context, []string, *Runner) int
type Dependencies struct {
	Environment      map[string]string
	Input            io.Reader
	Output, Error    io.Writer
	Discovery        bootstrap.Discovery
	Registry         dialect.Registry
	SessionFactory   bootstrap.SessionFactory
	ApplyFactory     bootstrap.ApplyFactory
	InventoryFetch   bootstrap.InventoryFetcher
	APFactory        collect.APFactory
	PrepareResources func(context.Context, int) error
	Execute          Executor
	ReportOptions    report.Options
	// RecommendationHook selects enrichment for the effective --scripts flag.
	RecommendationHook func(scripts bool) report.DeviceHook
	NewReport          func(string, report.Options) (*report.Report, error)
	// Commands optionally overrides a default command implementation.
	Commands map[string]Command
	// Terminal can be injected for stream tests; the default checks OS descriptors.
	Terminal func(any) bool
}
type Runner struct{ Dependencies }

func New(d Dependencies) *Runner {
	if d.Input == nil {
		d.Input = os.Stdin
	}
	if d.Output == nil {
		d.Output = os.Stdout
	}
	if d.Error == nil {
		d.Error = os.Stderr
	}
	if d.Environment == nil {
		d.Environment = map[string]string{}
		for _, entry := range os.Environ() {
			k, v, _ := strings.Cut(entry, "=")
			d.Environment[k] = v
		}
	} else {
		m := map[string]string{}
		maps.Copy(m, d.Environment)
		d.Environment = m
	}
	defaults := bootstrap.CollectionDefaults(bootstrap.CollectionDependencies{Registry: d.Registry, PrepareResources: d.PrepareResources, Execute: collectrun.Executor(d.Execute), NewReport: d.NewReport, ReportOptions: d.ReportOptions, RecommendationHook: d.RecommendationHook})
	d.Registry, d.PrepareResources, d.Execute = defaults.Registry, defaults.PrepareResources, Executor(defaults.Execute)
	d.NewReport, d.RecommendationHook = defaults.NewReport, defaults.RecommendationHook
	if d.Terminal == nil {
		d.Terminal = isTerminal
	}
	return &Runner{d}
}
func Run(ctx context.Context, argv []string, d Dependencies) int { return New(d).Run(ctx, argv) }
func isTerminal(v any) bool {
	if f, ok := v.(interface{ Fd() uintptr }); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}

// Help prints a bundled command help without configuration or input reads.
func (r *Runner) Help(command string) int { return r.help(command, false) }

func (r *Runner) usage() int {
	_, e := fmt.Fprintln(r.Output, "用法：wireless collect [选项]  只读采集与报告\n      wireless parse [选项]    离线文件或 stdin 解析\n      wireless review [选项]   离线生成建议\n      wireless apply [选项]    交互式单设备变更\n      wireless --version\n使用 wireless <命令> --help 查看参数；collect --help-all 查看完整约定，collect/parse --examples 查看范例。")
	if e != nil {
		return 2
	}
	return 0
}
func (r *Runner) help(command string, all bool) int {
	file := command
	if all && command == "collect" {
		file = "collect-all"
	}
	b, e := templates.ReadFile("templates/" + file + ".txt")
	if e != nil {
		return 2
	}
	text := string(b)
	if all && command == "collect" {
		cwd := r.Discovery.CWD
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		text = strings.ReplaceAll(text, "/Users/wenwu.yan/Works/ruby/wireless/wireless-reports", filepathJoin(cwd, "wireless-reports"))
	}
	if _, e = io.WriteString(r.Output, text); e != nil {
		return 2
	}
	return 0
}
func filepathJoin(a, b string) string {
	if strings.HasSuffix(a, string(os.PathSeparator)) {
		return a + b
	}
	return a + string(os.PathSeparator) + b
}
func (r *Runner) failure(message string) int {
	_, _ = fmt.Fprintf(r.Error, "%s；使用 --help 查看参数\n", message)
	return 2
}
func (r *Runner) outputJSON(v any) error { return r.outputJSONContext(context.Background(), v) }
func (r *Runner) outputJSONContext(ctx context.Context, v any) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	b, e := wireless.MarshalJSONIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	b = append(b, '\n')
	n, e := r.Output.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	return e
}

func safe(v string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
}
