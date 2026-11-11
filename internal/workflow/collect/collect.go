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

// Executor runs one function per device with bounded concurrency.
type Executor func(context.Context, int, []wireless.Record, func(context.Context, wireless.Record) error) error

// Journal is the existing report publisher; no acquisition is replayed to repair it.
type Journal interface {
	WriteDevice(ctx context.Context, result report.Result, snapshot *wireless.Snapshot) error
	Finish(ctx context.Context, results []report.Result, options report.FinishOptions) (wireless.Record, error)
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

// Options configures New.
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

// Run runs one command line and returns its exit code.
func Run(ctx context.Context, o Options) Result {
	if o.Execute == nil || o.Collect == nil || o.Journal == nil {
		return Result{Failed: true, Interrupted: ctx.Err() != nil}
	}
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &collectRun{ctx: ctx, o: o, cancel: cancel, results: []report.Result{}}
	e := o.Execute(batchCtx, o.Workers, o.Requested, r.device)
	// Executor returns only after callbacks and sessions have joined.
	r.summarize(e)
	return r.settle(e)
}

// collectRun is one execution of Run.
type collectRun struct {
	ctx    context.Context //nolint:containedctx // the caller's context, scoped to one Run call
	o      Options
	cancel context.CancelFunc

	mu               sync.Mutex // guards results and firstReportError while devices run
	results          []report.Result
	firstReportError error

	outcome   Result
	finish    report.FinishOptions
	finishErr error
}

// device collects one device and persists its result. Every started result
// is preserved after a sibling storage failure or cancellation; a storage
// failure cancels the batch.
func (r *collectRun) device(taskCtx context.Context, device wireless.Record) error {
	start := time.Now()
	if r.o.Events.Started != nil {
		r.o.Events.Started(device)
	}
	collected, err := r.o.Collect(taskCtx, device)
	var snapshot *wireless.Snapshot
	result := report.Result{Device: device, Status: wireless.Failed, Dimensions: map[string]report.DimensionSummary{}}
	if err != nil {
		result.Error = "collection_failed"
	} else {
		snapshot = &collected
		result.Status = collected.Dimension(wireless.Collection).Status()
		for name, observation := range collected.Dimensions() {
			result.Dimensions[string(name)] = report.DimensionSummary{Status: observation.Status(), Records: len(observation.Records()), Issues: issueCodes(observation)}
		}
	}
	result.DurationMS = time.Since(start).Round(time.Millisecond).Milliseconds()
	r.mu.Lock()
	index := len(r.results)
	r.results = append(r.results, result)
	r.mu.Unlock()
	err = r.o.Journal.WriteDevice(context.WithoutCancel(taskCtx), result, snapshot)
	r.mu.Lock()
	if err == nil {
		r.results[index].ReportSaved = true
		result.ReportSaved = true
	} else if r.firstReportError == nil {
		r.firstReportError = err
	}
	r.mu.Unlock()
	if err != nil {
		r.cancel()
		return err
	}
	if r.o.Events.Completed != nil {
		r.o.Events.Completed(result)
	}
	return nil
}

// issueCodes lists an observation's issue codes once each, in order.
func issueCodes(observation wireless.Observation) []string {
	codes := []string{}
	seen := map[string]bool{}
	for _, issue := range observation.Issues() {
		code, _ := issue["code"].(string)
		if code != "" && !seen[code] {
			codes = append(codes, code)
			seen[code] = true
		}
	}
	return codes
}

// summarize orders the results and prepares the finish options.
func (r *collectRun) summarize(executionErr error) {
	r.outcome = Result{Interrupted: r.ctx.Err() != nil}
	slices.SortFunc(r.results, func(a, b report.Result) int {
		return cmp.Compare(StringValue(a.Device["host"]), StringValue(b.Device["host"]))
	})
	r.finish = report.FinishOptions{Interrupted: r.outcome.Interrupted, Requested: r.o.Requested, OutputError: r.o.Events.outputError()}
	if r.firstReportError != nil {
		r.finish.ReportError = reportError(r.firstReportError)
	} else if executionErr != nil && !r.outcome.Interrupted {
		r.finish.ExecutionError = "execution_failed"
	}
}

// publish finishes the report once, recording a previous finish failure.
func (r *collectRun) publish() {
	if r.finishErr != nil {
		r.finish.ReportError = reportError(r.finishErr)
	}
	// A failed publication has no confirmed summary. Do not return an older
	// complete summary after a failed downgrade, even if it was once durable.
	r.outcome.Summary, r.finishErr = r.o.Journal.Finish(context.Background(), r.results, r.finish)
	if r.finishErr != nil {
		r.outcome.Summary = nil
	}
	r.outcome.FinalizationFailed = r.outcome.FinalizationFailed || r.finishErr != nil
	// Report resets this flag per Finish; batch state retains every failure.
	r.outcome.RecoveryFailed = r.outcome.RecoveryFailed || r.o.Journal.RecoveryFailed()
}

// finalize publishes; cancellation is a one-way transition, so if it arrived
// during Finish the interrupted state is published once more.
func (r *collectRun) finalize() {
	r.publish()
	if r.ctx.Err() != nil && !r.finish.Interrupted {
		r.finish.Interrupted = true
		r.publish()
	}
	r.outcome.Interrupted = r.finish.Interrupted
}

// settle publishes the summary, republishes it after late cancellation or
// output failure, delivers it and returns the outcome.
func (r *collectRun) settle(executionErr error) Result {
	r.finalize()
	if !r.outcome.FinalizationFailed {
		r.o.Events.finalized(r.outcome.Summary)
	}
	// Progress rendering may fail or observe a signal after publication.
	lateCancel := r.ctx.Err() != nil && !r.finish.Interrupted
	lateOutput := r.o.Events.outputError() != nil && r.finish.OutputError == nil
	if lateCancel || lateOutput {
		r.finish.Interrupted = r.finish.Interrupted || lateCancel
		if lateOutput {
			r.finish.OutputError = r.o.Events.outputError()
		}
		r.finalize()
	}
	if r.outcome.FinalizationFailed {
		r.outcome.Failed = true
		return r.outcome
	}
	if r.o.Events.Deliver != nil {
		if err := r.o.Events.Deliver(r.outcome.Summary); err != nil {
			r.finish.Interrupted = r.finish.Interrupted || r.ctx.Err() != nil
			r.finish.OutputError = wireless.Record{"host": nil, "error_type": "IOError"}
			r.finalize()
			r.outcome.Failed = true
			return r.outcome
		}
	}
	if r.ctx.Err() != nil && !r.finish.Interrupted {
		r.finish.Interrupted = true
		r.finalize()
	}
	r.outcome.Failed = r.outcome.FinalizationFailed || r.outcome.Interrupted || r.firstReportError != nil || executionErr != nil
	return r.outcome
}

// StringValue returns value when it is a string, otherwise "".
func StringValue(value any) string { text, _ := value.(string); return text }
func reportError(err error) any {
	if publication, ok := errors.AsType[*report.Error](err); ok {
		return publication.ToMap()
	}
	return "report_failed"
}
