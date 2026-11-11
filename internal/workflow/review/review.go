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

// Options configures New.
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

// Result is one device's collection result in the batch summary.
type Result struct {
	Directory string
	Devices   int
}

// Error reports the review stage that failed; it never carries device text.
type Error struct{ Stage string }

func (e *Error) Error() string { return "wireless review: " + e.Stage }

var reviewUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// Run runs one command line and returns its exit code.
func Run(ctx context.Context, o Options) (Result, error) {
	batch, e := filepath.Abs(o.Batch)
	if e != nil {
		return Result{}, &Error{Stage: "batch_path"}
	}
	paths, e := filepath.Glob(filepath.Join(batch, "devices", "*.json"))
	if e != nil || len(paths) == 0 {
		return Result{}, &Error{Stage: "no_reports"}
	}
	assessor, comparison, e := prepare(ctx, o)
	if e != nil {
		return Result{}, e
	}
	sources, controllers, e := readSources(ctx, paths)
	if e != nil {
		return Result{}, e
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
		return Result{}, failed(ctx, "assessment")
	}
	entries, counts, e := writeControllers(ctx, store, sources, controllers, assessment, o.Scripts)
	if e != nil {
		return Result{}, e
	}
	if e = publishBatch(ctx, store, assessment, controllers); e != nil {
		return Result{}, e
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
		return Result{}, failed(ctx, "finalize")
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return Result{Directory: store.Directory(), Devices: len(entries)}, nil
}

// failed reports a stage failure, or the cancellation that caused it.
func failed(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &Error{Stage: stage}
}

// prepare builds the assessor from the optional rules policy and loads the
// optional baseline and previous batch.
func prepare(ctx context.Context, o Options) (*rules.Assessor, rules.Inputs, error) {
	policy := assess.Policy{}
	if o.RulesPolicy != "" {
		body, e := input.Document(ctx, o.RulesPolicy)
		if e != nil {
			return nil, rules.Inputs{}, &Error{Stage: "rules_policy"}
		}
		if policy, e = assess.DecodePolicy(body); e != nil {
			return nil, rules.Inputs{}, &Error{Stage: "rules_policy"}
		}
	}
	assessor, e := rules.NewAssessor(policy, nil)
	if e != nil {
		return nil, rules.Inputs{}, &Error{Stage: "rules_policy"}
	}
	comparison := rules.Inputs{}
	if o.Baseline != "" {
		body, e := input.Document(ctx, o.Baseline)
		if e != nil {
			return nil, rules.Inputs{}, &Error{Stage: "baseline"}
		}
		b, e := wlan.DecodeBaseline(body)
		if e != nil {
			return nil, rules.Inputs{}, &Error{Stage: "baseline"}
		}
		comparison.Baseline = &b
	}
	if o.Previous != "" {
		previous, e := readControllers(ctx, o.Previous)
		if e != nil {
			return nil, rules.Inputs{}, e
		}
		comparison.Previous = previous
	}
	return assessor, comparison, nil
}

// sourceReport is one collected device report the review reads.
type sourceReport struct {
	path, sha, name string
	doc             wireless.Record
}

// readSources reads and validates every device report before any output is
// created.
func readSources(ctx context.Context, paths []string) ([]sourceReport, []*wlan.Controller, error) {
	controllers := []*wlan.Controller{}
	sources := []sourceReport{}
	for i, path := range paths {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		body, e := input.Document(ctx, path)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if e != nil {
			return nil, nil, &Error{Stage: "read"}
		}
		doc, e := wireless.DecodeJSONDocument(body)
		if e != nil {
			return nil, nil, &Error{Stage: "document"}
		}
		controller, e := wlan.DecodeDocument(body)
		if e != nil {
			return nil, nil, &Error{Stage: "document"}
		}
		controllers = append(controllers, controller)
		name := reviewUnsafe.ReplaceAllString(strings.TrimSuffix(filepath.Base(path), ".json"), "_")
		if len(name) > 160 {
			name = name[:160]
		}
		sources = append(sources, sourceReport{path: path, sha: fmt.Sprintf("%x", sha256.Sum256(body)), name: fmt.Sprintf("%d-%s", i+1, name), doc: doc})
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return sources, controllers, nil
}

// writeControllers publishes each controller's review output and totals the
// severity counts of all entries.
func writeControllers(ctx context.Context, store *report.Report, sources []sourceReport, controllers []*wlan.Controller, assessment rules.Report, scripts bool) ([]wireless.Record, wireless.Record, error) {
	entries := []wireless.Record{}
	counts := wireless.Record{}
	for i, in := range sources {
		entry, e := store.WriteAssessment(ctx, in.doc, controllers[i], assessment.Controllers[i], in.name, in.path, in.sha, scripts)
		if e != nil {
			return nil, nil, failed(ctx, "publish")
		}
		entries = append(entries, entry)
		entryCounts, _ := entry["counts"].(wireless.Record)
		for key, value := range entryCounts {
			n, _ := counts[key].(int)
			v, _ := value.(int)
			counts[key] = n + v
		}
	}
	return entries, counts, nil
}

// publishBatch publishes the batch assessment and the baseline draft.
func publishBatch(ctx context.Context, store *report.Report, assessment rules.Report, controllers []*wlan.Controller) error {
	assessmentBody, e := json.MarshalIndent(assessment, "", "  ")
	if e != nil {
		return &Error{Stage: "assessment"}
	}
	if e = store.Publish(ctx, "assessment.json", append(assessmentBody, '\n')); e != nil {
		return failed(ctx, "publish")
	}
	draft, e := rules.DeriveBaseline(controllers).Encode()
	if e != nil {
		return &Error{Stage: "assessment"}
	}
	draft = append([]byte("# 无线基线草案：由本批次当前状态生成，仅供审核；确认意图并删减后再用于 review --baseline。\n"), draft...)
	if e = store.Publish(ctx, "baseline-draft.yaml", draft); e != nil {
		return failed(ctx, "publish")
	}
	return nil
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
