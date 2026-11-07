package wireless

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type testSession struct {
	connected       bool
	output          map[string]string
	failures        map[string]*Failure
	stateErr, error error
	calls           []string
	onExecute       func(string)
	receipt         func(string) Receipt
}

func (s *testSession) State(context.Context) (bool, error) { return s.connected, s.stateErr }
func (s *testSession) Execute(_ context.Context, command string) (Receipt, error) {
	s.calls = append(s.calls, command)
	if s.onExecute != nil {
		s.onExecute(command)
	}
	if s.error != nil {
		return Receipt{}, s.error
	}
	if s.receipt != nil {
		return s.receipt(command), nil
	}
	body := s.output[command]
	f := s.failures[command]
	return NewReceipt(ReceiptOptions{Attempted: true, Command: command, Raw: command + "\n" + body + "<WLC>", Body: body, Success: f == nil, Steps: 1, Failure: f}), nil
}
func controllerFixture() *testSession {
	return &testSession{connected: true, output: map[string]string{"screen-length disable": "", "display wlan ap all verbose": "Total number of APs : 0\n", "display wlan client verbose": "Total number of clients : 0\n"}, failures: map[string]*Failure{}}
}
func options(s Session, dims ...Dimension) CollectOptions {
	return CollectOptions{Target: Record{"host": "192.0.2.10"}, Platform: "h3c", ConnectorPlatform: "h3c_wireless", Dimensions: dims, Open: func(ctx context.Context, run func(Session) error) error { return run(s) }}
}
func mustCollect(t *testing.T, c *Collector, o CollectOptions) Snapshot {
	t.Helper()
	s, e := c.Collect(t.Context(), o)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func code(o Observation) string {
	issues := o.Issues()
	if len(issues) == 0 {
		return ""
	}
	v, _ := issues[0]["code"].(string)
	return v
}
func TestCollectorSharedQueriesFirstFactsAndNoSession(t *testing.T) {
	s := controllerFixture()
	c := NewCollector(CollectorOptions{})
	snap := mustCollect(t, c, options(s, AccessPoints, Radios))
	if !snap.Complete() || len(s.calls) != 2 || snap.Dimension(Clients).Status() != NotRequested {
		t.Fatal(snap, s.calls)
	}
	if !reflect.DeepEqual(snap.Dimension(AccessPoints).Evidence(), snap.Dimension(Radios).Evidence()) {
		t.Fatal("shared metadata diverged")
	}
	s = controllerFixture()
	s.output["display wlan ap all verbose"] = "Total number of APs : 1\nAP name : AP-FIRST\nAP ID : 1\nState : Run\n"
	o := options(s, AccessPoints)
	o.Open = func(_ context.Context, run func(Session) error) error {
		if e := run(s); e != nil {
			return e
		}
		s.output["display wlan ap all verbose"] = "Total number of APs : 0\n"
		return run(s)
	}
	snap = mustCollect(t, c, o)
	if len(snap.Records(AccessPoints)) != 1 || snap.Records(AccessPoints)[0]["name"] != "AP-FIRST" || code(snap.Dimension(Collection)) != "session_reentered" || len(s.calls) != 2 {
		t.Fatal(snap, s.calls)
	}
	o.Open = func(context.Context, func(Session) error) error { return nil }
	snap = mustCollect(t, c, o)
	if code(snap.Dimension(AccessPoints)) != "session_not_opened" || snap.Complete() {
		t.Fatal(snap)
	}
}
func TestCollectorPreflightAndOwnership(t *testing.T) {
	for _, change := range []func(*CollectOptions){func(o *CollectOptions) { o.Target = nil }, func(o *CollectOptions) { o.Dimensions = []Dimension{} }, func(o *CollectOptions) { o.Dimensions = []Dimension{Clients, Clients} }, func(o *CollectOptions) { o.Dimensions = []Dimension{"unknown"} }, func(o *CollectOptions) { o.Dimensions = []Dimension{Collection} }, func(o *CollectOptions) { o.APNames = []string{"bad\nname"} }, func(o *CollectOptions) { o.ConnectorPlatform = "huawei" }, func(o *CollectOptions) { o.Platform = "unknown" }} {
		o := options(controllerFixture(), Clients)
		o.Open = func(context.Context, func(Session) error) error { t.Fatal("preflight opened device"); return nil }
		change(&o)
		if _, e := NewCollector(CollectorOptions{}).Collect(t.Context(), o); e == nil {
			t.Fatal("invalid input accepted")
		}
	}
	s := controllerFixture()
	o := options(s, AccessPoints)
	o.Target["nested"] = Record{"name": "before"}
	o.Open = func(_ context.Context, run func(Session) error) error {
		o.Target["nested"].(Record)["name"] = "after"
		o.Dimensions[0] = Clients
		return run(s)
	}
	snap := mustCollect(t, NewCollector(CollectorOptions{}), o)
	if snap.Target()["nested"].(map[string]any)["name"] != "before" || !snap.Dimension(AccessPoints).Complete() {
		t.Fatal("callback mutated selected input")
	}
}
func TestCollectorIndependentFailurePreparationAndClose(t *testing.T) {
	s := controllerFixture()
	s.failures["display wlan ap all verbose"] = NewFailure("device_error", "execute")
	snap := mustCollect(t, NewCollector(CollectorOptions{}), options(s, AccessPoints, Radios, Clients))
	if !snap.Dimension(Clients).Complete() || snap.Dimension(AccessPoints).Status() != Failed || snap.Dimension(Collection).Status() != Partial || len(s.calls) != 3 {
		t.Fatal(snap, s.calls)
	}
	s = controllerFixture()
	s.failures["screen-length disable"] = NewFailure("device_error", "execute")
	snap = mustCollect(t, NewCollector(CollectorOptions{}), options(s, AccessPoints, Clients))
	if len(s.calls) != 1 || snap.Dimension(Clients).Issues()[0]["skipped"] != true {
		t.Fatal(snap, s.calls)
	}
	s = controllerFixture()
	s.onExecute = func(command string) {
		if command == "display wlan ap all verbose" {
			s.connected = false
		}
	}
	s.failures["display wlan ap all verbose"] = NewFailure("connection_closed", "execute")
	snap = mustCollect(t, NewCollector(CollectorOptions{}), options(s, AccessPoints, Clients))
	if len(s.calls) != 2 || code(snap.Dimension(Clients)) != "connection_closed" || snap.Dimension(Clients).Issues()[0]["skipped"] != true {
		t.Fatal(snap, s.calls)
	}
	s = controllerFixture()
	o := options(s, AccessPoints)
	o.Open = func(_ context.Context, run func(Session) error) error {
		_ = run(s)
		return errors.New("private close message")
	}
	snap = mustCollect(t, NewCollector(CollectorOptions{}), o)
	if !snap.Dimension(AccessPoints).Complete() || code(snap.Dimension(Collection)) != "session_close_failed" {
		t.Fatal(snap)
	}
}
func TestCollectorOrderingDependenciesUnsupportedAndParserCompleteness(t *testing.T) {
	s := controllerFixture()
	o := options(s)
	o.Dimensions = nil
	_ = mustCollect(t, NewCollector(CollectorOptions{}), o)
	p, _ := NewProfile("h3c", "")
	dims := []Dimension{}
	for _, d := range p.Dimensions() {
		if d != LLDP {
			dims = append(dims, d)
		}
	}
	queries, _ := p.Queries(dims)
	want := []string{p.PreparationCommand()}
	for _, q := range queries {
		want = append(want, q.Command)
	}
	if !reflect.DeepEqual(want, s.calls) {
		t.Fatalf("order %v expected %v", s.calls, want)
	}
	s = controllerFixture()
	s.output["display current-configuration"] = "sysname WLC\nwlan service-template office\n ssid Office\n#\n--More--\nreturn\n"
	snap := mustCollect(t, NewCollector(CollectorOptions{}), options(s, ServiceTemplates, APGroups, Clients))
	if snap.Dimension(Configuration).Status() != Partial || snap.Dimension(ServiceTemplates).Status() != Partial || !snap.Dimension(Clients).Complete() || len(s.calls) != 3 {
		t.Fatal(snap, s.calls)
	}
	s = controllerFixture()
	s.output["display wlan ap all verbose"] = ""
	snap = mustCollect(t, NewCollector(CollectorOptions{}), options(s, AccessPoints))
	if snap.Complete() {
		t.Fatal("transport success became completeness")
	}
	s = controllerFixture()
	o = options(s, Radios, LLDP)
	o.Platform = "huawei"
	o.ConnectorPlatform = "huawei"
	s.output["display ap all"] = "Total: 0\n"
	snap = mustCollect(t, NewCollector(CollectorOptions{}), o)
	if snap.Dimension(Radios).Status() != Unsupported || snap.Dimension(LLDP).Status() != Unsupported || len(s.calls) != 2 {
		t.Fatal(snap, s.calls)
	}
}
func TestCollectorLateAndConcurrentCallbacksNeverReplay(t *testing.T) {
	s := controllerFixture()
	o := options(s, Clients)
	var saved func(Session) error
	o.Open = func(_ context.Context, run func(Session) error) error {
		saved = run
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { _ = run(s) })
		}
		wg.Wait()
		return nil
	}
	snap := mustCollect(t, NewCollector(CollectorOptions{}), o)
	if code(snap.Dimension(Collection)) != "session_reentered" || len(s.calls) != 2 {
		t.Fatal(snap, s.calls)
	}
	_ = saved(s)
	if len(s.calls) != 2 {
		t.Fatal("late callback replayed")
	}
}

