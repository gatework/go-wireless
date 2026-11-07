package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/report"
)

// commandFlags keeps review/apply's small, frozen flag surfaces separate from
// collect's discovery/configuration loader. Informational help is side-effect free.
func commandFlags(args []string, values, booleans []string) (map[string]string, bool, error) {
	result := map[string]string{}
	help := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if in(arg, "--help", "-h", "--help-all") {
			help = true
			continue
		}
		key, val, equal := strings.Cut(arg, "=")
		if in(key, booleans...) {
			if equal {
				return nil, false, wireless.ErrChangePlan
			}
			result[key] = "true"
			continue
		}
		if !in(key, values...) {
			return nil, false, wireless.ErrChangePlan
		}
		if !equal {
			i++
			if i == len(args) {
				return nil, false, wireless.ErrChangePlan
			}
			val = args[i]
		}
		if strings.TrimSpace(val) == "" {
			return nil, false, wireless.ErrChangePlan
		}
		result[key] = val
	}
	return result, help, nil
}

var reviewUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func reviewCommand(ctx context.Context, args []string, r *Runner) int {
	flags, help, e := commandFlags(args, []string{"--batch", "--output"}, []string{"--scripts", "--no-scripts"})
	if e != nil {
		return r.failure("建议参数错误")
	}
	if help {
		return r.Help("review")
	}
	if flags["--batch"] == "" {
		return r.failure("需要批次目录")
	}
	batch, e := filepath.Abs(flags["--batch"])
	if e != nil {
		return r.failure("批次路径错误")
	}
	paths, e := filepath.Glob(filepath.Join(batch, "devices", "*.json"))
	if e != nil || len(paths) == 0 {
		return r.failure("无设备报告")
	}
	type input struct {
		path, sha, name string
		doc, advice     wireless.Record
	}
	inputs := []input{}
	for i, path := range paths {
		if ctx.Err() != nil {
			return 130
		}
		body, e := os.ReadFile(path)
		if e != nil {
			return r.failure("设备报告读取失败")
		}
		doc, order, e := wireless.DecodeReviewJSON(body)
		if e != nil {
			return r.failure("设备报告结构错误")
		}
		name := reviewUnsafe.ReplaceAllString(strings.TrimSuffix(filepath.Base(path), ".json"), "_")
		if len(name) > 160 {
			name = name[:160]
		}
		inputs = append(inputs, input{path: path, sha: fmt.Sprintf("%x", sha256.Sum256(body)), name: fmt.Sprintf("%d-%s", i+1, name), doc: doc, advice: wireless.Recommendations(doc, order)})
	}
	if ctx.Err() != nil {
		return 130
	}
	root := flags["--output"]
	if root == "" {
		root = filepath.Join(batch, "reviews")
	}
	store, e := r.NewReport(root, r.ReportOptions)
	if e != nil {
		return r.failure("建议报告目录错误")
	}
	entries := []wireless.Record{}
	counts := wireless.Record{}
	scripts := false
	for _, arg := range args {
		if arg == "--scripts" {
			scripts = true
		}
		if arg == "--no-scripts" {
			scripts = false
		}
	}
	for _, in := range inputs {
		entry, e := store.WriteReview(ctx, in.doc, in.name, in.path, in.sha, in.advice, scripts)
		if e != nil {
			if ctx.Err() != nil {
				return 130
			}
			return r.failure("建议发布失败；已保存证据保留")
		}
		entries = append(entries, entry)
		for key, value := range in.advice["counts"].(wireless.Record) {
			n, _ := counts[key].(int)
			counts[key] = n + value.(int)
		}
	}
	checkpoint := wireless.Record{"status": "incomplete", "review_only": true, "devices": len(entries), "counts": counts, "source_batch": batch, "reports": entries}
	final := copyMap(checkpoint)
	final["status"] = "complete"
	e = store.Finalize(ctx, "review.json", checkpoint, final, func(ctx context.Context, p *report.Report) error {
		lines := []string{"WIRELESS 修复建议", "", "仅生成建议，未执行设备变更。candidate 需要确认条件；blocked 需要补齐参数；investigate 为排查或补采任务。", "", fmt.Sprintf("设备：%d；建议分类：%v", len(entries), counts), "", "未命中规则不代表网络健康；本次不推断跨设备 Fabric 归属或完整业务基线。", ""}
		for _, entry := range entries {
			device := entry["device"].(wireless.Record)
			lines = append(lines, fmt.Sprintf("%v（%v）：%v；报告：%v", device["name"], device["host"], entry["counts"], entry["report"]))
		}
		return p.Publish(ctx, "review.txt", []byte(strings.Join(lines, "\n")+"\n"))
	})
	if e != nil {
		if ctx.Err() != nil {
			return 130
		}
		return r.failure("建议摘要发布未确认；保留已有报告")
	}
	if ctx.Err() != nil {
		return 130
	}
	if _, e = fmt.Fprintf(r.Output, "建议已生成：%s/review.txt；%d 台；未执行设备变更。\n", store.Directory(), len(entries)); e != nil {
		return 2
	}
	return 0
}
