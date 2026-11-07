// Package cli owns Wireless command configuration, inventory, sessions and output.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-net-connector/netdisco"
	"github.com/gatework/go-net-connector/parallel"
	"github.com/gatework/go-net-connector/vendors"
	"github.com/gatework/go-net-connector/vendors/dialect"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/adapter"
	"github.com/gatework/go-wireless/report"
	"golang.org/x/term"
)

const Version = "0.1.0"

type Connection struct {
	Open     wireless.OpenSession
	Platform string
}
type SessionFactory func(context.Context, wireless.Record, connector.ConnectionConfig) (Connection, error)
type Executor func(context.Context, int, []wireless.Record, func(context.Context, wireless.Record) error) error
type Command func(context.Context, []string, *Runner) int
type Dependencies struct {
	Environment      map[string]string
	Input            io.Reader
	Output, Error    io.Writer
	Discovery        Discovery
	Registry         dialect.Registry
	SessionFactory   SessionFactory
	ApplyFactory     ApplyFactory
	SourceFactory    SourceFactory
	APFactory        wireless.APFactory
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
		for k, v := range d.Environment {
			m[k] = v
		}
		d.Environment = m
	}
	if d.RecommendationHook == nil && d.ReportOptions.DeviceHook == nil {
		d.RecommendationHook = report.RecommendationHook
	}
	if d.PrepareResources == nil {
		d.PrepareResources = prepareResources
	}
	if d.Registry == nil {
		d.Registry = vendors.NewBuiltinRegistry()
	}
	if d.NewReport == nil {
		d.NewReport = report.New
	}
	if d.Execute == nil {
		d.Execute = execute
	}
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
func (r *Runner) Run(ctx context.Context, argv []string) int {
	args := append([]string(nil), argv...)
	command := "collect"
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			if len(args) != 1 {
				return r.failure("未知命令或多余参数")
			}
			if _, e := fmt.Fprintln(r.Output, Version); e != nil {
				return 2
			}
			return 0
		case "--help", "--help-all", "-h":
			if len(args) == 1 {
				return r.usage()
			}
		case "collect", "parse", "review", "apply":
			command = args[0]
			args = args[1:]
		default:
			if !strings.HasPrefix(args[0], "-") {
				return r.failure("未知命令或多余参数")
			}
		}
	}
	if custom := r.Commands[command]; custom != nil {
		return custom(ctx, args, r)
	}
	switch command {
	case "collect":
		return r.collect(ctx, args)
	case "parse":
		return r.parse(ctx, args)
	case "review":
		return reviewCommand(ctx, args, r)
	case "apply":
		return applyCommand(ctx, args, r)
	default:
		return r.failure("未知命令")
	}
}
func (r *Runner) usage() int {
	_, e := fmt.Fprintln(r.Output, "用法：wireless collect [选项]  只读采集与报告\n      wireless parse [选项]    离线文件或 stdin 解析\n      wireless review [选项]   离线生成建议\n      wireless apply [选项]    交互式单设备变更\n      wireless --version\n使用 wireless <命令> --help 查看参数；collect --help-all 查看完整约定，collect/parse --examples 查看范例。")
	if e != nil {
		return 2
	}
	return 0
}

