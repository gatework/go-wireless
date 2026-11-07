package wireless

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
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
	Result                          *Capture
	Error, CleanupError, CloseError *Failure
	Skipped                         bool
}

func (APOutcome) String() string     { return "wireless.APOutcome" }
func (o APOutcome) GoString() string { return o.String() }

type APReader interface {
	Read(context.Context, Record) (APOutcome, error)
}
type APFactory func(context.Context, Session, APCredentials) (APReader, error)
type CollectorOptions struct {
	Clock     Clock
	APFactory APFactory
}
type CollectOptions struct {
	Target                             Record
	Platform, Model, ConnectorPlatform string
	Dimensions                         []Dimension
	APNames                            []string
	APCredentials                      APCredentialsSource
	Open                               OpenSession
}
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
func (c *Collector) unavailable(name Dimension, status Status, code string, details Record) Observation {
	issue := cloneRecord(details)
	if issue == nil {
		issue = Record{}
	}
	issue["code"] = code
	t := c.clock()
	o, _ := NewObservation(ObservationOptions{Name: name, Status: status, Issues: []Record{issue}, CompletedAt: &t})
	return o
}
func validAPName(s string) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func containsDimension(ds []Dimension, d Dimension) bool {
	for _, v := range ds {
		if v == d {
			return true
		}
	}
	return false
}
func (c *Collector) Collect(ctx context.Context, o CollectOptions) (Snapshot, error) {
	profile, e := NewProfile(o.Platform, o.Model)
	if e != nil {
		return Snapshot{}, e
	}
	if o.Target == nil || o.Open == nil {
		return Snapshot{}, errors.New("wireless: target and session opener required")
	}
	copied, e := copyJSON(o.Target, map[visit]bool{})
	if e != nil {
		return Snapshot{}, errors.New("wireless: invalid target")
	}
	target := copied.(map[string]any)
	dims := append([]Dimension(nil), o.Dimensions...)
	if o.Dimensions == nil {
		for _, d := range profile.Dimensions() {
			if d != LLDP {
				dims = append(dims, d)
			}
		}
	}
	if len(dims) == 0 {
		return Snapshot{}, errors.New("wireless: empty collection scope")
	}
	seen := map[Dimension]bool{}
	supported := SupportedDimensions()
	for _, d := range dims {
		if seen[d] || !containsDimension(supported, d) || d == Collection {
			return Snapshot{}, errors.New("wireless: invalid collection scope")
		}
		seen[d] = true
	}
	names := append([]string(nil), o.APNames...)
	for _, n := range names {
		if !validAPName(n) {
			return Snapshot{}, errors.New("wireless: invalid AP selection")
		}
	}
	peer, e := NewProfile(o.ConnectorPlatform, o.Model)
	if e != nil || peer.Platform() != profile.Platform() {
		return Snapshot{}, errors.New("wireless: connector platform mismatch")
	}
	if containsDimension(dims, LLDP) && !containsDimension(dims, AccessPoints) {
		dims = append(dims, AccessPoints)
	}
	if (containsDimension(dims, APGroups) || containsDimension(dims, ServiceTemplates)) && !containsDimension(dims, Configuration) {
		dims = append(dims, Configuration)
	}
	observations := map[Dimension]Observation{}
	queryDims := []Dimension{}
	for _, d := range dims {
		if _, err := profile.Command(d); err != nil {
			observations[d] = c.unavailable(d, Unsupported, "unsupported_platform_dimension", nil)
		} else if d != LLDP {
			queryDims = append(queryDims, d)
		}
	}
	queries, e := profile.Queries(queryDims)
	if e != nil {
		return Snapshot{}, e
	}
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
			return NewFailure("session_state_failed", "session")
		}
		stage = "prepare"
		prep, err := reader.Read(ctx, profile.PreparationCommand())
		var prepFailure *Failure
		if err != nil {
			prepFailure = safeFailure(err, "command_execution_failed", "prepare")
		} else {
			prepFailure = prep.Failure()
		}
		for _, q := range queries {
			if prepFailure != nil {
				for _, d := range q.Dimensions {
					observations[d] = c.unavailable(d, Failed, prepFailure.Code(), Record{"skipped": true, "stage": stage})
				}
				continue
			}
			stage = "collect"
			capture, err := reader.Read(ctx, q.Command)
			if err != nil {
				for _, d := range q.Dimensions {
					observations[d] = c.unavailable(d, Failed, safeFailure(err, "command_execution_failed", "execute").Code(), Record{"command": q.Command, "stage": "execute"})
				}
				continue
			}
			c.collectCommand(observations, profile, q, capture)
		}
		if containsDimension(dims, LLDP) {
			if _, err := profile.Command(LLDP); err == nil {
				failure := prepFailure
				if failure == nil {
					failure = reader.Failure(ctx)
				}
				if failure != nil {
					observations[LLDP] = c.unavailable(LLDP, Failed, failure.Code(), Record{"skipped": true, "stage": stage})
				} else {
					observations[LLDP] = c.collectNeighbors(ctx, session, observations[AccessPoints], names, o.APCredentials)
				}
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
				observations[d] = c.unavailable(d, Failed, first, Record{"skipped": true, "stage": stage})
			}
		}
		issues := make([]Record, 0, len(codes))
		for _, code := range codes {
			issues = append(issues, Record{"code": code})
		}
		completed := c.clock()
		observations[Collection], _ = NewObservation(ObservationOptions{Name: Collection, Status: Failed, Issues: issues, CompletedAt: &completed})
	}
	mu.Lock()
	repeat := reentered
	mu.Unlock()
	if repeat {
		if prior, ok := observations[Collection]; ok {
			issues := append([]Record{{"code": "session_reentered"}}, prior.Issues()...)
			t := c.clock()
			observations[Collection], _ = NewObservation(ObservationOptions{Name: Collection, Status: Failed, Issues: issues, CompletedAt: &t})
		} else {
			observations[Collection] = c.unavailable(Collection, Failed, "session_reentered", nil)
		}
	}
	for _, d := range dims {
		if _, ok := observations[d]; !ok {
			observations[d] = c.unavailable(d, Failed, "session_not_opened", Record{"skipped": true})
		}
	}
	if _, ok := observations[Collection]; !ok {
		status := Complete
		available := false
		for _, v := range observations {
			if !v.Complete() {
				status = Failed
			}
			available = available || v.Available()
		}
		if status != Complete && available {
			status = Partial
		}
		t := c.clock()
		observations[Collection], e = NewObservation(ObservationOptions{Name: Collection, Status: status, CompletedAt: &t})
		if e != nil {
			return Snapshot{}, e
		}
	}
	return NewSnapshot(SnapshotOptions{Target: target, Dimensions: observations, ObservedAt: c.clock()})
}
func (c *Collector) collectCommand(out map[Dimension]Observation, p *Profile, q Query, cap Capture) {
	var parsed map[Dimension]Observation
	var err error
	if cap.Success() {
		parsed, err = p.ParseMany(q.Dimensions, cap.Body(), true)
	}
	for _, d := range q.Dimensions {
		var obs Observation
		if !cap.Success() {
			f := cap.Failure()
			details := f.ToMap()
			details["command"] = q.Command
			details["skipped"] = cap.Skipped()
			obs = c.unavailable(d, Failed, f.Code(), details)
		} else if err != nil {
			obs = c.unavailable(d, Failed, "command_processing_failed", Record{"stage": "parse", "command": q.Command})
		} else {
			obs = parsed[d]
		}
		start, end := cap.StartedAt(), cap.CompletedAt()
		out[d], _ = NewObservation(ObservationOptions{Name: d, Status: obs.Status(), Records: obs.Records(), Issues: obs.Issues(), Evidence: cap.Evidence(), StartedAt: &start, CompletedAt: &end})
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
		code := safeFailure(err, fallback, stage).Code()
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