type apReaderFunc func(context.Context, Record) (APOutcome, error)

func (f apReaderFunc) Read(ctx context.Context, ap Record) (APOutcome, error) { return f(ctx, ap) }
func goodAPCapture(t *testing.T, body string) Capture {
	t.Helper()
	now := time.Now()
	c, e := NewCapture(CaptureOptions{Command: LLDPCommand, Body: &body, StartedAt: now, CompletedAt: now, Evidence: []Record{{"sha256": "synthetic", "ap_name": "OTHER", "command": "wrong", "status": "failed", "kind": "wrong", "attempted": false, "neighbor_count": 99}}})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func apInventory(s *testSession) {
	s.output["display wlan ap all verbose"] = "Total number of APs : 2\nAP name : AP-1\nAP ID : 1\nState : Run\nAP name : AP-2\nAP ID : 2\nState : Run\n"
}
func TestCollectorAPLazyDependenciesSelectionAndBinding(t *testing.T) {
	for _, dims := range [][]Dimension{{Clients}, {LLDP}} {
		s := controllerFixture()
		o := options(s, dims...)
		o.APCredentials = func(context.Context) (APCredentials, error) {
			t.Fatal("resolved unused credentials")
			return APCredentials{}, nil
		}
		snap := mustCollect(t, NewCollector(CollectorOptions{}), o)
		if !snap.Complete() {
			t.Fatal(snap)
		}
	}
	s := controllerFixture()
	apInventory(s)
	o := options(s, LLDP)
	o.APCredentials = func(context.Context) (APCredentials, error) {
		t.Fatal("resolved credentials without factory")
		return APCredentials{}, nil
	}
	snap := mustCollect(t, NewCollector(CollectorOptions{}), o)
	if code(snap.Dimension(LLDP)) != "ap_session_required" || !snap.Dimension(AccessPoints).Complete() {
		t.Fatal(snap)
	}
	names := []string{"AP-1"}
	o = options(s, LLDP)
	o.APNames = names
	cap := goodAPCapture(t, "No LLDP neighbors\n")
	reads := 0
	c := NewCollector(CollectorOptions{APFactory: func(_ context.Context, session Session, _ APCredentials) (APReader, error) {
		if session != s {
			t.Fatal("controller replaced")
		}
		names[0] = "AP-2"
		return apReaderFunc(func(_ context.Context, ap Record) (APOutcome, error) {
			reads++
			if ap["name"] != "AP-1" {
				t.Fatal("selection changed")
			}
			return APOutcome{Result: &cap}, nil
		}), nil
	}})
	snap = mustCollect(t, c, o)
	ev := snap.Dimension(LLDP).Evidence()[0]
	if reads != 1 || ev["ap_name"] != "AP-1" || ev["command"] != LLDPCommand || ev["status"] != "complete" || ev["attempted"] != true || ev["neighbor_count"] != 0 || ev["sha256"] != "synthetic" {
		t.Fatal(ev)
	}
}
func TestCollectorAPFailuresDoNotLoseFactsOrOtherAPs(t *testing.T) {
	body := "LLDP neighbor-information of port 1[GE1]:\nLLDP neighbor index: 1\nChassis ID: switch\nPort ID: GE2\n"
	cap := goodAPCapture(t, body)
	for _, which := range []string{"error", "cleanup", "close", "read"} {
		t.Run(which, func(t *testing.T) {
			s := controllerFixture()
			apInventory(s)
			c := NewCollector(CollectorOptions{APFactory: func(context.Context, Session, APCredentials) (APReader, error) {
				return apReaderFunc(func(_ context.Context, ap Record) (APOutcome, error) {
					if ap["name"] == "AP-2" {
						return APOutcome{Error: NewFailure("ap_not_online", ""), Skipped: true}, nil
					}
					out := APOutcome{Result: &cap}
					switch which {
					case "error":
						out.Error = NewFailure("device_error", "")
					case "cleanup":
						out.CleanupError = NewFailure("ap_exit_failed", "")
					case "close":
						out.CloseError = NewFailure("session_close_failed", "")
					case "read":
						return out, errors.New("private error")
					}
					return out, nil
				}), nil
			}})
			snap := mustCollect(t, c, options(s, LLDP, Clients))
			if len(snap.Records(LLDP)) != 1 || snap.Dimension(LLDP).Status() != Partial || !snap.Dimension(Clients).Complete() || len(snap.Dimension(LLDP).Evidence()) != 2 {
				t.Fatal(snap)
			}
			data, _ := json.Marshal(snap)
			if strings.Contains(string(data), "private error") {
				t.Fatal("leaked error")
			}
		})
	}
}
func TestCollectorAPInvalidCapturesSetupAndMissingNames(t *testing.T) {
	now := time.Now()
	body := "No LLDP neighbors\n"
	wrong, _ := NewCapture(CaptureOptions{Command: "display unrelated", Body: &body, StartedAt: now, CompletedAt: now})
	good := goodAPCapture(t, body)
	zero := Capture{}
	for _, outcome := range []APOutcome{{Result: &wrong}, {Result: &good, Skipped: true}, {Result: &zero}, {}} {
		s := controllerFixture()
		apInventory(s)
		c := NewCollector(CollectorOptions{APFactory: func(context.Context, Session, APCredentials) (APReader, error) {
			return apReaderFunc(func(context.Context, Record) (APOutcome, error) { return outcome, nil }), nil
		}})
		snap := mustCollect(t, c, options(s, LLDP, Clients))
		if snap.Dimension(LLDP).Status() != Failed || !snap.Dimension(Clients).Complete() {
			t.Fatal(snap)
		}
	}
	for _, factory := range []APFactory{func(context.Context, Session, APCredentials) (APReader, error) {
		return nil, errors.New("private setup")
	}, func(context.Context, Session, APCredentials) (APReader, error) { return nil, nil }} {
		s := controllerFixture()
		apInventory(s)
		snap := mustCollect(t, NewCollector(CollectorOptions{APFactory: factory}), options(s, LLDP, Clients))
		if snap.Dimension(LLDP).Status() != Failed || !snap.Dimension(Clients).Complete() {
			t.Fatal(snap)
		}
	}
	s := controllerFixture()
	apInventory(s)
	o := options(s, LLDP)
	o.APNames = []string{"missing", "missing"}
	o.APCredentials = func(context.Context) (APCredentials, error) {
		t.Fatal("unused credentials")
		return APCredentials{}, nil
	}
	snap := mustCollect(t, NewCollector(CollectorOptions{}), o)
	if len(snap.Dimension(LLDP).Issues()) != 2 {
		t.Fatal(snap.Dimension(LLDP).Issues())
	}
}
func TestCollectorAPSkippedFailuresDeduplicateAndRepeat(t *testing.T) {
	now := time.Now()
	cap, e := NewCapture(CaptureOptions{Command: LLDPCommand, StartedAt: now, CompletedAt: now, Failure: NewFailure("session_closed", "session"), Skipped: true})
	if e != nil {
		t.Fatal(e)
	}
	s := controllerFixture()
	apInventory(s)
	calls := 0
	factoryCalls := 0
	c := NewCollector(CollectorOptions{APFactory: func(context.Context, Session, APCredentials) (APReader, error) {
		factoryCalls++
		return apReaderFunc(func(context.Context, Record) (APOutcome, error) {
			calls++
			return APOutcome{Result: &cap, Skipped: true, Error: NewFailure("session_closed", ""), CleanupError: NewFailure("session_closed", "")}, nil
		}), nil
	}})
	o := options(s, LLDP)
	o.Open = func(_ context.Context, run func(Session) error) error { _ = run(s); return run(s) }
	snap := mustCollect(t, c, o)
	if calls != 2 || factoryCalls != 1 || len(snap.Dimension(LLDP).Issues()) != 2 || snap.Dimension(LLDP).Evidence()[0]["attempted"] != false || code(snap.Dimension(Collection)) != "session_reentered" {
		t.Fatal(snap)
	}
}
func TestReaderEvidenceImmutableSafeAndFirstFailure(t *testing.T) {
	s := controllerFixture()
	now := time.Date(2026, 10, 26, 0, 0, 0, 123000000, time.UTC)
	r, _ := NewReader(s, func() time.Time { return now })
	cap, e := r.Read(t.Context(), "display wlan client verbose")
	if e != nil || !cap.Success() {
		t.Fatal(cap, e)
	}
	raw := "display wlan client verbose\nTotal number of clients : 0\n<WLC>"
	ev := cap.Evidence()[0]
	if ev["sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) || ev["bytes"] != len(raw) || ev["observed_at"] != "2026-10-26T00:00:00.123000Z" {
		t.Fatal(ev)
	}
	ev["bytes"] = 0
	if cap.Evidence()[0]["bytes"] == 0 {
		t.Fatal("mutable evidence")
	}
	data, _ := json.Marshal(cap)
	for _, text := range []string{string(data), fmt.Sprint(cap), fmt.Sprintf("%#v", cap)} {
		if strings.Contains(text, "Total number") {
			t.Fatal("body leaked")
		}
	}
	s.failures["display wlan client verbose"] = NewFailure("device_error", "")
	s.onExecute = func(string) { s.connected = false }
	failed, _ := r.Read(t.Context(), "display wlan client verbose")
	skipped, _ := r.Read(t.Context(), "display wlan ap all verbose")
	if failed.Success() || !skipped.Skipped() || skipped.Failure().Code() != "device_error" || len(skipped.Evidence()) != 0 {
		t.Fatal(failed, skipped)
	}
}
func TestReaderRejectsMalformedReceiptsAndReentry(t *testing.T) {
	for _, response := range []Receipt{{}, NewReceipt(ReceiptOptions{Attempted: true, Command: "other", Success: true, Steps: 1}), NewReceipt(ReceiptOptions{Attempted: true, Command: "query", Success: true, Steps: 2}), NewReceipt(ReceiptOptions{Attempted: true, Command: "query", Success: true, Steps: 1, Failure: NewFailure("device_error", "")}), NewReceipt(ReceiptOptions{Attempted: true, Command: "query", Success: false})} {
		s := controllerFixture()
		s.receipt = func(string) Receipt { return response }
		r, _ := NewReader(s, nil)
		cap, e := r.Read(t.Context(), "query")
		if e != nil || cap.Success() {
			t.Fatal(cap, e)
		}
	}
	s := controllerFixture()
	var r *Reader
	r, _ = NewReader(s, func() time.Time {
		if _, e := r.Read(t.Context(), "nested"); e == nil {
			t.Fatal("clock callback reentered")
		}
		return time.Now()
	})
	_, e := r.Read(t.Context(), "query")
	if e != nil || len(s.calls) != 1 {
		t.Fatal(e, s.calls)
	}
	s = controllerFixture()
	s.stateErr = errors.New("private state")
	r, _ = NewReader(s, nil)
	cap, e := r.Read(t.Context(), "query")
	if e != nil || !cap.Skipped() || cap.Failure().Code() != "session_state_failed" || len(s.calls) != 0 {
		t.Fatal(cap, e)
	}
}

func TestCaptureValidationAndReaderClockBoundaries(t *testing.T) {
	now := time.Now()
	body := "private body"
	for _, o := range []CaptureOptions{{Command: "bad\n", Body: &body, StartedAt: now, CompletedAt: now}, {Command: "query", Body: &body}, {Command: "query", StartedAt: now, CompletedAt: now}, {Command: "query", Body: &body, Failure: NewFailure("device_error", ""), StartedAt: now, CompletedAt: now}, {Command: "query", Skipped: true, Failure: NewFailure("device_error", ""), Evidence: []Record{{"bytes": 1}}, StartedAt: now, CompletedAt: now}} {
		if _, e := NewCapture(o); e == nil {
			t.Fatal("invalid capture accepted")
		}
	}
	if NewFailure("private credential string", "private stage").Code() != "invalid_command_result" {
		t.Fatal("unsafe failure accepted")
	}
	if _, e := NewReader(nil, nil); e == nil {
		t.Fatal("nil session accepted")
	}
	s := controllerFixture()
	r, _ := NewReader(s, func() time.Time { return time.Time{} })
	if _, e := r.Read(t.Context(), "query"); e == nil || len(s.calls) != 0 {
		t.Fatal("zero clock allowed I/O")
	}
	r, _ = NewReader(s, nil)
	if _, e := r.Read(t.Context(), "bad\n"); e == nil {
		t.Fatal("invalid query accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cap, e := r.Read(ctx, "query")
	if e != nil || !cap.Skipped() || cap.Failure().Code() != "cancelled" || len(s.calls) != 0 {
		t.Fatal(cap, e)
	}
	ticks := 0
	r, _ = NewReader(s, func() time.Time {
		ticks++
		if ticks == 2 {
			return time.Time{}
		}
		return now
	})
	cap, e = r.Read(t.Context(), "query")
	if e != nil || cap.Success() || len(cap.Evidence()) != 0 {
		t.Fatal(cap, e)
	}
}
func TestCollectorAPCredentialsFailureAndUnsupportedCommandViews(t *testing.T) {
	s := controllerFixture()
	apInventory(s)
	o := options(s, LLDP)
	o.APCredentials = func(context.Context) (APCredentials, error) {
		return APCredentials{}, errors.New("private credentials")
	}
	c := NewCollector(CollectorOptions{APFactory: func(context.Context, Session, APCredentials) (APReader, error) {
		t.Fatal("invalid credentials reached factory")
		return nil, nil
	}})
	snap := mustCollect(t, c, o)
	if code(snap.Dimension(LLDP)) != "invalid_ap_credentials" {
		t.Fatal(snap)
	}
	for _, body := range []string{"\xff", "% Unrecognized command found at '^' position.\n"} {
		s := controllerFixture()
		s.output["display wlan ap all verbose"] = body
		snap := mustCollect(t, NewCollector(CollectorOptions{}), options(s, AccessPoints, Radios, Clients))
		if snap.Dimension(AccessPoints).Complete() || !snap.Dimension(Clients).Complete() || !reflect.DeepEqual(snap.Dimension(AccessPoints).Evidence(), snap.Dimension(Radios).Evidence()) {
			t.Fatal(snap)
		}
	}
}

func TestTransportObjectsDoNotExposeCredentialsBodiesOrCallbacks(t *testing.T) {
	s := controllerFixture()
	r, _ := NewReader(s, func() time.Time { t.Fatal("formatter called clock"); return time.Now() })
	c := NewCollector(CollectorOptions{Clock: func() time.Time { t.Fatal("formatter called clock"); return time.Now() }})
	receipt := NewReceipt(ReceiptOptions{Attempted: true, Command: "query", Raw: "private receipt", Body: "private receipt", Success: true, Steps: 1})
	values := []any{receipt, r, c, APCredentials{Username: "private credential", Password: "private credential"}, APOutcome{Error: NewFailure("private error", "private stage")}, NewFailure("private error", "private stage")}
	for _, v := range values {
		j, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		text := fmt.Sprintf("%v %#v %s", v, v, j)
		if strings.Contains(text, "private") {
			t.Fatalf("secret leaked: %T", v)
		}
	}
	if receipt.Raw() != "private receipt" || receipt.Body() != "private receipt" {
		t.Fatal("explicit receipt access lost")
	}
}

func TestReviewOpenerDiagnosticsPreserveCancellationAndClose(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		codes []string
	}{
		{"cancel", context.Canceled, []string{"cancelled"}},
		{"deadline", context.DeadlineExceeded, []string{"deadline_exceeded"}},
		{"typed", NewFailure("transport_error", "close"), []string{"transport_error"}},
		{"cancel_close", errors.Join(context.Canceled, NewFailure("session_close_failed", "close")), []string{"cancelled", "session_close_failed"}},
		{"wrapped_join", fmt.Errorf("private: %w", errors.Join(context.DeadlineExceeded, NewFailure("session_close_failed", "close"))), []string{"deadline_exceeded", "session_close_failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := controllerFixture()
			o := options(s, Clients)
			o.Open = func(_ context.Context, run func(Session) error) error {
				if e := run(s); e != nil {
					return e
				}
				return tc.err
			}
			snapshot := mustCollect(t, NewCollector(CollectorOptions{}), o)
			if !snapshot.Dimension(Clients).Complete() {
				t.Fatal("lost first facts")
			}
			got := []string{}
			for _, issue := range snapshot.Dimension(Collection).Issues() {
				got = append(got, issue["code"].(string))
			}
			if !reflect.DeepEqual(got, tc.codes) {
				t.Fatalf("codes %v want %v", got, tc.codes)
			}
		})
	}
}
