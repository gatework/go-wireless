// Package report publishes private Wireless evidence through one durable writer.
package report

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gatework/go-net-connector/storage"
	wireless "github.com/gatework/go-wireless"
)

type DimensionSummary struct {
	Status  wireless.Status `json:"status"`
	Records int             `json:"records"`
	Issues  []string        `json:"issues"`
}
type Result struct {
	Device      wireless.Record             `json:"device"`
	Status      wireless.Status             `json:"status"`
	Dimensions  map[string]DimensionSummary `json:"dimensions"`
	Error       string                      `json:"error,omitempty"`
	DurationMS  int64                       `json:"duration_ms"`
	ReportSaved bool                        `json:"report_saved"`
}

func (r Result) ToMap() wireless.Record {
	d := map[string]any{}
	for n, v := range r.Dimensions {
		d[n] = v
	}
	m := wireless.Record{"device": r.Device, "status": r.Status, "dimensions": d, "duration_ms": r.DurationMS, "report_saved": r.ReportSaved}
	if r.Error != "" {
		m["error"] = r.Error
	}
	return m
}

// Error exposes safe publication metadata, never underlying error messages.
type Error struct {
	Relative, Phase    string
	Committed, Durable bool
	cancelErr          error
}

func (e *Error) Error() string {
	return fmt.Sprintf("report: %s at %s (committed=%t durable=%t)", e.Relative, e.Phase, e.Committed, e.Durable)
}
func (e *Error) GoString() string { return e.Error() }

// Is preserves cancellation identity without retaining arbitrary private causes.
func (e *Error) Is(target error) bool { return e.cancelErr != nil && errors.Is(e.cancelErr, target) }
func (e *Error) ToMap() wireless.Record {
	return wireless.Record{"relative": e.Relative, "phase": e.Phase, "committed": e.Committed, "durable": e.Durable}
}

type WriteFunc func(context.Context, string, []byte) (storage.Receipt, error)

// DeviceHook prepares recommendations before JSON publication and returns one
// post-publication artifact callback. It is the recommendation enrichment boundary.
type DeviceHook func(context.Context, wireless.Record) (Enrichment, error)
type Enrichment struct {
	Recommendations any
	Artifacts       func(context.Context, *Report, PublishedDevice) error
}
type PublishedDevice struct {
	Basename, Path string
	SHA256         string
	JSON           []byte
	Document       wireless.Record
}
type Options struct {
	Now        func() time.Time
	Write      WriteFunc
	DeviceHook DeviceHook
}
type Report struct {
	directory      string
	write          WriteFunc
	hook           DeviceHook
	mu             sync.Mutex
	recoveryFailed bool
}

func New(root string, o Options) (*Report, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("report: private root required")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, errors.New("report: cannot prepare root")
	}
	stat, e := os.Lstat(root)
	if e != nil || !stat.IsDir() || stat.Mode().Perm()&0077 != 0 {
		return nil, errors.New("report: root must be a real private directory (0700)")
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	stem := now().UTC().Format("2006-01-02_15-04-05")
	var directory string
	for i := 0; ; i++ {
		name := stem
		if i > 0 {
			name = fmt.Sprintf("%s_%d", stem, i)
		}
		directory = filepath.Join(root, name)
		e = os.Mkdir(directory, 0700)
		if errors.Is(e, os.ErrExist) {
			continue
		}
		if e != nil {
			return nil, errors.New("report: cannot create batch")
		}
		break
	}
	if e = os.Mkdir(filepath.Join(directory, "devices"), 0700); e != nil {
		return nil, errors.New("report: cannot create device directory")
	}
	write := o.Write
	if write == nil {
		write = storage.Write
	}
	return &Report{directory: directory, write: write, hook: o.DeviceHook}, nil
}
func (r *Report) Directory() string    { return r.directory }
func (r *Report) RecoveryFailed() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.recoveryFailed }
func (r *Report) recoveryFailure()     { r.mu.Lock(); r.recoveryFailed = true; r.mu.Unlock() }

