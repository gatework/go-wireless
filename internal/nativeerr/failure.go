package nativeerr

import (
	"context"
	"errors"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
)

// Failure classifies an error at the caller's stage without retaining its text.
func Failure(err error, fallback, stage string) *wireless.FailureError {
	if err == nil {
		return nil
	}
	if native, ok := err.(*connector.Error); ok && native == nil { //nolint:errorlint // a typed nil at the top is a caller defect, not a wrapped cause
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
//
//nolint:errorlint // classifies err itself; callers walk wrapped and joined causes
func Classify(err error, fallback, stage string) *wireless.FailureError {
	if f, ok := err.(*wireless.FailureError); ok && f != nil {
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

// ClassifyNative recognizes one direct connector error and exposes only its
// safe failure plus the next cause; ok is false when err is not a connector
// error. Traversal remains with the application so joined diagnostics retain
// their original order and independent cancellation.
//
//nolint:errorlint // classifies err itself; callers walk wrapped and joined causes
func ClassifyNative(err error, fallback, stage string) (failure *wireless.FailureError, ok bool, next error) {
	native, ok := err.(*connector.Error)
	if !ok {
		return nil, false, nil
	}
	if native == nil {
		return wireless.NewFailure(fallback, stage), true, nil
	}
	return Classify(native, fallback, stage), true, native.Unwrap()
}
