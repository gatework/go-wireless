package collect

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

// OpenSession owns controller connection lifecycle. Callback invocations are synchronous;
// repeated or concurrently repeated invocations never replay collection.
type OpenSession func(context.Context, func(Session) error) error
type APCredentials struct{ Username, Password string }

func (APCredentials) String() string               { return "wireless.APCredentials" }
func (c APCredentials) GoString() string           { return c.String() }
func (APCredentials) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

type APCredentialsSource func(context.Context) (APCredentials, error)
type APOutcome struct {
	Result                          *wireless.Capture
	Error, CleanupError, CloseError *wireless.FailureError
	Skipped                         bool
}

func (APOutcome) String() string     { return "wireless.APOutcome" }
func (o APOutcome) GoString() string { return o.String() }

// APRequest associates the selected profile's query with one detached AP identity.
type APRequest struct {
	AccessPoint wireless.Record
	Query       parser.APQuery
}

type APReader interface {
	Read(ctx context.Context, request APRequest) (APOutcome, error)
}
type APFactory func(context.Context, Session, APCredentials) (APReader, error)
type CollectorOptions struct {
	Clock     Clock
	APFactory APFactory
}

// CollectOptions selects a validated parser profile and independently identifies
// the actual connector. Target and scope are copied before the opener is called.
type CollectOptions struct {
	Target            wireless.Record
	Profile           *parser.Profile
	ConnectorPlatform string
	Dimensions        []wireless.Dimension
	APNames           []string
	APCredentials     APCredentialsSource
	Open              OpenSession
}

// Collector acquires one device snapshot without owning a transport implementation.
type Collector struct {
	clock     Clock
	apFactory APFactory
}

func NewCollector(o CollectorOptions) *Collector {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	return &Collector{o.Clock, o.APFactory}
}
func (c *Collector) String() string   { return "wireless.Collector" }
func (c *Collector) GoString() string { return c.String() }
func (c *Collector) Collect(ctx context.Context, o CollectOptions) (wireless.Snapshot, error) {
	profile := o.Profile
	if !profile.Valid() {
		return wireless.Snapshot{}, errors.New("wireless: parser profile required")
	}
	ownedProfile := *profile
	profile = &ownedProfile
	if o.Target == nil || o.Open == nil {
		return wireless.Snapshot{}, errors.New("wireless: target and session opener required")
	}
	copied, e := wireless.CopyValue(o.Target)
	if e != nil {
		return wireless.Snapshot{}, errors.New("wireless: invalid target")
	}
	target, _ := copied.(map[string]any) // CopyValue preserves the map type
	scope, e := profile.CollectionScope(o.Dimensions)
	if e != nil {
		return wireless.Snapshot{}, e
	}
	names := append([]string(nil), o.APNames...)
	for _, n := range names {
		if !validAPName(n) {
			return wireless.Snapshot{}, errors.New("wireless: invalid AP selection")
		}
	}
	if !profile.MatchesConnector(o.ConnectorPlatform) {
		return wireless.Snapshot{}, errors.New("wireless: connector platform mismatch")
	}
	run := &collection{collector: c, profile: profile, scope: scope, apNames: names, credentials: o.APCredentials,
		observations: map[wireless.Dimension]wireless.Observation{}, stage: "connect"}
	for _, dimension := range scope.Unsupported() {
		run.observations[dimension] = c.unavailable(dimension, wireless.Unsupported, "unsupported_platform_dimension", nil)
	}
	var admission singleSession
	openErr := o.Open(ctx, func(session Session) error {
		if !admission.enter() {
			return nil
		}
		defer admission.leave()
		return run.session(ctx, session)
	})
	reentered := admission.close()
	if openErr != nil {
		run.openFailed(openErr)
	}
	if reentered {
		run.reentered()
	}
	return run.snapshot(target)
}

// singleSession admits one session callback and joins it when collection
// ends; a second or late callback is recorded as reentry.
type singleSession struct {
	mu                         sync.Mutex
	running                    sync.WaitGroup
	started, ended, reentrance bool
}