// Publish is the only artifact writer; relative paths are confined to this batch.
func (r *Report) Publish(ctx context.Context, relative string, body []byte) error {
	if !safeRelative(relative) {
		return &Error{Relative: "[invalid]", Phase: "path"}
	}
	target := filepath.Join(r.directory, relative)
	// Hooks may create subdirectories but may never traverse symlink directories.
	parts := strings.Split(filepath.Dir(relative), string(filepath.Separator))
	parent := r.directory
	for _, p := range parts {
		if p == "." {
			continue
		}
		parent = filepath.Join(parent, p)
		if e := os.Mkdir(parent, 0700); e != nil && !errors.Is(e, os.ErrExist) {
			return &Error{Relative: relative, Phase: "directory"}
		}
		s, e := os.Lstat(parent)
		if e != nil || !s.IsDir() || s.Mode().Perm()&0077 != 0 {
			return &Error{Relative: relative, Phase: "directory"}
		}
	}
	receipt, e := r.write(ctx, target, body)
	if e == nil && !receipt.Durable() {
		e = errors.New("report: durability unconfirmed")
	}
	if e != nil {
		failure := &Error{Relative: relative, Phase: receipt.Phase, Committed: receipt.Committed(), Durable: receipt.Durable()}
		if errors.Is(e, context.Canceled) {
			failure.cancelErr = context.Canceled
		} else if errors.Is(e, context.DeadlineExceeded) {
			failure.cancelErr = context.DeadlineExceeded
		}
		return failure
	}
	return nil
}
func safeRelative(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator)) && !strings.ContainsAny(path, "\x00\r\n")
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func Basename(device wireless.Record) string {
	name, _ := device["name"].(string)
	name = unsafeName.ReplaceAllString(name, "_")
	if len(name) > 100 {
		name = name[:100]
	}
	host, _ := device["host"].(string)
	return name + "-" + strings.ReplaceAll(host, ":", "_")
}
func jsonBody(value any) ([]byte, error) {
	b, e := wireless.MarshalJSONIndent(value, "", "  ")
	if e != nil {
		return nil, &Error{Relative: "[document]", Phase: "encode"}
	}
	return append(b, '\n'), nil
}
func (r *Report) WriteDevice(ctx context.Context, result Result, snapshot *wireless.Snapshot) error {
	document := result.ToMap()
	delete(document, "report_saved")
	document["snapshot"] = nil
	if snapshot != nil {
		document["snapshot"] = snapshot.ToMap()
	}
	// Materialize exactly the Ruby wire document before enrichment. UseNumber
	// retains arbitrary controller integers, integral floats and negative zero.
	encoded, encodeErr := wireless.MarshalJSON(document)
	if encodeErr != nil {
		return &Error{Relative: "[document]", Phase: "encode"}
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if decoder.Decode(&document) != nil {
		return &Error{Relative: "[document]", Phase: "encode"}
	}
	enrichment := Enrichment{}
	var e error
	if r.hook != nil {
		enrichment, e = r.hook(ctx, document)
		if e != nil {
			return &Error{Relative: "recommendations", Phase: "prepare"}
		}
		document["recommendations"] = enrichment.Recommendations
	}
	body, e := jsonBody(document)
	if e != nil {
		return e
	}
	name := Basename(result.Device)
	relative := "devices/" + name + ".json"
	if e = r.Publish(ctx, relative, body); e != nil {
		return e
	}
	lines := []string{fmt.Sprintf("设备：%s（%s）", result.Device["name"], result.Device["host"]), "证据：" + string(result.Status), "此结果不代表 无线网络健康。"}
	keys := []string{}
	for k := range result.Dimensions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := result.Dimensions[k]
		lines = append(lines, fmt.Sprintf("%s：%s；记录 %d；诊断 %s", k, v.Status, v.Records, strings.Join(v.Issues, ", ")))
	}
	if result.Error != "" {
		lines = append(lines, "错误："+result.Error)
	}
	if r.hook != nil {
		lines = append(lines, "修复建议：recommendations/"+name+".txt；候选配置未执行。")
	}
	if e = r.Publish(ctx, "devices/"+name+".txt", []byte(strings.Join(lines, "\n")+"\n")); e != nil {
		return e
	}
	if enrichment.Artifacts != nil {
		if e = enrichment.Artifacts(ctx, r, PublishedDevice{Basename: name, Path: filepath.Join(r.directory, relative), SHA256: fmt.Sprintf("%x", sha256.Sum256(body)), JSON: append([]byte(nil), body...), Document: document}); e != nil {
			var publishError *Error
			if errors.As(e, &publishError) {
				return e
			}
			return &Error{Relative: "recommendations/" + name, Phase: "artifacts"}
		}
	}
	return nil
}

