package apply_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/apply"
	"github.com/gatework/go-wireless/report"
)

type session struct {
	before, after wireless.Record
	read          int
	commands      []string
	readHook      func(context.Context) error
	writeHook     func(context.Context, string) error
}

func (s *session) ReadConfiguration(ctx context.Context) (wireless.Record, error) {
	s.read++
	if s.readHook != nil {
		if err := s.readHook(ctx); err != nil {
			return nil, err
		}
	}
	if s.read == 1 {
		return s.before, nil
	}
	return s.after, nil
}

func (s *session) Write(ctx context.Context, command string) error {
	s.commands = append(s.commands, command)
	if s.writeHook != nil {
		return s.writeHook(ctx, command)
	}
	return ctx.Err()
}

type journal struct {
	entries   []wireless.Record
	finalized bool
	writeHook func(context.Context, wireless.Record) error
	finalHook func(context.Context, wireless.Record) error
}

func (j *journal) WriteExecution(ctx context.Context, state wireless.Record) error {
	if j.writeHook != nil {
		if err := j.writeHook(ctx, state); err != nil {
			return err
		}
	}
	// The journal consumes each synchronous write, as the durable adapter does.
	body, err := wireless.MarshalJSON(state)
	if err != nil {
		return err
	}
	copy, _, err := wireless.DecodeJSONDocument(body)
	if err == nil {
		j.entries = append(j.entries, copy)
	}
	return err
}

func (j *journal) FinalizeExecution(ctx context.Context, state wireless.Record) error {
	if j.finalHook != nil {
		if err := j.finalHook(ctx, state); err != nil {
			return err
		}
	}
	j.finalized = true
	return j.WriteExecution(ctx, state)
}

func fixture(t *testing.T) (wireless.Record, *session) {
	t.Helper()
	body, err := os.ReadFile("../../testdata/ruby-interop/security.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := wireless.DecodeJSONDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("../../testdata/ruby-interop/source.cfg")
	if err != nil {
		t.Fatal(err)
	}
	parser, err := wireless.NewConfig("h3c_wireless", "")
	if err != nil {
		t.Fatal(err)
	}
	parse := func(body string) wireless.Record {
		observation, err := parser.Parse(body, true)
		if err != nil || observation.Status() != wireless.Complete {
			t.Fatalf("configuration fixture: %v, %v", observation, err)
		}
		return observation.Records()[0]
	}
	before := parse(string(source))
	after := parse(strings.ReplaceAll(strings.ReplaceAll(string(source), " cipher-suite tkip\n", ""), " security-ie wpa\n", ""))
	return plan, &session{before: before, after: after}
}

func openSession(s wireless.ApplySession) apply.Opener {
	return func(ctx context.Context, run func(context.Context, wireless.ApplySession) error) (error, error) {
		return run(ctx, s), nil
	}
}

func TestRunUsesBorrowedContextAndDurableAcknowledgement(t *testing.T) {
	for _, cancelAfterSend := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "cancel_after_send"}[cancelAfterSend], func(t *testing.T) {
			plan, s := fixture(t)
			type key struct{}
			child, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "borrowed"))
			defer cancel()
			j := &journal{}
			s.readHook = func(ctx context.Context) error {
				if ctx != child {
					t.Error("read did not receive the borrowed context")
				}
				return ctx.Err()
			}
			s.writeHook = func(ctx context.Context, command string) error {
				if ctx != child {
					t.Error("write did not receive the borrowed context")
				}
				last := j.entries[len(j.entries)-1]
				if last["status"] != "executing" || last["pending_command"] == nil {
					t.Fatal("command has no durable intent", last)
				}
				if cancelAfterSend {
					cancel()
				}
				return nil
			}
			acknowledged := 0
			j.writeHook = func(ctx context.Context, state wireless.Record) error {
				if state["status"] != "executing" {
					return nil
				}
				if state["pending_command"] != nil {
					if ctx != child {
						t.Error("intent lost the borrowed context")
					}
					return ctx.Err()
				}
				acknowledged++
				if ctx.Value(key{}) != "borrowed" || ctx.Err() != nil {
					t.Error("acknowledgement lost values or remained cancellable")
				}
				return nil
			}
			open := func(ctx context.Context, run func(context.Context, wireless.ApplySession) error) (error, error) {
				return run(child, s), nil
			}
			result, err := apply.Run(t.Context(), plan, "exact-file-digest", open, j)
			if cancelAfterSend {
				if !errors.Is(err, context.Canceled) || len(s.commands) != 1 || s.read != 1 || acknowledged != 1 || j.finalized || result.Summary["status"] != "uncertain" {
					t.Fatalf("cancellation was lost or writes replayed: %v, %#v", err, result)
				}
				if j.entries[len(j.entries)-1]["pending_command"] != nil {
					t.Fatal("acknowledged command still pending")
				}
			} else if err != nil || len(s.commands) != 8 || s.read != 2 || acknowledged != 8 || !j.finalized {
				t.Fatalf("incomplete execution: %v, %#v", err, result)
			}
		})
	}
}

