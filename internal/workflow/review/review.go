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
	"github.com/gatework/go-wireless/rules"
	"github.com/gatework/go-wireless/wlan"

	"github.com/gatework/go-netops/assess"
)

type Options struct {
	Batch, Output string
	// RulesPolicy is an optional YAML file tuning the schema 3 assessment.
	RulesPolicy string
	// Baseline is an optional frozen baseline YAML file.
	Baseline string
	// Previous is an optional earlier batch directory to compare with.
	Previous      string
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
	comparison := rules.Inputs{}
	if o.Baseline != "" {
		body, e := input.Document(ctx, o.Baseline)
		if e != nil {
			return Result{}, &Error{Stage: "baseline"}
		}
		b, e := wlan.DecodeBaseline(body)
		if e != nil {
			return Result{}, &Error{Stage: "baseline"}
		}
		comparison.Baseline = &b
	}
	if o.Previous != "" {
		previous, e := readControllers(ctx, o.Previous)
		if e != nil {
			return Result{}, e
		}
		comparison.Previous = previous
	}
	controllers := []*wlan.Controller{}
	type sourceReport struct {
		path, sha, name string
		doc             wireless.Record
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
		doc, _, e := wireless.DecodeJSONDocument(body)
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
		inputs = append(inputs, sourceReport{path: path, sha: fmt.Sprintf("%x", sha256.Sum256(body)), name: fmt.Sprintf("%d-%s", i+1, name), doc: doc})
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
	assessment, e := assessor.AssessWith(ctx, controllers, comparison)
	if e != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, &Error{Stage: "assessment"}
	}
	entries := []wireless.Record{}
	counts := wireless.Record{}
	for i, in := range inputs {
		entry, e := store.WriteAssessment(ctx, in.doc, controllers[i], assessment.Controllers[i], in.name, in.path, in.sha, o.Scripts)
		if e != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			return Result{}, &Error{Stage: "publish"}
		}
		entries = append(entries, entry)
		for key, value := range entry["counts"].(wireless.Record) {
			n, _ := counts[key].(int)
			counts[key] = n + value.(int)
		}
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
	draft, e := rules.DeriveBaseline(controllers).Encode()
	if e != nil {
		return Result{}, &Error{Stage: "assessment"}
	}
	draft = append([]byte("# 无线基线草案：由本批次当前状态生成，仅供审核；确认意图并删减后再用于 review --baseline。\n"), draft...)
	if e = store.Publish(ctx, "baseline-draft.yaml", draft); e != nil {
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

// readControllers decodes the device reports of an earlier batch.
func readControllers(ctx context.Context, batch string) ([]*wlan.Controller, error) {
	dir, e := filepath.Abs(batch)
	if e != nil {
		return nil, &Error{Stage: "previous"}
	}
	paths, e := filepath.Glob(filepath.Join(dir, "devices", "*.json"))
	if e != nil || len(paths) == 0 {
		return nil, &Error{Stage: "previous"}
	}
	out := []*wlan.Controller{}
	for _, path := range paths {
		body, e := input.Document(ctx, path)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &Error{Stage: "previous"}
		}
		c, e := wlan.DecodeDocument(body)
		if e != nil {
			return nil, &Error{Stage: "previous"}
		}
		out = append(out, c)
	}
	return out, nil
}
