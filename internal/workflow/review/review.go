package review

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/input"
	"github.com/gatework/go-wireless/report"
	domain "github.com/gatework/go-wireless/review"
	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/wlan"

	"github.com/gatework/go-netops/assess"
)

type Options struct {
	Batch, Output string
	// RulesPolicy is an optional YAML file tuning the schema 3 assessment.
	RulesPolicy   string
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
	policy := assess.Policy{}
	if o.RulesPolicy != "" {
		body, e := input.Document(ctx, o.RulesPolicy)
		if e != nil {
			return Result{}, &Error{Stage: "rules_policy"}
		}
		if policy, e = assess.DecodePolicy(body); e != nil {
			return Result{}, &Error{Stage: "rules_policy"}
		}
	}
	assessor, e := rules.NewAssessor(policy, nil)
	if e != nil {
		return Result{}, &Error{Stage: "rules_policy"}
	}
	controllers := []*wlan.Controller{}
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
		controller, e := wlan.DecodeDocument(body)
		if e != nil {
			return Result{}, &Error{Stage: "document"}
		}
		controllers = append(controllers, controller)
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
	assessment, e := assessor.Assess(ctx, controllers)
	if e != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, &Error{Stage: "assessment"}
	}
	assessmentBody, e := json.MarshalIndent(assessment, "", "  ")
	if e != nil {
		return Result{}, &Error{Stage: "assessment"}
	}
	if e = store.Publish(ctx, "assessment.json", append(assessmentBody, '\n')); e != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, &Error{Stage: "publish"}
	}
	riskCounts := wireless.Record{}
	for severity, n := range assessment.Counts {
		riskCounts[severity.String()] = n
	}
	checkpoint := wireless.Record{"assessment": "assessment.json", "risk_counts": riskCounts, "risk_highest": assessment.Highest.String(), "assessment_incomplete": assessment.Incomplete, "status": "incomplete", "review_only": true, "devices": len(entries), "counts": counts, "source_batch": batch, "reports": entries}
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
