package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gatework/go-wireless/internal/bootstrap"

	"github.com/gatework/go-net-connector/netdisco"
	wireless "github.com/gatework/go-wireless"
	collectrun "github.com/gatework/go-wireless/internal/workflow/collect"
)

func (r *Runner) collect(ctx context.Context, o *commandOptions) int {
	var e error
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
	if o.Options, e = bootstrap.LoadConfiguration(ctx, o.Request, r.Environment, r.Discovery, r.Registry); e != nil {
		if ctx.Err() != nil {
			return 130
		}
		return r.failure("WIRELESS 环境文件错误")
	}
	if o.Flags.Init {
		if e = o.Initialize(ctx, r.Output); e != nil {
			if ctx.Err() != nil {
				return 130
			}
			if errors.Is(e, bootstrap.ErrLegacyConfiguration) {
				return r.failure("初始化请指定 wireless.yml；net-connector.yml 仅保存公共配置")
			}
			return r.failure("WIRELESS 初始化错误")
		}
		return 0
	}
	if ctx.Err() != nil {
		return 130
	}
	if o.Missing() {
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
				if e = o.Initialize(ctx, r.Output); e != nil {
					if ctx.Err() != nil {
						return 130
					}
					return r.failure("WIRELESS 初始化错误")
				}
				return 0
			}
		}
		return r.failure("配置不存在；请执行 --init 后填写设备，或使用 --config 指定配置")
	}
	if ctx.Err() != nil {
		return 130
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
	inventoryResult, e := o.FetchInventory(ctx, r.InventoryFetch)
	devices, total := inventoryResult.Devices, inventoryResult.Total
	if e != nil {
		if ctx.Err() != nil {
			return 130
		}
		var problem *netdisco.InventoryError
		if errors.As(e, &problem) && problem.Code == "cache_namespace_required" {
			return r.failure("API key 清单缓存需要 ND_USERNAME 或非秘密 ND_CACHE_NAMESPACE 来区分账号")
		}
		if errors.Is(e, netdisco.ErrCacheRefreshRequired) {
			return r.failure("当前清单身份尚未刷新；请使用 --refresh-inventory 重新采集后再使用 --cache-only")
		}
		return r.failure("WIRELESS 清单读取或策略错误")
	}
	if o.Flags.InventoryOnly {
		if o.Flags.JSON {
			e = r.outputJSON(devices)
		} else {
			for _, d := range devices {
				if _, e = fmt.Fprintf(r.Output, "%s\t%s\t%s\n", d["host"], safe(StringValue(d["name"])), safe(StringValue(d["model"]))); e != nil {
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
	prepared, e := bootstrap.PrepareCollection(ctx, o.Options, devices, bootstrap.CollectionDependencies{
		Registry: r.Registry, SessionFactory: r.SessionFactory, APFactory: r.APFactory, PrepareResources: r.PrepareResources,
		Execute: collectrun.Executor(r.Execute), NewReport: r.NewReport, ReportOptions: r.ReportOptions, RecommendationHook: r.RecommendationHook,
		Scripts: o.Flags.Scripts, SessionEvent: p.event,
	})
	if e != nil {
		if ctx.Err() != nil {
			return 130
		}
		return r.failure(setupMessage(e))
	}
	p.plan(total, len(devices), o.Workers())
	prepared.Events = collectrun.Events{Started: p.start, Completed: p.result, Finalized: p.finish, OutputError: p.outputError, Deliver: func(summary wireless.Record) error {
		if o.Flags.JSON {
			return r.outputJSON(summary)
		}
		return nil
	}}
	result := collectrun.Run(ctx, prepared)
	if result.FinalizationFailed {
		p.notice("WIRELESS 摘要报告错误；已采集设备结果保留")
		if result.RecoveryFailed {
			p.notice("摘要恢复未确认；请依据退出码及报告目录检查")
		}
	}
	if result.Interrupted || ctx.Err() != nil {
		return 130
	}
	if result.Failed {
		return 2
	}
	if result.Summary["status"] == "complete" {
		return 0
	}
	return 1
}
