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

	"github.com/gatework/go-wireless/internal/bootstrap"
	"github.com/gatework/go-wireless/internal/input"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/internal/workflow/apply"
)

func applyCommand(ctx context.Context, o *documentFlags, r *Runner) int {
	flags, help := o.Values, o.Help
	var e error
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
	body, e := input.Document(ctx, flags["--plan"])
	if ctx.Err() != nil {
		return 130
	}
	if e != nil {
		return r.failure("计划读取失败")
	}
	document, _, e := wireless.DecodeJSONDocument(body)
	if e != nil {
		return r.failure("计划结构、目标、指纹或命令无效")
	}
	plan, e := change.NewChangePlan(document)
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
	fmt.Fprintf(&display, "目标：%s（%s）；%s；%s\n影响：%s\n", safe(StringValue(device["name"])), host, safe(StringValue(document["scope"])), safe(StringValue(document["title"])), safe(StringValue(document["impact"])))
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
	open, e := bootstrap.PrepareApply(ctx, r.Environment, flags["--env-file"], r.Discovery, r.Registry, r.ApplyFactory, plan, port)
	if e != nil {
		if ctx.Err() != nil {
			return 130
		}
		return r.failure(setupMessage(e))
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
