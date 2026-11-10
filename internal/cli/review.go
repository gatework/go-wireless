package cli

import (
	"context"
	"errors"
	"fmt"

	reviewrun "github.com/gatework/go-wireless/internal/workflow/review"
)

func reviewCommand(ctx context.Context, o *documentFlags, r *Runner) int {
	flags, help := o.Values, o.Help
	var e error
	if help {
		return r.Help("review")
	}
	if flags["--batch"] == "" {
		return r.failure("需要批次目录")
	}
	result, err := reviewrun.Run(ctx, reviewrun.Options{Batch: flags["--batch"], Output: flags["--output"], RulesPolicy: flags["--rules-policy"], Scripts: o.Scripts, NewReport: r.NewReport, ReportOptions: r.ReportOptions})
	if err != nil {
		if ctx.Err() != nil {
			return 130
		}
		var problem *reviewrun.Error
		if errors.As(err, &problem) {
			messages := map[string]string{"batch_path": "批次路径错误", "no_reports": "无设备报告", "read": "设备报告读取失败", "document": "设备报告结构错误", "directory": "建议报告目录错误", "publish": "建议发布失败；已保存证据保留", "finalize": "建议摘要发布未确认；保留已有报告", "rules_policy": "风险规则策略无效", "assessment": "风险评估失败"}
			return r.failure(messages[problem.Stage])
		}
		return r.failure("建议生成失败")
	}
	if _, e = fmt.Fprintf(r.Output, "建议已生成：%s/review.txt；%d 台；未执行设备变更。\n", result.Directory, result.Devices); e != nil {
		return 2
	}
	return 0
}