func (s *singleSession) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || s.started {
		s.reentrance = true
		return false
	}
	s.started = true
	s.running.Add(1)
	return true
}

func (s *singleSession) leave() { s.running.Done() }

// close ends admission, joins the admitted callback and reports reentry.
func (s *singleSession) close() bool {
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	s.running.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reentrance
}

// collection is one controller collection inside its session.
type collection struct {
	collector    *Collector
	profile      *parser.Profile
	scope        parser.CollectionScope
	apNames      []string
	credentials  APCredentialsSource
	observations map[wireless.Dimension]wireless.Observation
	stage        string
}

// session runs preparation, every query, then AP neighbors on session.
func (r *collection) session(ctx context.Context, session Session) error {
	c := r.collector
	reader, err := NewReader(session, c.clock)
	if err != nil {
		return wireless.NewFailure("session_state_failed", "session")
	}
	r.stage = "prepare"
	prep, err := reader.Read(ctx, r.profile.PreparationCommand())
	var prepFailure *wireless.FailureError
	if err != nil {
		prepFailure = wireless.FailureFrom(err, "command_execution_failed", "prepare")
	} else {
		prepFailure = prep.Failure()
	}
	for _, q := range r.scope.Queries() {
		r.query(ctx, reader, q, prepFailure)
	}
	if r.scope.APNeighbors() {
		failure := prepFailure
		if failure == nil {
			failure = reader.Failure(ctx)
		}
		if failure != nil {
			r.observations[wireless.LLDP] = c.unavailable(wireless.LLDP, wireless.Failed, failure.Code(), wireless.Record{"skipped": true, "stage": r.stage})
		} else {
			r.observations[wireless.LLDP] = c.collectNeighbors(ctx, r.profile, r.scope.APQuery(), session, r.observations[wireless.AccessPoints], r.apNames, r.credentials)
		}
	}
	r.stage = "close"
	return nil
}

// query runs one query, or marks its dimensions skipped after a failed
// preparation.
func (r *collection) query(ctx context.Context, reader *Reader, q parser.Query, prepFailure *wireless.FailureError) {
	c := r.collector
	if prepFailure != nil {
		for _, d := range q.Dimensions {
			r.observations[d] = c.unavailable(d, wireless.Failed, prepFailure.Code(), wireless.Record{"skipped": true, "stage": r.stage})
		}
		return
	}
	r.stage = "collect"
	capture, err := reader.Read(ctx, q.Command)
	if err != nil {
		for _, d := range q.Dimensions {
			r.observations[d] = c.unavailable(d, wireless.Failed, wireless.FailureFrom(err, "command_execution_failed", "execute").Code(), wireless.Record{"command": q.Command, "stage": "execute"})
		}
		return
	}
	c.collectCommand(r.observations, r.profile, q, capture)
}

// openFailed marks every unobserved dimension failed at the last stage and
// records the session failure codes.
func (r *collection) openFailed(openErr error) {
	c := r.collector
	codes := collectionFailureCodes(openErr, r.stage)
	for _, d := range r.scope.Dimensions() {
		if _, ok := r.observations[d]; !ok {
			r.observations[d] = c.unavailable(d, wireless.Failed, codes[0], wireless.Record{"skipped": true, "stage": r.stage})
		}
	}
	issues := make([]wireless.Record, 0, len(codes))
	for _, code := range codes {
		issues = append(issues, wireless.Record{"code": code})
	}
	completed := c.clock()
	r.observations[wireless.Collection], _ = wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Collection, Status: wireless.Failed, Issues: issues, CompletedAt: &completed})
}

// reentered records that the opener called back more than once.
func (r *collection) reentered() {
	c := r.collector
	prior, ok := r.observations[wireless.Collection]
	if !ok {
		r.observations[wireless.Collection] = c.unavailable(wireless.Collection, wireless.Failed, "session_reentered", nil)
		return
	}
	issues := append([]wireless.Record{{"code": "session_reentered"}}, prior.Issues()...)
	t := c.clock()
	r.observations[wireless.Collection], _ = wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Collection, Status: wireless.Failed, Issues: issues, CompletedAt: &t})
}

