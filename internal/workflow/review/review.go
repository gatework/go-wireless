package review

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/input"
	"github.com/gatework/go-wireless/report"
	domain "github.com/gatework/go-wireless/review"
)

type Options struct {
	Batch, Output string
	Scripts       bool
	NewReport     func(string, report.Options) (*report.Report, error)
	ReportOptions report.Options
}
type Result struct {
	Directory string
	Devices   int
}
type Error struct{ Stage string }

func (e *Error) Error() string { return "wireless review: " + e.Stage }

var reviewUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func Run(ctx context.Context, o Options) (Result, error) {
	batch, e := filepath.Abs(o.Batch)
	if e != nil {
		return Result{}, &Error{Stage: "batch_path"}
	}
	paths, e := filepath.Glob(filepath.Join(batch, "devices", "*.json"))
	if e != nil || len(paths) == 0 {
		return Result{}, &Error{Stage: "no_reports"}
	}
	type sourceReport struct {
		path, sha, name string
		doc, advice     wireless.Record
	}
	inputs := []sourceReport{}
	for i, path := range paths {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		body, e := input.Document(ctx, path)
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if e != nil {
			return Result{}, &Error{Stage: "read"}
		}
		doc, order, e := domain.DecodeReviewJSON(body)
		if e != nil {
			return Result{}, &Error{Stage: "document"}
		}
		name := reviewUnsafe.ReplaceAllString(strings.TrimSuffix(filepath.Base(path), ".json"), "_")
		if len(name) > 160 {
			name = name[:160]
		}
		inputs = append(inputs, sourceReport{path: path, sha: fmt.Sprintf("%x", sha256.Sum256(body)), name: fmt.Sprintf("%d-%s", i+1, name), doc: doc, advice: domain.Recommendations(doc, order)})
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	root := o.Output
	if root == "" {
		root = filepath.Join(batch, "reviews")
	}
	store, e := o.NewReport(root, o.ReportOptions)
	if e != nil {
		return Result{}, &Error{Stage: "directory"}
	}
	entries := []wireless.Record{}
	counts := wireless.Record{}
	for _, in := range inputs {
		entry, e := store.WriteReview(ctx, in.doc, in.name, in.path, in.sha, in.advice, o.Scripts)
		if e != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			return Result{}, &Error{Stage: "publish"}
		}
		entries = append(entries, entry)
		for key, value := range in.advice["counts"].(wireless.Record) {
			n, _ := counts[key].(int)
			counts[key] = n + value.(int)
		}
	}
	checkpoint := wireless.Record{"status": "incomplete", "review_only": true, "devices": len(entries), "counts": counts, "source_batch": batch, "reports": entries}
	final := wireless.CloneRecord(checkpoint)
	final["status"] = "complete"
	e = store.Finalize(ctx, "review.json", checkpoint, final, func(ctx context.Context, p *report.Report) error { return p.PublishReviewSummary(ctx, entries, counts) })
	if e != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, &Error{Stage: "finalize"}
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return Result{Directory: store.Directory(), Devices: len(entries)}, nil
}
