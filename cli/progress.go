package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/report"
)

type progress struct {
	mu                                   sync.Mutex
	out                                  io.Writer
	enabled, verbose, tty                bool
	failure                              wireless.Record
	selected, completed, active, skipped int
}

func newProgress(out io.Writer, enabled, verbose, tty bool) *progress {
	return &progress{out: out, enabled: enabled, verbose: verbose, tty: tty}
}
func (p *progress) write(text, host string) {
	if !p.enabled || p.failure != nil {
		return
	}
	n, e := io.WriteString(p.out, text)
	if e != nil || n != len(text) {
		var identity any
		if host != "" {
			identity = host
		}
		p.failure = wireless.Record{"host": identity, "error_type": "IOError"}
	}
}
func (p *progress) notice(text string) { p.mu.Lock(); defer p.mu.Unlock(); p.write(text+"\n", "") }
func (p *progress) outputError() any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure == nil {
		return nil
	}
	return copyMap(p.failure)
}
func (p *progress) plan(total, selected, workers int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selected = selected
	p.skipped = total - selected
	p.write(fmt.Sprintf("WIRELESS 只读采集：清单 %d 台；本次 %d 台；跳过 %d 台；并发 %d\n", total, selected, total-selected, workers), "")
}
func (p *progress) refresh() {
	if p.tty {
		percentage := 0
		if p.selected > 0 {
			percentage = p.completed * 100 / p.selected
		}
		p.write(fmt.Sprintf("\r\x1b[2K%d/%d %d%%；登录 %d", p.completed, p.selected, percentage, p.active), "")
	}
}
func (p *progress) start(d wireless.Record) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active++
	p.refresh()
	if p.verbose && !p.tty {
		p.write(safe(stringValue(d["host"]))+"  开始采集\n", stringValue(d["host"]))
	}
}
func (p *progress) result(r report.Result) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
	p.completed++
	p.refresh()
	if r.Status != wireless.Complete || !r.ReportSaved || p.verbose {
		message := "采集完成；报告已保存"
		if !r.ReportSaved {
			message = "未完成（" + resultDiagnostic(r) + "）"
		} else if r.Status != wireless.Complete {
			message = "未完成（" + resultDiagnostic(r) + "）"
			if r.Status == wireless.Partial {
				message = "部分成功（" + resultDiagnostic(r) + "）"
			}
		}
		if p.tty {
			p.write("\n", "")
		}
		p.write(fmt.Sprintf("%s  %s\n", r.Device["host"], message), stringValue(r.Device["host"]))
	}
}
func (p *progress) event(event connector.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.verbose {
		return
	}
	p.write(fmt.Sprintf("%s  事件 %s %s\n", safe(event.Host), safe(event.Name), safe(event.Status)), event.Host)
}
func (p *progress) finish(summary wireless.Record) {
	p.mu.Lock()
	defer p.mu.Unlock()
	success, partial, failed := 0, 0, 0
	for _, item := range summary["devices"].([]any) {
		d := item.(map[string]any)
		switch {
		case d["report_saved"] != true:
			failed++
		case d["status"] == wireless.Complete:
			success++
		case d["status"] == wireless.Partial:
			partial++
		default:
			failed++
		}
	}
	if p.tty {
		p.write("\n", "")
	}
	p.write(fmt.Sprintf("批次结束：成功 %d，部分成功 %d，失败 %d，跳过 %d\n", success, partial, failed, p.skipped), "")
	if partial+failed > 0 {
		failures := map[string]int{}
		for _, item := range summary["devices"].([]any) {
			d := item.(map[string]any)
			code := "collection_incomplete"
			if d["report_saved"] != true {
				code = "report_not_saved"
			} else if d["status"] == wireless.Complete {
				continue
			} else if value, ok := d["error"].(string); ok && value != "" {
				code = safe(value)
			}
			failures[code]++
		}
		codes := []string{}
		for code := range failures {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		parts := []string{}
		for _, code := range codes {
			parts = append(parts, fmt.Sprintf("%s %d", code, failures[code]))
		}
		p.write("未完成分类："+strings.Join(parts, "，")+"\n", "")
	}
	if pending := len(summary["pending"].([]wireless.Record)); pending > 0 {
		p.write(fmt.Sprintf("未采集 %d 台；详见批次报告\n", pending), "")
	}
	p.write("证据完整性不代表网络健康，审查结果见报告。\n报告："+stringValue(summary["directory"])+"/summary.json\n文本报告："+stringValue(summary["directory"])+"/summary.txt\n", "")
}

func resultDiagnostic(r report.Result) string {
	if !r.ReportSaved {
		return "report_not_saved"
	}
	if r.Error != "" {
		return safe(r.Error)
	}
	if c, ok := r.Dimensions["collection"]; ok && c.Status != wireless.Complete && len(c.Issues) > 0 {
		return safe(c.Issues[0])
	}
	keys := []string{}
	for k := range r.Dimensions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := r.Dimensions[k]
		if v.Status != wireless.Complete && len(v.Issues) > 0 {
			return safe(v.Issues[0])
		}
	}
	return "collection_incomplete"
}
