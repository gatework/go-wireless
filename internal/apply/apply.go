// Package apply owns one confirmed configuration change and its durable journal.
package apply

import (
	"context"
	"errors"
	"sync"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/report"
)

// Opener lends one session and its operation context, then returns operation and
// cleanup errors separately. Run admits at most one callback and joins it before
// publishing the final result, even if an opener returns while it is running.
type Opener func(context.Context, func(context.Context, wireless.ApplySession) error) (error, error)

// Journal durably records intent before sending and acknowledges completed work.
// Implementations consume each state synchronously without modifying it.
// FinalizeExecution must preserve a recoverable checkpoint if publication fails.
type Journal interface {
	WriteExecution(context.Context, wireless.Record) error
	FinalizeExecution(context.Context, wireless.Record) error
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
func Run(ctx context.Context, plan wireless.ChangePlan, planSHA256 string, open Opener, journal Journal) (Result, error) {
	if plan.Host() == "" || open == nil || journal == nil {
		return Result{}, &runError{wireless.ErrChangePlan}
	}
	commands := plan.Commands()
	state := wireless.Record{"status": "confirmed", "device": plan.Device(), "plan_sha256": planSHA256, "expected_config_sha256": plan.ExpectedConfigSHA256(), "commands": plan.Commands(), "steps": []wireless.Record{}}
	started := false
	stage := "connect"
	fail := func(cause, cleanup error) (Result, error) {
		state["status"] = "failed"
		if started {
			state["status"] = "uncertain"
		}
		state["error"] = "apply_failed"
		if ctx.Err() != nil {
			state["error"] = "interrupted"
		}
		diagnostics := &diagnostics{}
		fallback := "command_execution_failed"
		if stage == "connect" {
			fallback = "connection_failed"
		} else if stage == "response" {
			fallback = "invalid_command_result"
		}
		diagnostics.collect(cause, "operation_errors", fallback, stage)
		diagnostics.collect(ctx.Err(), "cancellation_errors", fallback, stage)
		diagnostics.collect(cleanup, "cleanup_errors", "session_close_failed", "close")
		diagnostics.attach(state)
		if cleanup != nil {
			state["cleanup_error"] = "session_close_failed"
		}
		publicationErr := journal.WriteExecution(context.WithoutCancel(ctx), state)
		if publicationErr != nil {
			diagnostics.collect(publicationErr, "report_errors", "command_execution_failed", stage)
			diagnostics.attach(state)
		}
		summary := wireless.Record{"status": state["status"], "error": state["error"]}
		if pending, ok := state["pending_command"]; ok {
			summary["pending_command"] = pending
		}
		diagnostics.attach(summary)
		result := Result{Summary: summary, ReportUnconfirmed: publicationErr != nil, Interrupted: ctx.Err() != nil}
		return result, &runError{errors.Join(cause, cleanup, ctx.Err(), publicationErr)}
	}
	if err := journal.WriteExecution(ctx, state); err != nil {
		return fail(err, nil)
	}
	operation := func(operationCtx context.Context, session wireless.ApplySession) error {
		if operationCtx == nil || session == nil {
			return wireless.ErrChangePlan
		}
		if err := operationCtx.Err(); err != nil {
			return err
		}
		stage = "response"
		before, err := session.ReadConfiguration(operationCtx)
		if err != nil {
			return err
		}
		if err := plan.CheckBefore(before); err != nil {
			return err
		}
		for i, command := range commands {
			stage = "execute"
			if err := operationCtx.Err(); err != nil {
				return err
			}
			state["status"] = "executing"
			state["pending_command"] = i
			if err := journal.WriteExecution(operationCtx, state); err != nil {
				var publication *report.Error
				if errors.As(err, &publication) && publication.Committed {
					started = true
				}
				return err
			}
			started = true
			if err := operationCtx.Err(); err != nil {
				return err
			}
			if err := session.Write(operationCtx, command); err != nil {
				return err
			}
			steps := state["steps"].([]wireless.Record)
			state["steps"] = append(steps, wireless.Record{"index": i, "status": "acknowledged"})
			delete(state, "pending_command")
			if err := journal.WriteExecution(context.WithoutCancel(operationCtx), state); err != nil {
				return err
			}
			if err := operationCtx.Err(); err != nil {
				return err
			}
		}
		stage = "response"
		after, err := session.ReadConfiguration(operationCtx)
		if err != nil {
			return err
		}
		fingerprint, err := plan.CheckAfter(after)
		if err != nil {
			return err
		}
		state["observed_config_sha256"] = fingerprint
		state["readback_verified"] = true
		return operationCtx.Err()
	}
	operationErr, cleanupErr := ownedOpen(ctx, open, operation)
	if operationErr != nil || cleanupErr != nil || ctx.Err() != nil {
		return fail(operationErr, cleanupErr)
	}
	state["status"] = "configuration_verified"
	if err := journal.FinalizeExecution(ctx, state); err != nil {
		return fail(err, nil)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err(), nil)
	}
	return Result{}, nil
}

func ownedOpen(ctx context.Context, open Opener, run func(context.Context, wireless.ApplySession) error) (error, error) {
	var mu sync.Mutex
	active, started := true, false
	done := make(chan struct{})
	var callbackErr error
	opErr, closeErr := open(ctx, func(operationCtx context.Context, session wireless.ApplySession) error {
		mu.Lock()
		if !active || started {
			mu.Unlock()
			return wireless.ErrChangePlan
		}
		started = true
		mu.Unlock()
		err := wireless.ErrChangePlan
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
			opErr = wireless.ErrChangePlan
		}
		return opErr, closeErr
	}
	<-done
	return errors.Join(opErr, callbackErr, ctx.Err()), closeErr
}
