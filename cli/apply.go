package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/adapter"
	"github.com/gatework/go-wireless/internal/apply"
)

// ApplyFactory owns one synchronous borrowed session callback and returns
// operation and close diagnostics separately. Native defaults take one lease.
type ApplyFactory func(context.Context, wireless.Record, connector.ConnectionConfig, func(context.Context, wireless.ApplySession) error) (error, error)

func applyCommand(ctx context.Context, args []string, r *Runner) int {
	flags, help, e := commandFlags(args, []string{"--plan", "--env-file", "--directory", "--port"}, nil)
	if e != nil {
		return r.failure("下发参数错误")
	}
	if help {
		return r.Help("apply")
	}
	if !(r.Terminal(r.Input) && r.Terminal(r.Output) && r.Terminal(r.Error)) {
		return r.failure("必须使用真实交互终端")
	}
	if flags["--plan"] == "" {
		return r.failure("需要单个计划")
	}
	port := 22
	if v := flags["--port"]; v != "" {
		port, e = strconv.Atoi(v)
		if e != nil || port < 1 || port > 65535 {
			return r.failure("SSH 端口无效")
		}
	}
	body, e := os.ReadFile(flags["--plan"])
	if e != nil {
		return r.failure("计划读取失败")
	}
	document, _, e := wireless.DecodeJSONDocument(body)
	if e != nil {
		return r.failure("计划结构、目标、指纹或命令无效")
	}
	plan, e := wireless.NewChangePlan(document)
	if e != nil {
		return r.failure("计划结构、目标、指纹或命令无效")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(body))
	device := plan.Device()
	host := plan.Host()
	commands := plan.Commands()
	rollback := plan.Rollback()
	document = plan.ToMap()
	var display strings.Builder
	fmt.Fprintf(&display, "目标：%s（%s）；%s；%s\n影响：%s\n", safe(stringValue(device["name"])), host, safe(stringValue(document["scope"])), safe(stringValue(document["title"])), safe(stringValue(document["impact"])))
	if prerequisites, ok := stringList(document["prerequisites"]); ok == nil {
		for _, v := range prerequisites {
			fmt.Fprintf(&display, "条件：%s\n", safe(v))
		}
	}
	fmt.Fprintf(&display, "计划 SHA256：%s\n拟执行命令：\n%s\n条件回退（不自动执行）：\n%s\n", hash, strings.Join(commands, "\n"), strings.Join(rollback, "\n"))
	token := "APPLY " + host + " " + hash[:12]
	fmt.Fprintf(&display, "确认业务策略及维护窗口后输入 %s；其他输入取消：", token)
	if _, e = io.WriteString(r.Output, display.String()); e != nil {
		return 2
	}
	if f, ok := r.Output.(interface{ Flush() error }); ok {
		if e = f.Flush(); e != nil {
			return 2
		}
	}
	line, e := r.ReadLine(ctx)
	if ctx.Err() != nil {
		return 130
	}
	if e != nil && !errors.Is(e, io.EOF) {
		return r.failure("确认输入失败")
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line != token {
		if _, e = fmt.Fprintln(r.Output, "已取消，未连接设备。"); e != nil {
			return 2
		}
		return 1
	}
	env, _, e := LoadEnvironment(r.Environment, flags["--env-file"], r.Discovery)
	if e != nil {
		return r.failure("凭据环境加载失败")
	}
	o := &Options{Environment: env, Values: wireless.Record{"ssh": map[string]any{"port": port, "host_key_policy": "strict", "known_hosts": env["NC_KNOWN_HOSTS"], "command_timeout": 60, "login_timeout": 15}}}
	cfg, e := connectionSettings(o, wireless.Record{"host": host, "platform": "h3c_wireless"})
	if e != nil {
		return r.failure("缺少设备凭据")
	}
	platform, e := r.Registry.Lookup("h3c_wireless")
	if e != nil {
		return r.failure("下发平台错误")
	}
	native, e := connector.New(platform.Profile(), cfg)
	if e != nil {
		return r.failure("连接参数无效")
	}
	if ctx.Err() != nil {
		return 130
	}
	root := flags["--directory"]
	if root == "" {
		cwd := r.Discovery.CWD
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		root = filepath.Join(cwd, "wireless-executions")
	}
	store, e := r.NewReport(root, r.ReportOptions)
	if e != nil {
		return r.failure("执行日志目录错误")
	}
	factory := r.ApplyFactory
	if factory == nil {
		factory = func(ctx context.Context, _ wireless.Record, _ connector.ConnectionConfig, run func(context.Context, wireless.ApplySession) error) (error, error) {
			return adapter.Apply(ctx, native, run)
		}
	}
	open := func(ctx context.Context, run func(context.Context, wireless.ApplySession) error) (error, error) {
		return factory(ctx, device, cfg, run)
	}
	result, err := apply.Run(ctx, plan, hash, open, store)
	if err != nil {
		if result.ReportUnconfirmed {
			_, _ = fmt.Fprintln(r.Error, "执行日志收尾未确认；新增诊断仅保留在终端，已有检查点保留，禁止据此重放配置。")
		}
		if safeJSON, encodeErr := wireless.MarshalJSON(result.Summary); encodeErr == nil {
			_, _ = fmt.Fprintf(r.Error, "安全诊断：%s\n", safeJSON)
		}
		_, _ = fmt.Fprintln(r.Error, "下发停止；先回读核对，不自动重放、回退或保存配置。")
		if result.Interrupted {
			return 130
		}
		return 2
	}
	if _, e = fmt.Fprintf(r.Output, "配置回读已确认；仍需完成业务验收，未执行保存配置。日志：%s/execution.json\n", store.Directory()); e != nil {
		return 2
	}
	return 0
}