type FinishOptions struct {
	Interrupted                              bool
	Requested                                []wireless.Record
	ReportError, ExecutionError, OutputError any
}

func (r *Report) Finish(ctx context.Context, results []Result, o FinishOptions) (wireless.Record, error) {
	r.mu.Lock()
	r.recoveryFailed = false
	r.mu.Unlock()
	complete := len(results) > 0 && o.ReportError == nil && o.ExecutionError == nil && o.OutputError == nil
	completed := map[string]bool{}
	counts := map[string]int{}
	devices := []any{}
	for _, v := range results {
		host, _ := v.Device["host"].(string)
		completed[host] = true
		counts[string(v.Status)]++
		complete = complete && v.Status == wireless.Complete && v.ReportSaved
		devices = append(devices, v.ToMap())
	}
	pending := []wireless.Record{}
	for _, d := range o.Requested {
		h, _ := d["host"].(string)
		if !completed[h] {
			pending = append(pending, d)
		}
	}
	complete = complete && len(pending) == 0
	status := "incomplete"
	if complete {
		status = "complete"
	}
	if o.Interrupted {
		status = "interrupted"
	}
	summary := wireless.Record{"status": status, "total": max(len(results), len(o.Requested)), "completed": len(results), "pending": pending, "counts": counts, "devices": devices, "directory": r.directory, "evidence_only": true}
	for key, value := range map[string]any{"report_error": o.ReportError, "execution_error": o.ExecutionError, "output_error": o.OutputError} {
		if value != nil {
			summary[key] = value
		}
	}
	checkpoint := wireless.Record{}
	for k, v := range summary {
		checkpoint[k] = v
	}
	checkpoint["status"] = "incomplete"
	if o.Interrupted {
		checkpoint["status"] = "interrupted"
	}
	if o.ReportError == nil {
		checkpoint["report_error"] = "report_finalization_unconfirmed"
	}
	lines := []string{fmt.Sprintf("WIRELESS 证据：%s；完成 %d 台", status, len(results)), "证据完整性不代表 无线网络健康。", "批次报告状态：参见 summary.json；文本中的证据状态不确认报告发布完成。"}
	for _, field := range []struct {
		name  string
		value any
	}{{"报告错误", o.ReportError}, {"执行错误", o.ExecutionError}, {"进度输出错误", o.OutputError}} {
		if field.value != nil {
			lines = append(lines, fmt.Sprintf("%s：%v", field.name, field.value))
		}
	}
	if r.hook != nil {
		lines = append(lines, "逐设备修复建议：recommendations/；所有候选配置仅供审阅，未执行。")
	}
	for _, v := range results {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t保存确认=%t\t%s", v.Device["host"], v.Device["name"], v.Status, v.ReportSaved, v.Error))
	}
	for _, v := range pending {
		lines = append(lines, fmt.Sprintf("%s\t%s\t未采集", v["host"], v["name"]))
	}
	err := r.Finalize(ctx, "summary.json", checkpoint, summary, func(ctx context.Context, publisher *Report) error {
		return publisher.Publish(ctx, "summary.txt", []byte(strings.Join(lines, "\n")+"\n"))
	})
	if err != nil {
		return nil, err
	}
	return summary, nil
}

