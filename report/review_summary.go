package report

import (
	"context"
	"fmt"
	"strings"

	wireless "github.com/gatework/go-wireless"
)

// PublishReviewSummary writes the human-readable companion to the review checkpoint.
func (p *Report) PublishReviewSummary(ctx context.Context, entries []wireless.Record, counts wireless.Record) error {
	lines := []string{"WIRELESS 修复建议", "", "仅生成建议，未执行设备变更。candidate 需要确认条件；blocked 需要补齐参数；investigate 为排查或补采任务。", "", fmt.Sprintf("设备：%d；建议分类：%v", len(entries), counts), "", "未命中规则不代表网络健康；本次不推断跨设备 Fabric 归属或完整业务基线。", ""}
	for _, entry := range entries {
		device := entry["device"].(wireless.Record)
		lines = append(lines, fmt.Sprintf("%v（%v）：%v；报告：%v", device["name"], device["host"], entry["counts"], entry["report"]))
	}
	return p.Publish(ctx, "review.txt", []byte(strings.Join(lines, "\n")+"\n"))
}
