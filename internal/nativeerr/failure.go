// Package nativeerr translates connector failures into wireless safe diagnostics.
package nativeerr

import (
	"context"
	"errors"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
)

// Failure classifies an error at the caller's stage without retaining its text.
func Failure(err error, fallback, stage string) *wireless.Failure {
	if err == nil {
		return nil
	}
	if native, ok := err.(*connector.Error); ok && native == nil {
		return wireless.NewFailure(fallback, stage)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return wireless.NewFailure("cancelled", stage)
	case errors.Is(err, context.DeadlineExceeded):
		return wireless.NewFailure("deadline_exceeded", stage)
	case errors.Is(err, connector.ErrClosed), errors.Is(err, connector.ErrNotConnected):
		return wireless.NewFailure("session_closed", stage)
	case errors.Is(err, connector.ErrBusy):
		return wireless.NewFailure("session_busy", stage)
	case errors.Is(err, connector.ErrScope):
		return wireless.NewFailure("session_scope_ended", stage)
	}
	var native *connector.Error
	if errors.As(err, &native) && native != nil {
		return wireless.NewFailure(native.Code, stage)
	}
	return wireless.NewFailure(fallback, stage)
}

// Classify preserves a direct typed failure's allowed stage. Joined and wrapped
// causes must be visited separately to retain independent diagnostics.
func Classify(err error, fallback, stage string) *wireless.Failure {
	if f, ok := err.(*wireless.Failure); ok && f != nil {
		return wireless.NewFailure(f.Code(), f.Stage())
	}
	if f, ok := err.(*connector.Error); ok && f != nil {
		if original := wireless.NewFailure(f.Code, f.Phase).Stage(); original != "" {
			stage = original
		}
		return wireless.NewFailure(f.Code, stage)
	}
	return Failure(err, fallback, stage)
}