// snapshot fills dimensions the session never reached, derives the overall
// collection status and builds the snapshot.
func (r *collection) snapshot(target map[string]any) (wireless.Snapshot, error) {
	c := r.collector
	for _, d := range r.scope.Dimensions() {
		if _, ok := r.observations[d]; !ok {
			r.observations[d] = c.unavailable(d, wireless.Failed, "session_not_opened", wireless.Record{"skipped": true})
		}
	}
	if _, ok := r.observations[wireless.Collection]; !ok {
		status := wireless.Complete
		available := false
		for _, v := range r.observations {
			if !v.Complete() {
				status = wireless.Failed
			}
			available = available || v.Available()
		}
		if status != wireless.Complete && available {
			status = wireless.Partial
		}
		t := c.clock()
		observation, e := wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Collection, Status: status, CompletedAt: &t})
		if e != nil {
			return wireless.Snapshot{}, e
		}
		r.observations[wireless.Collection] = observation
	}
	return wireless.NewSnapshot(wireless.SnapshotOptions{Target: target, Dimensions: r.observations, ObservedAt: c.clock()})
}

func validAPName(s string) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func (c *Collector) unavailable(name wireless.Dimension, status wireless.Status, code string, details wireless.Record) wireless.Observation {
	issue := wireless.CloneRecord(details)
	if issue == nil {
		issue = wireless.Record{}
	}
	issue["code"] = code
	t := c.clock()
	o, _ := wireless.NewObservation(wireless.ObservationOptions{Name: name, Status: status, Issues: []wireless.Record{issue}, CompletedAt: &t})
	return o
}
func (c *Collector) collectCommand(out map[wireless.Dimension]wireless.Observation, p *parser.Profile, q parser.Query, captured wireless.Capture) {
	var parsed map[wireless.Dimension]wireless.Observation
	var err error
	if captured.Success() {
		parsed, err = p.ParseMany(q.Dimensions, captured.Body(), true)
	}
	for _, d := range q.Dimensions {
		var obs wireless.Observation
		if !captured.Success() {
			f := captured.Failure()
			details := f.ToMap()
			details["command"] = q.Command
			details["skipped"] = captured.Skipped()
			obs = c.unavailable(d, wireless.Failed, f.Code(), details)
		} else if err != nil {
			obs = c.unavailable(d, wireless.Failed, "command_processing_failed", wireless.Record{"stage": "parse", "command": q.Command})
		} else {
			obs = parsed[d]
		}
		start, end := captured.StartedAt(), captured.CompletedAt()
		out[d], _ = wireless.NewObservation(wireless.ObservationOptions{Name: d, Status: obs.Status(), Records: obs.Records(), Issues: obs.Issues(), Evidence: captured.Evidence(), StartedAt: &start, CompletedAt: &end})
	}
}

// Joined opener errors carry independent lifecycle facts in their original order.
// Only an untyped post-callback failure uses close-stage inference.
func collectionFailureCodes(err error, stage string) []string {
	codes := []string{}
	seen := map[string]bool{}
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if many, ok := err.(interface{ Unwrap() []error }); ok {
			for _, child := range many.Unwrap() {
				visit(child)
			}
			return
		}
		if one, ok := err.(interface{ Unwrap() error }); ok && one.Unwrap() != nil {
			visit(one.Unwrap())
			return
		}
		fallback := "connection_failed"
		if stage == "close" {
			fallback = "session_close_failed"
		}
		code := wireless.FailureFrom(err, fallback, stage).Code()
		if !seen[code] {
			codes = append(codes, code)
			seen[code] = true
		}
	}
	visit(err)
	if len(codes) == 0 {
		codes = append(codes, "connection_failed")
	}
	return codes
}