func TestRunAdmissionRefusesRepeatedAndLateCallbacks(t *testing.T) {
	plan, s := fixture(t)
	j := &journal{}
	var saved func(context.Context, wireless.ApplySession) error
	open := func(ctx context.Context, run func(context.Context, wireless.ApplySession) error) (error, error) {
		saved = run
		first := run(ctx, s)
		if err := run(ctx, s); !errors.Is(err, wireless.ErrChangePlan) {
			t.Fatal("repeated callback was admitted", err)
		}
		return first, nil
	}
	if _, err := apply.Run(t.Context(), plan, "hash", open, j); err != nil {
		t.Fatal(err)
	}
	if err := saved(t.Context(), s); !errors.Is(err, wireless.ErrChangePlan) {
		t.Fatal("late callback was admitted", err)
	}
	if len(s.commands) != 8 || s.read != 2 {
		t.Fatal("replayed work", s.commands, s.read)
	}
}

func TestRunJoinsAdmittedCallbackBeforePublication(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "goexit"}[abort], func(t *testing.T) {
			plan, s := fixture(t)
			entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
			s.readHook = func(ctx context.Context) error {
				if s.read == 1 {
					close(entered)
					<-release
					if abort {
						runtime.Goexit()
					}
				}
				return ctx.Err()
			}
			j := &journal{}
			open := func(ctx context.Context, run func(context.Context, wireless.ApplySession) error) (error, error) {
				go func() { _ = run(ctx, s) }()
				<-entered
				close(returned)
				return nil, nil
			}
			done := make(chan error, 1)
			go func() {
				_, err := apply.Run(t.Context(), plan, "hash", open, j)
				done <- err
			}()
			<-returned
			select {
			case err := <-done:
				t.Fatal("published before callback finished", err)
			default:
			}
			close(release)
			select {
			case err := <-done:
				if abort {
					if err == nil || j.finalized || len(s.commands) != 0 {
						t.Fatal("aborted callback became success", err)
					}
				} else if err != nil || !j.finalized || len(s.commands) != 8 {
					t.Fatal("admitted callback lost", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("admitted callback was not joined")
			}
		})
	}
}

func TestRunRetainsIndependentFailuresAndUncertainIntent(t *testing.T) {
	plan, s := fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.writeHook = func(context.Context, string) error {
		cancel()
		return errors.Join(wireless.NewFailure("command_timeout", "execute"), errors.New("PRIVATE"))
	}
	j := &journal{writeHook: func(ctx context.Context, state wireless.Record) error {
		if state["status"] == "uncertain" {
			return &report.Error{Relative: "execution.json", Phase: "rename"}
		}
		return nil
	}}
	open := func(ctx context.Context, run func(context.Context, wireless.ApplySession) error) (error, error) {
		return run(ctx, s), wireless.NewFailure("transport_error", "cleanup")
	}
	result, err := apply.Run(ctx, plan, "hash", open, j)
	if !errors.Is(err, context.Canceled) || !result.Interrupted || !result.ReportUnconfirmed || len(s.commands) != 1 || s.read != 1 || j.finalized {
		t.Fatalf("failed execution lost evidence: %v, %#v", err, result)
	}
	for _, field := range []string{"operation_errors", "cancellation_errors", "cleanup_errors", "report_errors"} {
		if result.Summary[field] == nil {
			t.Fatal("missing independent failure", field, result.Summary)
		}
	}
	if !reflect.DeepEqual(result.Summary["operation_errors"], []wireless.Record{{"code": "command_timeout", "stage": "execute"}, {"code": "command_execution_failed", "stage": "execute"}}) {
		t.Fatal("diagnostic order changed", result.Summary)
	}
	body, _ := wireless.MarshalJSON(result.Summary)
	if strings.Contains(string(body)+err.Error(), "PRIVATE") {
		t.Fatal("private error leaked")
	}
	last := j.entries[len(j.entries)-1]
	if last["status"] != "executing" || last["pending_command"] == nil {
		t.Fatal("durable intent overwritten by failed publication", last)
	}
}
