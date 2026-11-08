package nativeerr_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/nativeerr"
)

func TestFailureMapsNativeLifecycleWithoutPrivateText(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"cancelled", context.Canceled, "cancelled"},
		{"deadline", context.DeadlineExceeded, "deadline_exceeded"},
		{"closed", connector.ErrClosed, "session_closed"},
		{"disconnected", connector.ErrNotConnected, "session_closed"},
		{"busy", connector.ErrBusy, "session_busy"},
		{"scope", connector.ErrScope, "session_scope_ended"},
		{"native", &connector.Error{Code: "command_timeout", Phase: "parse", Source: "PRIVATE"}, "command_timeout"},
		{"unknown", errors.New("PRIVATE"), "command_execution_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nativeerr.Failure(fmt.Errorf("PRIVATE: %w", tc.err), "command_execution_failed", "execute")
			if got.Code() != tc.code || got.Stage() != "execute" || got.Error() != "wireless: "+tc.code {
				t.Fatalf("unexpected safe failure: %#v", got)
			}
		})
	}
	if got := nativeerr.Failure(nil, "connection_failed", "connect"); got != nil {
		t.Fatal(got)
	}
}

func TestClassifyPreservesOnlyAllowedTypedStages(t *testing.T) {
	for _, tc := range []struct {
		err   error
		code  string
		stage string
	}{
		{wireless.NewFailure("transport_error", "cleanup"), "transport_error", "cleanup"},
		{&connector.Error{Code: "command_timeout", Phase: "parse", Source: "PRIVATE"}, "command_timeout", "parse"},
		{&connector.Error{Code: "command_timeout", Phase: "PRIVATE"}, "command_timeout", "execute"},
		{&connector.Error{Code: "PRIVATE", Phase: "parse"}, "invalid_command_result", "parse"},
		{(*connector.Error)(nil), "command_execution_failed", "execute"},
	} {
		got := nativeerr.Classify(tc.err, "command_execution_failed", "execute")
		if got.Code() != tc.code || got.Stage() != tc.stage {
			t.Fatalf("unexpected safe failure: %#v", got)
		}
	}
}

func TestClassifyNativeLeavesWrapperTraversalToCaller(t *testing.T) {
	native := &connector.Error{Code: "command_timeout", Phase: "parse", Source: "PRIVATE"}
	failure, cause := nativeerr.ClassifyNative(native, "command_execution_failed", "execute")
	if failure.Code() != "command_timeout" || failure.Stage() != "parse" || cause != nil {
		t.Fatal("native node lost its safe classification", failure, cause)
	}
	if failure, cause := nativeerr.ClassifyNative(fmt.Errorf("PRIVATE: %w", native), "command_execution_failed", "execute"); failure != nil || cause != nil {
		t.Fatal("classification consumed a wrapper", failure, cause)
	}
	if failure, cause := nativeerr.ClassifyNative((*connector.Error)(nil), "command_execution_failed", "execute"); failure == nil || failure.Code() != "command_execution_failed" || cause != nil {
		t.Fatal("typed nil native error lost safe fallback", failure, cause)
	}
}