// Finalize publishes an incomplete/interrupted checkpoint, artifacts, then the
// final document at one relative path. Review and collect share this lifecycle.
// A committed final write failure restores the checkpoint without cancellation.
func (r *Report) Finalize(ctx context.Context, relative string, checkpoint, final wireless.Record, artifacts func(context.Context, *Report) error) error {
	r.mu.Lock()
	r.recoveryFailed = false
	r.mu.Unlock()
	if !safeRelative(relative) {
		return &Error{Relative: "[invalid]", Phase: "path"}
	}
	if checkpoint["status"] != "incomplete" && checkpoint["status"] != "interrupted" {
		return &Error{Relative: relative, Phase: "checkpoint"}
	}
	checkpointBody, e := jsonBody(checkpoint)
	if e != nil {
		return e
	}
	if e = r.Publish(ctx, relative, checkpointBody); e != nil {
		r.invalidateComplete(relative)
		return e
	}
	if artifacts != nil {
		if e = artifacts(ctx, r); e != nil {
			return e
		}
	}
	body, e := jsonBody(final)
	if e != nil {
		return e
	}
	if e = r.Publish(ctx, relative, body); e != nil {
		var pe *Error
		if errors.Is(e, context.Canceled) {
			cancelledCheckpoint := wireless.Record{}
			for key, value := range checkpoint {
				cancelledCheckpoint[key] = value
			}
			cancelledCheckpoint["status"] = "interrupted"
			cancelledBody, encodeErr := jsonBody(cancelledCheckpoint)
			if encodeErr == nil {
				r.restore(relative, cancelledBody)
			} else {
				r.recoveryFailure()
				r.discard(relative)
			}
		} else if errors.As(e, &pe) && pe.Committed {
			r.restore(relative, checkpointBody)
		}
		return e
	}
	return nil
}
func (r *Report) restore(relative string, body []byte) {
	if e := r.Publish(context.Background(), relative, body); e != nil {
		r.recoveryFailure()
		var pe *Error
		if !errors.As(e, &pe) || !pe.Committed {
			r.discard(relative)
		}
	}
}
func (r *Report) discard(relative string) {
	if e := os.Remove(filepath.Join(r.directory, relative)); e != nil && !errors.Is(e, os.ErrNotExist) {
		r.recoveryFailure()
	}
}
func (r *Report) invalidateComplete(relative string) {
	b, e := os.ReadFile(filepath.Join(r.directory, relative))
	if errors.Is(e, os.ErrNotExist) {
		return
	}
	var v map[string]any
	if e != nil || json.Unmarshal(b, &v) != nil {
		r.recoveryFailure()
		return
	}
	if v["status"] == "complete" {
		r.discard(relative)
	}
}

func (r *Report) WriteExecution(ctx context.Context, state any) error {
	b, e := jsonBody(state)
	if e != nil {
		return e
	}
	return r.Publish(ctx, "execution.json", b)
}

// FinalizeExecution keeps an independently durable checkpoint before replacing
// execution.json with verified status. Failed publication/recovery must not erase
// intent or present a committed-but-unconfirmed final file as durable success.
func (r *Report) FinalizeExecution(ctx context.Context, final wireless.Record) error {
	if final["status"] != "configuration_verified" || final["readback_verified"] != true {
		return &Error{Relative: "execution.json", Phase: "checkpoint"}
	}
	checkpoint := cloneRecord(final)
	checkpoint["status"] = "uncertain"
	checkpoint["report_error"] = "execution_finalization_unconfirmed"
	checkpointBody, e := jsonBody(checkpoint)
	if e != nil {
		return e
	}
	if e = r.Publish(ctx, "execution-checkpoint.json", checkpointBody); e != nil {
		return e
	}
	e = r.WriteExecution(ctx, final)
	if e == nil {
		e = ctx.Err()
	}
	if e != nil {
		var pe *Error
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || errors.As(e, &pe) && pe.Committed {
			if restoreErr := r.Publish(context.Background(), "execution.json", checkpointBody); restoreErr != nil {
				r.recoveryFailure()
				var restored *Error
				if !errors.As(restoreErr, &restored) || !restored.Committed {
					r.discard("execution.json")
				}
			}
		}
		return e
	}
	return nil
}
