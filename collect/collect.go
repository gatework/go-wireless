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
	Error, CleanupError, CloseError *wireless.Failure
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
	Read(context.Context, APRequest) (APOutcome, error)
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
func validAPName(s string) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
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
	target := copied.(map[string]any)
	scope, e := profile.CollectionScope(o.Dimensions)
	if e != nil {
		return wireless.Snapshot{}, e
	}
	dims := scope.Dimensions()
	names := append([]string(nil), o.APNames...)
	for _, n := range names {
		if !validAPName(n) {
			return wireless.Snapshot{}, errors.New("wireless: invalid AP selection")
		}
	}
	if !profile.MatchesConnector(o.ConnectorPlatform) {
		return wireless.Snapshot{}, errors.New("wireless: connector platform mismatch")
	}
	observations := map[wireless.Dimension]wireless.Observation{}
	for _, dimension := range scope.Unsupported() {
		observations[dimension] = c.unavailable(dimension, wireless.Unsupported, "unsupported_platform_dimension", nil)
	}
	queries := scope.Queries()
	var mu sync.Mutex
	var running sync.WaitGroup
	started, ended, reentered := false, false, false
	stage := "connect"
	openErr := o.Open(ctx, func(session Session) error {
		mu.Lock()
		if ended || started {
			reentered = true
			mu.Unlock()
			return nil
		}
		started = true
		running.Add(1)
		mu.Unlock()
		defer running.Done()
		reader, err := NewReader(session, c.clock)
		if err != nil {
			return wireless.NewFailure("session_state_failed", "session")
		}
		stage = "prepare"
		prep, err := reader.Read(ctx, profile.PreparationCommand())
		var prepFailure *wireless.Failure
		if err != nil {
			prepFailure = wireless.FailureFrom(err, "command_execution_failed", "prepare")
		} else {
			prepFailure = prep.Failure()
		}
		for _, q := range queries {
			if prepFailure != nil {
				for _, d := range q.Dimensions {
					observations[d] = c.unavailable(d, wireless.Failed, prepFailure.Code(), wireless.Record{"skipped": true, "stage": stage})
				}
				continue
			}
			stage = "collect"
			capture, err := reader.Read(ctx, q.Command)
			if err != nil {
				for _, d := range q.Dimensions {
					observations[d] = c.unavailable(d, wireless.Failed, wireless.FailureFrom(err, "command_execution_failed", "execute").Code(), wireless.Record{"command": q.Command, "stage": "execute"})
				}
				continue
			}
			c.collectCommand(observations, profile, q, capture)
		}
		if scope.APNeighbors() {
			failure := prepFailure
			if failure == nil {
				failure = reader.Failure(ctx)
			}
			if failure != nil {
				observations[wireless.LLDP] = c.unavailable(wireless.LLDP, wireless.Failed, failure.Code(), wireless.Record{"skipped": true, "stage": stage})
			} else {
				observations[wireless.LLDP] = c.collectNeighbors(ctx, profile, scope.APQuery(), session, observations[wireless.AccessPoints], names, o.APCredentials)
			}
		}
		stage = "close"
		return nil
	})
	mu.Lock()
	ended = true
	mu.Unlock()
	running.Wait()
	if openErr != nil {
		codes := collectionFailureCodes(openErr, stage)
		first := codes[0]
		for _, d := range dims {
			if _, ok := observations[d]; !ok {
				observations[d] = c.unavailable(d, wireless.Failed, first, wireless.Record{"skipped": true, "stage": stage})
			}
		}
		issues := make([]wireless.Record, 0, len(codes))
		for _, code := range codes {
			issues = append(issues, wireless.Record{"code": code})
		}
		completed := c.clock()
		observations[wireless.Collection], _ = wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Collection, Status: wireless.Failed, Issues: issues, CompletedAt: &completed})
	}
	mu.Lock()
	repeat := reentered
	mu.Unlock()
	if repeat {
		if prior, ok := observations[wireless.Collection]; ok {
			issues := append([]wireless.Record{{"code": "session_reentered"}}, prior.Issues()...)
			t := c.clock()
			observations[wireless.Collection], _ = wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Collection, Status: wireless.Failed, Issues: issues, CompletedAt: &t})
		} else {
			observations[wireless.Collection] = c.unavailable(wireless.Collection, wireless.Failed, "session_reentered", nil)
		}
	}
	for _, d := range dims {
		if _, ok := observations[d]; !ok {
			observations[d] = c.unavailable(d, wireless.Failed, "session_not_opened", wireless.Record{"skipped": true})
		}
	}
	if _, ok := observations[wireless.Collection]; !ok {
		status := wireless.Complete
		available := false
		for _, v := range observations {
			if !v.Complete() {
				status = wireless.Failed
			}
			available = available || v.Available()
		}
		if status != wireless.Complete && available {
			status = wireless.Partial
		}
		t := c.clock()
		observations[wireless.Collection], e = wireless.NewObservation(wireless.ObservationOptions{Name: wireless.Collection, Status: status, CompletedAt: &t})
		if e != nil {
			return wireless.Snapshot{}, e
		}
	}
	return wireless.NewSnapshot(wireless.SnapshotOptions{Target: target, Dimensions: observations, ObservedAt: c.clock()})
}
func (c *Collector) collectCommand(out map[wireless.Dimension]wireless.Observation, p *parser.Profile, q parser.Query, cap wireless.Capture) {
	var parsed map[wireless.Dimension]wireless.Observation
	var err error
	if cap.Success() {
		parsed, err = p.ParseMany(q.Dimensions, cap.Body(), true)
	}
	for _, d := range q.Dimensions {
		var obs wireless.Observation
		if !cap.Success() {
			f := cap.Failure()
			details := f.ToMap()
			details["command"] = q.Command
			details["skipped"] = cap.Skipped()
			obs = c.unavailable(d, wireless.Failed, f.Code(), details)
		} else if err != nil {
			obs = c.unavailable(d, wireless.Failed, "command_processing_failed", wireless.Record{"stage": "parse", "command": q.Command})
		} else {
			obs = parsed[d]
		}
		start, end := cap.StartedAt(), cap.CompletedAt()
		out[d], _ = wireless.NewObservation(wireless.ObservationOptions{Name: d, Status: obs.Status(), Records: obs.Records(), Issues: obs.Issues(), Evidence: cap.Evidence(), StartedAt: &start, CompletedAt: &end})
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
