package collect

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/report"
)

type Executor func(context.Context, int, []wireless.Record, func(context.Context, wireless.Record) error) error

// Journal is the existing report publisher; no acquisition is replayed to repair it.
type Journal interface {
	WriteDevice(context.Context, report.Result, *wireless.Snapshot) error
	Finish(context.Context, []report.Result, report.FinishOptions) (wireless.Record, error)
	RecoveryFailed() bool
}

// Events render progress and final output. The execution owns the consequences of
// failed delivery, while the CLI adapter owns all presentation and formatting.
type Events struct {
	Started     func(wireless.Record)
	Completed   func(report.Result)
	Finalized   func(wireless.Record)
	OutputError func() any
	Deliver     func(wireless.Record) error
}

func (e Events) outputError() any {
	if e.OutputError != nil {
		return e.OutputError()
	}
	return nil
}
func (e Events) finalized(summary wireless.Record) {
	if e.Finalized != nil {
		e.Finalized(summary)
	}
}

type Options struct {
	Workers   int
	Requested []wireless.Record
	Execute   Executor
	Collect   func(context.Context, wireless.Record) (wireless.Snapshot, error)
	Journal   Journal
	Events    Events
}

// Result retains failure history across finalization attempts. Summary is the
// most recent confirmed publication, or nil when its last publication failed.
type Result struct {
	Summary                                                 wireless.Record
	Interrupted, Failed, FinalizationFailed, RecoveryFailed bool
}

func Run(ctx context.Context, o Options) Result {
	if o.Execute == nil || o.Collect == nil || o.Journal == nil {
		return Result{Failed: true, Interrupted: ctx.Err() != nil}
	}
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	results := []report.Result{}
	var firstReportError error
	e := o.Execute(batchCtx, o.Workers, o.Requested, func(taskCtx context.Context, device wireless.Record) error {
		start := time.Now()
		if o.Events.Started != nil {
			o.Events.Started(device)
		}
		collected, err := o.Collect(taskCtx, device)
		var snapshot *wireless.Snapshot
		result := report.Result{Device: device, Status: wireless.Failed, Dimensions: map[string]report.DimensionSummary{}}
		if err != nil {
			result.Error = "collection_failed"
		} else {
			snapshot = &collected
			result.Status = collected.Dimension(wireless.Collection).Status()
			for name, observation := range collected.Dimensions() {
				codes := []string{}
				seen := map[string]bool{}
				for _, issue := range observation.Issues() {
					code, _ := issue["code"].(string)
					if code != "" && !seen[code] {
						codes = append(codes, code)
						seen[code] = true
					}
				}
				result.Dimensions[string(name)] = report.DimensionSummary{Status: observation.Status(), Records: len(observation.Records()), Issues: codes}
			}
		}
		result.DurationMS = time.Since(start).Round(time.Millisecond).Milliseconds()
		mu.Lock()
		index := len(results)
		results = append(results, result)
		mu.Unlock()
		// Preserve every started result after sibling storage failure or cancellation.
		err = o.Journal.WriteDevice(context.WithoutCancel(taskCtx), result, snapshot)
		mu.Lock()
		if err == nil {
			results[index].ReportSaved = true
			result.ReportSaved = true
		} else if firstReportError == nil {
			firstReportError = err
		}
		mu.Unlock()
		if err != nil {
			cancel()
			return err
		}
		if o.Events.Completed != nil {
			o.Events.Completed(result)
		}
		return nil
	})
	// Executor returns only after callbacks and sessions have joined.
	outcome := Result{Interrupted: ctx.Err() != nil}
	slices.SortFunc(results, func(a, b report.Result) int {
		return cmp.Compare(StringValue(a.Device["host"]), StringValue(b.Device["host"]))
	})
	finish := report.FinishOptions{Interrupted: outcome.Interrupted, Requested: o.Requested, OutputError: o.Events.outputError()}
	if firstReportError != nil {
		finish.ReportError = reportError(firstReportError)
	} else if e != nil && !outcome.Interrupted {
		finish.ExecutionError = "execution_failed"
	}
	var finishErr error
	publish := func() {
		if finishErr != nil {
			finish.ReportError = reportError(finishErr)
		}
		// A failed publication has no confirmed summary. Do not return an older
		// complete summary after a failed downgrade, even if it was once durable.
		outcome.Summary, finishErr = o.Journal.Finish(context.Background(), results, finish)
		if finishErr != nil {
			outcome.Summary = nil
		}
		outcome.FinalizationFailed = outcome.FinalizationFailed || finishErr != nil
		// Report resets this flag per Finish; batch state retains every failure.
		outcome.RecoveryFailed = outcome.RecoveryFailed || o.Journal.RecoveryFailed()
	}
	finalize := func() {
		publish()
		// Cancellation is a one-way transition. If it arrived during Finish,
		// publish interrupted state once, through the same report finalizer.
		if ctx.Err() != nil && !finish.Interrupted {
			finish.Interrupted = true
			publish()
		}
		outcome.Interrupted = finish.Interrupted
	}
	finalize()
	if !outcome.FinalizationFailed {
		o.Events.finalized(outcome.Summary)
	}
	// Progress rendering may fail or observe a signal after publication.
	lateCancel := ctx.Err() != nil && !finish.Interrupted
	lateOutput := o.Events.outputError() != nil && finish.OutputError == nil
	if lateCancel || lateOutput {
		finish.Interrupted = finish.Interrupted || lateCancel
		if lateOutput {
			finish.OutputError = o.Events.outputError()
		}
		finalize()
	}
	if outcome.FinalizationFailed {
		outcome.Failed = true
		return outcome
	}
	if o.Events.Deliver != nil {
		if err := o.Events.Deliver(outcome.Summary); err != nil {
			finish.Interrupted = finish.Interrupted || ctx.Err() != nil
			finish.OutputError = wireless.Record{"host": nil, "error_type": "IOError"}
			finalize()
			outcome.Failed = true
			return outcome
		}
	}
	if ctx.Err() != nil && !finish.Interrupted {
		finish.Interrupted = true
		finalize()
	}
	outcome.Failed = outcome.FinalizationFailed || outcome.Interrupted || firstReportError != nil || e != nil
	return outcome
}
func StringValue(value any) string { text, _ := value.(string); return text }
func reportError(err error) any {
	if publication, ok := errors.AsType[*report.Error](err); ok {
		return publication.ToMap()
	}
	return "report_failed"
}
