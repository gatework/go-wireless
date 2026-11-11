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
func Run(ctx context.Context, plan change.ChangePlan, planSHA256 string, open Opener, journal Journal) (Result, error) {
	if plan.Host() == "" || open == nil || journal == nil {
		return Result{}, &runError{change.ErrChangePlan}
	}
	state := newExecution(plan, planSHA256)
	fail := func(cause, cleanup error) (Result, error) {
		state.fail(ctx.Err() != nil)
		fallback := "command_execution_failed"
		switch state.stage {
		case "connect":
			fallback = "connection_failed"
		case "response":
			fallback = "invalid_command_result"
		}
		state.diagnostics.collect(cause, "operation_errors", fallback, state.stage)
		state.diagnostics.collect(ctx.Err(), "cancellation_errors", fallback, state.stage)
		state.diagnostics.collect(cleanup, "cleanup_errors", "session_close_failed", "close")
		state.cleanupFailed = cleanup != nil
		publicationErr := journal.WriteExecution(context.WithoutCancel(ctx), state.record())
		if publicationErr != nil {
			state.diagnostics.collect(publicationErr, "report_errors", "command_execution_failed", state.stage)
		}
		result := Result{Summary: state.summary(), ReportUnconfirmed: publicationErr != nil, Interrupted: ctx.Err() != nil}
		return result, &runError{errors.Join(cause, cleanup, ctx.Err(), publicationErr)}
	}
	if err := journal.WriteExecution(ctx, state.record()); err != nil {
		return fail(err, nil)
	}
	operation := func(operationCtx context.Context, session change.ApplySession) error {
		if operationCtx == nil || session == nil {
			return change.ErrChangePlan
		}
		if err := errors.Join(ctx.Err(), operationCtx.Err()); err != nil {
			return err
		}
		state.stage = "response"
		before, err := session.ReadConfiguration(operationCtx)
		if err != nil {
			return err
		}
		if err := plan.CheckBefore(before); err != nil {
			return err
		}
		for i, command := range state.commands {
			state.stage = "execute"
			if err := errors.Join(ctx.Err(), operationCtx.Err()); err != nil {
				return err
			}
			state.beginCommand(i)
			if err := journal.WriteExecution(operationCtx, state.record()); err != nil {
				if publication, ok := errors.AsType[publicationStatus](err); ok && publication.PublicationCommitted() {
					state.started = true
				}
				return err
			}
			state.started = true
			if err := errors.Join(ctx.Err(), operationCtx.Err()); err != nil {
				return err
			}
			if err := session.Write(operationCtx, command); err != nil {
				return err
			}
			state.acknowledge(i)
			if err := journal.WriteExecution(context.WithoutCancel(operationCtx), state.record()); err != nil {
				return err
			}
			if err := errors.Join(ctx.Err(), operationCtx.Err()); err != nil {
				return err
			}
		}
		state.stage = "response"
		after, err := session.ReadConfiguration(operationCtx)
		if err != nil {
			return err
		}
		fingerprint, err := plan.CheckAfter(after)
		if err != nil {
			return err
		}
		state.observedConfigSHA256 = fingerprint
		state.readbackVerified = true
		return errors.Join(ctx.Err(), operationCtx.Err())
	}
	operationErr, cleanupErr := ownedOpen(ctx, open, operation)
	if operationErr != nil || cleanupErr != nil || ctx.Err() != nil {
		return fail(operationErr, cleanupErr)
	}
	state.status = configurationVerified
	if err := journal.FinalizeExecution(ctx, state.record()); err != nil {
		return fail(err, nil)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err(), nil)
	}
	return Result{}, nil
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
			return change.ErrChangePlan
		}
		started = true
		mu.Unlock()
		err := change.ErrChangePlan
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
			opErr = change.ErrChangePlan
		}
		return opErr, closeErr
	}
	<-done
	return errors.Join(opErr, callbackErr, ctx.Err()), closeErr
}
