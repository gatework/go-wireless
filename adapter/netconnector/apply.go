package netconnector

import (
	"context"
	"errors"
	"strings"

	"github.com/gatework/go-wireless/vendors"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/internal/nativeerr"
)

// Apply opens once and owns one operation for preparation, precheck, all writes
// and readback. Both operation and cleanup errors survive lease retirement.
func Apply(ctx context.Context, device *connector.Device, run func(context.Context, change.ApplySession) error) (error, error) {
	if device == nil || run == nil {
		return change.ErrChangePlan, nil
	}
	if e := device.Connect(ctx); e != nil {
		if errors.Is(e, connector.ErrBusy) {
			return nativeerr.Failure(e, "connection_failed", "connect"), nil
		}
		return nativeerr.Failure(e, "connection_failed", "connect"), device.Close()
	}
	entered := false
	operationErr, closeErr := device.WithBorrowedOperation(ctx, func(ctx context.Context, op *connector.Operation) error {
		entered = true
		e := run(ctx, &applySession{op: op})
		if op.Connected() {
			_ = op.Retire() // The scope returns the first retirement error separately.
		}
		return e
	})
	if !entered && !errors.Is(operationErr, connector.ErrBusy) {
		closeErr = errors.Join(closeErr, device.Close())
	}
	return operationErr, closeErr
}

type applySession struct{ op *connector.Operation }

func (s *applySession) execute(ctx context.Context, command string, query bool) (connector.Result, error) {
	if !s.op.Connected() {
		return connector.Result{}, wireless.NewFailure("session_closed", "execute")
	}
	cmd := connector.Command{Text: strings.TrimSpace(command), OutputSensitive: true}
	if query {
		prompt := strings.TrimSpace(s.op.RawPrompt())
		if prompt == "" {
			return connector.Result{}, change.ErrChangePlan
		}
		cmd.Prompt = exactPrompt(prompt)
	}
	result, e := s.op.ExecuteCommand(ctx, cmd, connector.ExecuteOptions{SkipPrivilege: true})
	if e != nil {
		return result, e
	}
	steps := result.Steps()
	if len(steps) != 1 || steps[0].Command().Text != cmd.Text || steps[0].Response().RawPrompt() == "" {
		return result, wireless.NewFailure("invalid_command_result", "response")
	}
	return result, nil
}
func (s *applySession) Write(ctx context.Context, command string) error {
	_, e := s.execute(ctx, command, false)
	return e
}
func (s *applySession) ReadConfiguration(ctx context.Context) (wireless.Record, error) {
	if _, e := s.execute(ctx, "screen-length disable", true); e != nil {
		return nil, e
	}
	result, e := s.execute(ctx, "display current-configuration", true)
	if e != nil {
		return nil, e
	}
	body, e := responseBody(result.Steps()[0])
	if e != nil {
		return nil, e
	}
	parser, e := vendors.NewBuiltinRegistry().Fetch("h3c_wireless", "")
	if e != nil {
		return nil, e
	}
	observation, e := parser.Parse(wireless.Configuration, body, true)
	if e != nil || observation.Status() != wireless.Complete || len(observation.Records()) != 1 {
		return nil, wireless.NewFailure("invalid_command_result", "response")
	}
	return observation.Records()[0], nil
}
