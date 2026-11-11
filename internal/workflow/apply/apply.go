package apply

import (
	"context"
	"errors"
	"sync"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
)

// Opener lends one session and its operation context, then returns operation and
// cleanup errors separately. Run admits at most one callback and joins it before
// publishing the final result, even if an opener returns while it is running.
type Opener func(context.Context, func(context.Context, change.ApplySession) error) (error, error)

// Journal durably records intent before sending and acknowledges completed work.
// Each call receives a detached projection that the journal may retain.
// FinalizeExecution must preserve a recoverable checkpoint if publication fails.
type Journal interface {
	WriteExecution(ctx context.Context, state wireless.Record) error
	FinalizeExecution(ctx context.Context, state wireless.Record) error
}

// publicationStatus is the journal capability needed to retain uncertain intent.
type publicationStatus interface {
	error
	PublicationCommitted() bool
}

// Result contains safe terminal diagnostics if execution fails. A failed journal
// publication leaves Summary as terminal-only evidence, not a durable receipt.
type Result struct {
	Summary           wireless.Record
	ReportUnconfirmed bool
	Interrupted       bool
}

type runError struct{ cause error }

func (*runError) Error() string      { return "wireless: apply failed" }
func (e *runError) Unwrap() error    { return e.cause }
func (e *runError) GoString() string { return e.Error() }

// Run performs a confirmed plan once. It never retries, reconnects, rolls back,
// or saves configuration. planSHA256 identifies the exact confirmed file bytes.
func Run(ctx context.Context, plan change.Plan, planSHA256 string, open Opener, journal Journal) (Result, error) {
	if plan.Host() == "" || open == nil || journal == nil {
		return Result{}, &runError{change.ErrInvalid}
	}
	r := &applyRun{ctx: ctx, plan: plan, journal: journal, state: newExecution(plan, planSHA256)}
	if err := journal.WriteExecution(ctx, r.state.record()); err != nil {
		return r.fail(err, nil)
	}
	operationErr, cleanupErr := ownedOpen(ctx, open, r.operation)
	if operationErr != nil || cleanupErr != nil || ctx.Err() != nil {
		return r.fail(operationErr, cleanupErr)
	}
	r.state.status = configurationVerified
	if err := journal.FinalizeExecution(ctx, r.state.record()); err != nil {
		return r.fail(err, nil)
	}
	if ctx.Err() != nil {
		return r.fail(ctx.Err(), nil)
	}
	return Result{}, nil
}

// applyRun is one execution of Run.
type applyRun struct {
	ctx     context.Context //nolint:containedctx // the caller's context, scoped to one Run call
	plan    change.Plan
	journal Journal
	state   *execution
}

// interrupted reports cancellation of the run or of the operation.
func (r *applyRun) interrupted(operationCtx context.Context) error {
	return errors.Join(r.ctx.Err(), operationCtx.Err())
}

// operation reads and checks the live configuration, sends every command
// once and verifies the readback.
func (r *applyRun) operation(operationCtx context.Context, session change.ApplySession) error {
	if operationCtx == nil || session == nil {
		return change.ErrInvalid
	}
	if err := r.interrupted(operationCtx); err != nil {
		return err
	}
	r.state.stage = "response"
	before, err := session.ReadConfiguration(operationCtx)
	if err != nil {
		return err
	}
	if err := r.plan.CheckBefore(before); err != nil {
		return err
	}
	for i, command := range r.state.commands {
		if err := r.send(operationCtx, session, i, command); err != nil {
			return err
		}
	}
	r.state.stage = "response"
	after, err := session.ReadConfiguration(operationCtx)
	if err != nil {
		return err
	}
	fingerprint, err := r.plan.CheckAfter(after)
	if err != nil {
		return err
	}
	r.state.observedConfigSHA256 = fingerprint
	r.state.readbackVerified = true
	return r.interrupted(operationCtx)
}

// send journals the intent for command i, writes it once and journals its
// acknowledgement. An intent publication that may have become visible marks
// the command as possibly sent.
func (r *applyRun) send(operationCtx context.Context, session change.ApplySession, i int, command string) error {
	r.state.stage = "execute"
	if err := r.interrupted(operationCtx); err != nil {
		return err
	}
	r.state.beginCommand(i)
	if err := r.journal.WriteExecution(operationCtx, r.state.record()); err != nil {
		if publication, ok := errors.AsType[publicationStatus](err); ok && publication.PublicationCommitted() {
			r.state.started = true
		}
		return err
	}
	r.state.started = true
	if err := r.interrupted(operationCtx); err != nil {
		return err
	}
	if err := session.Write(operationCtx, command); err != nil {
		return err
	}
	r.state.acknowledge(i)
	if err := r.journal.WriteExecution(context.WithoutCancel(operationCtx), r.state.record()); err != nil {
		return err
	}
	return r.interrupted(operationCtx)
}

// fail records the failure with safe diagnostics, publishes it once more
// without cancellation, and returns the terminal result.
func (r *applyRun) fail(cause, cleanup error) (Result, error) {
	state := r.state
	state.fail(r.ctx.Err() != nil)
	fallback := "command_execution_failed"
	switch state.stage {
	case "connect":
		fallback = "connection_failed"
	case "response":
		fallback = "invalid_command_result"
	}
	state.diagnostics.collect(cause, "operation_errors", fallback, state.stage)
	state.diagnostics.collect(r.ctx.Err(), "cancellation_errors", fallback, state.stage)
	state.diagnostics.collect(cleanup, "cleanup_errors", "session_close_failed", "close")
	state.cleanupFailed = cleanup != nil
	publicationErr := r.journal.WriteExecution(context.WithoutCancel(r.ctx), state.record())
	if publicationErr != nil {
		state.diagnostics.collect(publicationErr, "report_errors", "command_execution_failed", state.stage)
	}
	result := Result{Summary: state.summary(), ReportUnconfirmed: publicationErr != nil, Interrupted: r.ctx.Err() != nil}
	return result, &runError{errors.Join(cause, cleanup, r.ctx.Err(), publicationErr)}
}

func ownedOpen(ctx context.Context, open Opener, run func(context.Context, change.ApplySession) error) (error, error) {
	var mu sync.Mutex
	active, started := true, false
	done := make(chan struct{})
	var callbackErr error
	opErr, closeErr := open(ctx, func(operationCtx context.Context, session change.ApplySession) error {
		mu.Lock()
		if !active || started {
			mu.Unlock()
			return change.ErrInvalid
		}
		started = true
		mu.Unlock()
		err := change.ErrInvalid
		defer func() {
			mu.Lock()
			callbackErr = err
			close(done)
			mu.Unlock()
		}()
		err = run(operationCtx, session)
		return err
	})
	mu.Lock()
	active = false
	admitted := started
	mu.Unlock()
	if !admitted {
		if opErr == nil {
			opErr = change.ErrInvalid
		}
		return opErr, closeErr
	}
	<-done
	return errors.Join(opErr, callbackErr, ctx.Err()), closeErr
}