// Help prints a bundled command help without configuration or input reads.
func (r *Runner) Help(command string) int { return r.help(command, false) }
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
func (r *Runner) collect(ctx context.Context, argv []string) int {
	o, e := parseOptions(argv, r.Discovery)
	if e != nil {
		return r.failure("WIRELESS 参数错误")
	}
	o.registry = r.Registry
	if o.Flags.Help || o.Flags.HelpAll {
		return r.help("collect", o.Flags.HelpAll)
	}
	if o.Flags.Examples {
		_, e = fmt.Fprintln(r.Output, "常用示例（地址须替换为实际清单地址）：\n  wireless collect --init --non-interactive\n  wireless collect --host 192.0.2.10 --plan\n  wireless collect --name wlc --exclude-name spare -p 4\n  wireless collect --dimensions access_points,clients --json --no-progress")
		if e != nil {
			return 2
		}
		return 0
	}
	terminal := r.Terminal(r.Input) && r.Terminal(r.Error)
	if o.Flags.Interactive != nil && *o.Flags.Interactive && !terminal {
		return r.failure("--interactive 需要交互终端；自动化请使用 --init --non-interactive")
	}
	if e = o.loadEnvironment(r.Environment); e != nil {
		return r.failure("WIRELESS 环境文件错误")
	}
	if o.Flags.Init {
		if e = o.Initialize(r.Output); e != nil {
			if errors.Is(e, ErrLegacyConfiguration) {
				return r.failure("初始化请指定 wireless.yml；net-connector.yml 仅保存公共配置")
			}
			return r.failure("WIRELESS 初始化错误")
		}
		return 0
	}
	data, specific, e := o.loadFiles()
	if e != nil {
		if errors.Is(e, ErrLegacyConfiguration) {
			return r.failure("请将 common 配置移到 net-connector.yml 顶层，并将 wireless 配置移到 wireless.yml")
		}
		return r.failure("WIRELESS 配置文件错误")
	}
	if !o.applicable {
		if terminal && (o.Flags.Interactive == nil || *o.Flags.Interactive) {
			_, e = fmt.Fprintf(r.Error, "配置不存在：%s；是否创建配置与环境模板？[y/N] ", o.Config)
			if e != nil {
				return 2
			}
			if ctx.Err() != nil {
				return 130
			}
			line, readErr := r.ReadLine(ctx)
			if ctx.Err() != nil {
				return 130
			}
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return r.failure("WIRELESS 初始化输入错误")
			}
			if in(strings.ToLower(strings.TrimSpace(line)), "y", "yes") {
				if e = o.Initialize(r.Output); e != nil {
					return r.failure("WIRELESS 初始化错误")
				}
				return 0
			}
		}
		return r.failure("配置不存在；请执行 --init 后填写设备，或使用 --config 指定配置")
	}
	if e = o.load(data, specific); e != nil {
		if errors.Is(e, ErrLegacyConfiguration) {
			return r.failure("请将 common 配置移到 net-connector.yml 顶层，并将 wireless 配置移到 wireless.yml")
		}
		return r.failure("WIRELESS 公开配置错误")
	}
	p := newProgress(r.Error, !o.Flags.Quiet, o.Flags.Verbose, r.Terminal(r.Error))
	p.notice("配置：" + safe(o.Config))
	if o.Flags.ShowConfig {
		public := copyMap(o.Values)
		public["sources"] = o.Sources
		if e = r.outputJSON(public); e != nil {
			return 2
		}
		if p.outputError() != nil {
			return 1
		}
		return 0
	}
	if o.Dynamic() {
		p.notice("读取设备清单…")
	}
	devices, total, e := o.inventory(ctx, r.SourceFactory)
	if e != nil {
		if ctx.Err() != nil {
			return 130
		}
		var problem *netdisco.InventoryError
		if errors.As(e, &problem) && problem.Code == "cache_namespace_required" {
			return r.failure("API key 清单缓存需要 ND_USERNAME 或非秘密 ND_CACHE_NAMESPACE 来区分账号")
		}
		return r.failure("WIRELESS 清单读取或策略错误")
	}
	if o.Flags.InventoryOnly {
		if o.Flags.JSON {
			e = r.outputJSON(devices)
		} else {
			for _, d := range devices {
				if _, e = fmt.Fprintf(r.Output, "%s\t%s\t%s\n", d["host"], safe(stringValue(d["name"])), safe(stringValue(d["model"]))); e != nil {
					break
				}
			}
			if e == nil {
				_, e = fmt.Fprintf(r.Output, "设备：%d 台；未建立设备连接\n", len(devices))
			}
		}
		if e != nil {
			return 2
		}
		if len(devices) == 0 || p.outputError() != nil {
			return 1
		}
		return 0
	}
	if len(devices) == 0 {
		p.notice("没有匹配设备，未建立连接")
		if o.Flags.JSON {
			if e = r.outputJSON(wireless.Record{"status": "no_devices", "total": 0}); e != nil {
				return 2
			}
		}
		return 1
	}
	settings := map[string]connector.ConnectionConfig{}
	native := map[string]*connector.Device{}
	for _, device := range devices {
		cfg, e := connectionSettings(o, device)
		if e != nil {
			return r.failure("请设置 NC_DEVICE_USERNAME 或平台 NC_*_USERNAME，并检查设备连接参数")
		}
		cfg.OnSessionEvent = p.event
		platform := stringValue(device["platform"])
		if platform == "h3c" {
			platform = "h3c_wireless"
		}
		driver, e := r.Registry.Lookup(platform)
		if e != nil {
			return r.failure("WIRELESS 平台错误")
		}
		d, e := connector.New(driver.Profile(), cfg)
		if e != nil {
			return r.failure("WIRELESS 连接参数错误")
		}
		host := stringValue(device["host"])
		settings[host] = cfg
		native[host] = d
	}
	workers := o.Values["parallel"].(int)
	if e = r.PrepareResources(ctx, min(workers, len(devices))); e != nil {
		if ctx.Err() != nil {
			return 130
		}
		return r.failure("本机文件描述符资源不足；请降低 --parallel 或调整 ulimit -n")
	}
	if ctx.Err() != nil {
		return 130
	}
	reportOptions := r.ReportOptions
	if r.RecommendationHook != nil {
		reportOptions.DeviceHook = r.RecommendationHook(o.Flags.Scripts)
	}
	store, e := r.NewReport(stringValue(o.Values["directory"]), reportOptions)
	if e != nil {
		return r.failure("WIRELESS 报告目录须为真实私有目录0700，且可写")
	}
	p.plan(total, len(devices), workers)
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	results := []report.Result{}
	var firstReportError error
	e = r.Execute(batchCtx, workers, devices, func(taskCtx context.Context, device wireless.Record) error {
		start := time.Now()
		host := stringValue(device["host"])
		p.start(device)
		connection := Connection{Open: adapter.Open(native[host]), Platform: native[host].ProfileName()}
		var err error
		if r.SessionFactory != nil {
			connection, err = r.SessionFactory(taskCtx, copyMap(device), settings[host])
		}
		var snapshot *wireless.Snapshot
		result := report.Result{Device: device, Status: wireless.Failed, Dimensions: map[string]report.DimensionSummary{}}
		if err == nil {
			dimensions := []wireless.Dimension(nil)
			if o.Values["dimensions"] != nil {
				names, _ := stringList(o.Values["dimensions"])
				dimensions = []wireless.Dimension{}
				for _, n := range names {
					dimensions = append(dimensions, wireless.Dimension(n))
				}
			}
			apFactory := r.APFactory
			if apFactory == nil && containsDimension(dimensions, wireless.LLDP) && in(stringValue(device["platform"]), "h3c", "h3c_wireless") {
				apFactory = adapter.APFactory(nil)
			}
			names, _ := stringList(o.Values["ap_names"])
			collected, ce := wireless.NewCollector(wireless.CollectorOptions{APFactory: apFactory}).Collect(taskCtx, wireless.CollectOptions{Target: device, Platform: stringValue(device["platform"]), Model: stringValue(device["model"]), ConnectorPlatform: connection.Platform, Dimensions: dimensions, APNames: names, Open: connection.Open, APCredentials: func(context.Context) (wireless.APCredentials, error) {
				return wireless.APCredentials{Username: secret(o.Environment, "WIRELESS_AP_USERNAME"), Password: secret(o.Environment, "WIRELESS_AP_PASSWORD")}, nil
			}})
			err = ce
			if err == nil {
				snapshot = &collected
				result.Status = collected.Dimension(wireless.Collection).Status()
				for name, observation := range collected.Dimensions() {
					codes := []string{}
					seen := map[string]bool{}
					for _, issue := range observation.Issues() {
						code := stringValue(issue["code"])
						if code != "" && !seen[code] {
							codes = append(codes, code)
							seen[code] = true
						}
					}
					result.Dimensions[string(name)] = report.DimensionSummary{Status: observation.Status(), Records: len(observation.Records()), Issues: codes}
				}
			}
		}
		if err != nil {
			result.Error = "collection_failed"
		}
		result.DurationMS = time.Since(start).Round(time.Millisecond).Milliseconds()
		result.ReportSaved = false
		mu.Lock()
		index := len(results)
		results = append(results, result)
		mu.Unlock()
		// Persist even when another task failed or user cancellation was observed;
		// acquisition is never replayed and all completed evidence remains owned.
		err = store.WriteDevice(context.WithoutCancel(taskCtx), result, snapshot)
		mu.Lock()
		if err == nil {
			results[index].ReportSaved = true
			result.ReportSaved = true
		} else if firstReportError == nil {
			firstReportError = err
		}
		mu.Unlock()
		if err != nil {
			cancel()
			return err
		}
		p.result(result)
		return nil
	})
	// Executor returns only after callbacks and sessions have joined.
	interrupted := ctx.Err() != nil
	sort.Slice(results, func(i, j int) bool {
		return stringValue(results[i].Device["host"]) < stringValue(results[j].Device["host"])
	})
	finish := report.FinishOptions{Interrupted: interrupted, Requested: devices, OutputError: p.outputError()}
	if firstReportError != nil {
		finish.ReportError = reportError(firstReportError)
	} else if e != nil && !interrupted {
		finish.ExecutionError = "execution_failed"
	}
	summary, finishErr := store.Finish(context.Background(), results, finish)
	if finishErr == nil {
		p.finish(summary)
	}
	// Final output is also fallible and a signal may arrive during publication.
	// One finalizer owns necessary checkpoint restoration; no device is rerun.
	lateCancel := ctx.Err() != nil && !interrupted
	lateOutput := p.outputError() != nil && finish.OutputError == nil
	if lateCancel || lateOutput {
		if lateCancel {
			interrupted = true
			finish.Interrupted = true
		}
		if lateOutput {
			finish.OutputError = p.outputError()
		}
		original := finishErr
		if original != nil {
			finish.ReportError = reportError(original)
		}
		summary, finishErr = store.Finish(context.Background(), results, finish)
		if original != nil {
			finishErr = original
		}
	}
	interrupted = interrupted || ctx.Err() != nil
	if finishErr != nil {
		p.notice("WIRELESS 摘要报告错误；已采集设备结果保留")
		if store.RecoveryFailed() {
			p.notice("摘要恢复未确认；请依据退出码及报告目录检查")
		}
		if interrupted || ctx.Err() != nil {
			return 130
		}
		return 2
	}
	if o.Flags.JSON {
		if outputErr := r.outputJSON(summary); outputErr != nil {
			if ctx.Err() != nil {
				interrupted = true
				finish.Interrupted = true
			}
			finish.OutputError = wireless.Record{"host": nil, "error_type": "IOError"}
			_, recoveryErr := store.Finish(context.Background(), results, finish)
			// A signal may arrive while recovering the failed stdout delivery.
			// Observe it after recovery and publish interrupted state if possible.
			if ctx.Err() != nil {
				if !finish.Interrupted {
					finish.Interrupted = true
					if recoveryErr != nil {
						finish.ReportError = reportError(recoveryErr)
					}
					_, _ = store.Finish(context.Background(), results, finish)
				}
				return 130
			}
			if interrupted {
				return 130
			}
			return 2
		}
	}
	if ctx.Err() != nil && !interrupted {
		interrupted = true
		finish.Interrupted = true
		_, _ = store.Finish(context.Background(), results, finish)
	}
	if interrupted {
		return 130
	}
	if firstReportError != nil || e != nil {
		return 2
	}
	if summary["status"] == "complete" {
		return 0
	}
	return 1
}
func execute(ctx context.Context, workers int, devices []wireless.Record, run func(context.Context, wireless.Record) error) error {
	results, e := parallel.Map(ctx, workers, devices, func(ctx context.Context, d wireless.Record) (struct{}, error) { return struct{}{}, run(ctx, d) })
	for _, r := range results {
		if r.Started && r.Err != nil {
			return r.Err
		}
	}
	return e
}
func reportError(e error) any {
	var pe *report.Error
	if errors.As(e, &pe) {
		return pe.ToMap()
	}
	return "report_failed"
}
func containsDimension(a []wireless.Dimension, d wireless.Dimension) bool {
	for _, v := range a {
		if v == d {
			return true
		}
	}
	return false
}
func connectionSettings(o *Options, device wireless.Record) (connector.ConnectionConfig, error) {
	prefix := "NC_H3C_WIRELESS"
	if device["platform"] == "huawei" {
		prefix = "NC_HUAWEI"
	}
	username := secret(o.Environment, prefix+"_USERNAME")
	if username == "" {
		username = secret(o.Environment, "NC_DEVICE_USERNAME")
	}
	if username == "" {
		return connector.ConnectionConfig{}, ErrOptions
	}
	password := secret(o.Environment, prefix+"_PASSWORD")
	if password == "" {
		password = secret(o.Environment, "NC_DEVICE_PASSWORD")
	}
	ssh := o.Values["ssh"].(map[string]any)
	command, _ := number(ssh["command_timeout"])
	login, _ := number(ssh["login_timeout"])
	port, _ := ssh["port"].(int)
	cfg := connector.ConnectionConfig{
		Host: stringValue(device["host"]), Username: username, Password: password,
		EnablePassword: secret(o.Environment, "NC_ENABLE_PASSWORD"),
		Protocol:       connector.ProtocolSSH, Port: port,
		HostKeyPolicy: connector.HostKeyPolicy(stringValue(ssh["host_key_policy"])), KnownHosts: stringValue(ssh["known_hosts"]),
		CommandTimeout: time.Duration(command * 1e9), LoginTimeout: time.Duration(login * 1e9), LogLevel: "info",
	}
	return cfg, nil
}
func safe(v string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
}
