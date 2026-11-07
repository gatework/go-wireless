package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/gatework/go-net-connector/connector"
	"github.com/gatework/go-net-connector/vendors"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/adapter"
	"github.com/gatework/go-wireless/report"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ApplyFactory owns one synchronous borrowed session callback and returns
// operation and close diagnostics separately. Native defaults take one lease.
type ApplyFactory func(context.Context, wireless.Record, connector.ConnectionConfig, func(wireless.ApplySession) error) (error, error)

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
	plan, _, e := wireless.DecodeJSONDocument(body)
	if e != nil || wireless.ValidateChangePlan(plan) != nil {
		return r.failure("计划结构、目标、指纹或命令无效")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(body))
	device := plan["device"].(wireless.Record)
	host := device["host"].(string)
	commands, _ := stringList(plan["commands"])
	rollback, _ := stringList(plan["rollback"])
	var display strings.Builder
	fmt.Fprintf(&display, "目标：%s（%s）；%s；%s\n影响：%s\n", safe(stringValue(device["name"])), host, safe(stringValue(plan["scope"])), safe(stringValue(plan["title"])), safe(stringValue(plan["impact"])))
	if prerequisites, ok := stringList(plan["prerequisites"]); ok == nil {
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
	profile, e := vendors.Profile("h3c_wireless")
	if e != nil {
		return r.failure("下发平台错误")
	}
	native, e := connector.New(profile, cfg)
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
	state := wireless.Record{"status": "confirmed", "device": device, "plan_sha256": hash, "expected_config_sha256": plan["expected_config_sha256"], "commands": commands, "steps": []wireless.Record{}}
	started := false
	stage := "connect"
	fail := func(cause error, cleanup error) int {
		state["status"] = "failed"
		if started {
			state["status"] = "uncertain"
		}
		state["error"] = "apply_failed"
		if ctx.Err() != nil {
			state["error"] = "interrupted"
		}
		diagnostics := &applyDiagnostics{}
		fallback := "command_execution_failed"
		if stage == "connect" {
			fallback = "connection_failed"
		} else if stage == "response" {
			fallback = "invalid_command_result"
		}
		diagnostics.collect(cause, "operation_errors", fallback, stage)
		diagnostics.collect(ctx.Err(), "cancellation_errors", fallback, stage)
		diagnostics.collect(cleanup, "cleanup_errors", "session_close_failed", "close")
		diagnostics.attach(state)
		if cleanup != nil {
			state["cleanup_error"] = "session_close_failed"
		}
		if e := store.WriteExecution(context.Background(), state); e != nil {
			diagnostics.collect(e, "report_errors", "command_execution_failed", stage)
			diagnostics.attach(state)
			_, _ = fmt.Fprintln(r.Error, "执行日志收尾未确认；新增诊断仅保留在终端，已有检查点保留，禁止据此重放配置。")
		}
		summary := wireless.Record{"status": state["status"], "error": state["error"]}
		if pending, ok := state["pending_command"]; ok {
			summary["pending_command"] = pending
		}
		diagnostics.attach(summary)
		if safeJSON, encodeErr := wireless.MarshalJSON(summary); encodeErr == nil {
			_, _ = fmt.Fprintf(r.Error, "安全诊断：%s\n", safeJSON)
		}
		_, _ = fmt.Fprintln(r.Error, "下发停止；先回读核对，不自动重放、回退或保存配置。")
		if ctx.Err() != nil {
			return 130
		}
		return 2
	}
	if e = store.WriteExecution(ctx, state); e != nil {
		return fail(e, nil)
	}
	change := plan["change"].(wireless.Record)
	factory := r.ApplyFactory
	if factory == nil {
		factory = func(ctx context.Context, _ wireless.Record, _ connector.ConnectionConfig, run func(wireless.ApplySession) error) (error, error) {
			return adapter.Apply(ctx, native, run)
		}
	}
	operation := func(session wireless.ApplySession) error {
		if session == nil {
			return wireless.ErrChangePlan
		}
		stage = "response"
		before, e := session.ReadConfiguration(ctx)
		if e != nil {
			return e
		}
		fingerprint, e := wireless.Fingerprint(before)
		if e != nil || fingerprint != plan["expected_config_sha256"] || !wireless.ChangeEligible(before, change) {
			return wireless.ErrChangePlan
		}
		for i, command := range commands {
			stage = "execute"
			if e = ctx.Err(); e != nil {
				return e
			}
			state["status"] = "executing"
			state["pending_command"] = i
			if e = store.WriteExecution(ctx, state); e != nil {
				var pe *report.Error
				if errors.As(e, &pe) && pe.Committed {
					started = true
				}
				return e
			}
			started = true
			if e = ctx.Err(); e != nil {
				return e
			}
			if e = session.Write(ctx, command); e != nil {
				return e
			}
			steps := state["steps"].([]wireless.Record)
			state["steps"] = append(steps, wireless.Record{"index": i, "status": "acknowledged"})
			delete(state, "pending_command")
			if e = store.WriteExecution(context.WithoutCancel(ctx), state); e != nil {
				return e
			}
			if e = ctx.Err(); e != nil {
				return e
			}
		}
		stage = "response"
		after, e := session.ReadConfiguration(ctx)
		if e != nil {
			return e
		}
		if !wireless.ChangeSatisfied(after, change) {
			return errors.New("wireless: readback not satisfied")
		}
		fingerprint, e = wireless.Fingerprint(after)
		if e != nil {
			return e
		}
		state["observed_config_sha256"] = fingerprint
		state["readback_verified"] = true
		return ctx.Err()
	}
	operationErr, cleanupErr := ownedApply(ctx, factory, device, cfg, operation)
	if operationErr != nil || cleanupErr != nil || ctx.Err() != nil {
		return fail(operationErr, cleanupErr)
	}
	state["status"] = "configuration_verified"
	if e = store.FinalizeExecution(ctx, state); e != nil {
		return fail(e, nil)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err(), nil)
	}
	if _, e = fmt.Fprintf(r.Output, "配置回读已确认；仍需完成业务验收，未执行保存配置。日志：%s/execution.json\n", store.Directory()); e != nil {
		return 2
	}
	return 0
}

// Host callbacks cannot replay writes, outlive the factory or race its return.
func ownedApply(ctx context.Context, factory ApplyFactory, device wireless.Record, cfg connector.ConnectionConfig, run func(wireless.ApplySession) error) (error, error) {
	var mu sync.Mutex
	active, started := true, false
	done := make(chan struct{})
	var callbackErr error
	opErr, closeErr := factory(ctx, device, cfg, func(s wireless.ApplySession) error {
		mu.Lock()
		if !active || started {
			mu.Unlock()
			return wireless.ErrChangePlan
		}
		started = true
		mu.Unlock()
		e := run(s)
		mu.Lock()
		callbackErr = e
		close(done)
		mu.Unlock()
		return e
	})
	mu.Lock()
	active = false
	admitted := started
	mu.Unlock()
	if !admitted {
		if opErr == nil {
			opErr = wireless.ErrChangePlan
		}
		return opErr, closeErr
	}
	<-done
	return errors.Join(opErr, callbackErr, ctx.Err()), closeErr
}
