package wireless

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// CoverageOptions configures NewCoverage.
type CoverageOptions struct {
	Observations, Projection Record
	Dimensions               []Dimension
	Now                      time.Time
	StaleAfter               float64
}

// Coverage reports, per dimension, whether the latest evidence is current,
// stale or unknown, from stored observations and a projection.
type Coverage struct {
	observations, projection Record
	dimensions               []Dimension
	now                      time.Time
	staleAfter               float64
}

// NewCoverage validates the options; StaleAfter is in seconds.
func NewCoverage(o CoverageOptions) (Coverage, error) {
	if o.Now.IsZero() || math.IsNaN(o.StaleAfter) || math.IsInf(o.StaleAfter, 0) || o.StaleAfter < 0 {
		return Coverage{}, errors.New("coverage requires a clock time and finite nonnegative freshness window")
	}
	var obs, proj Record
	if o.Observations != nil {
		v, err := copyJSON(o.Observations, map[visit]bool{})
		if err != nil {
			return Coverage{}, err
		}
		obs, _ = v.(Record)
	}
	if o.Projection != nil {
		v, err := copyJSON(o.Projection, map[visit]bool{})
		if err != nil {
			return Coverage{}, err
		}
		proj, _ = v.(Record)
	}
	return Coverage{obs, proj, append([]Dimension{}, o.Dimensions...), o.Now, o.StaleAfter}, nil
}

// ToMap returns the state of every dimension.
func (c Coverage) ToMap() Record {
	if c.observations == nil {
		return Record{"state": "untracked", "source": "inventory", "dimensions": Record{}}
	}
	dims := Record{}
	state := "complete"
	if len(c.dimensions) == 0 {
		state = "unknown"
	}
	for _, n := range c.dimensions {
		entry := c.dimension(string(n))
		dims[string(n)] = entry
		if entry["state"] == "unknown" {
			state = "unknown"
		} else if entry["state"] == "stale" && state != "unknown" {
			state = "stale"
		}
	}
	return cloneRecord(Record{"state": state, "source": "cli", "dimensions": dims})
}

// MarshalJSON encodes the coverage states.
func (c Coverage) MarshalJSON() ([]byte, error) { return MarshalJSON(c.ToMap()) }
func (c Coverage) String() string               { return "<wireless.Coverage>" }

// GoString formats metadata only.
func (c Coverage) GoString() string { return c.String() }
func (c Coverage) dimension(n string) Record {
	stored := asRecord(c.observations[n])
	latest := asRecord(stored["latest"])
	projection := asRecord(c.projection[n])
	at := parseTimestamp(firstPresent(latest, "completed_at", "observed_at"))
	projectedAt := parseTimestamp(projection["observed_at"])
	last := asRecord(stored["last_complete"])
	if latest["status"] == "complete" {
		last = latest
	}
	state := "unknown"
	reason := ""
	_, hasProj := c.projection[n]
	switch {
	case latest["status"] != "complete":
		if len(latest) == 0 {
			reason = "not_requested"
		} else {
			reason = "collection_" + fmt.Sprint(latest["status"])
			if latest["status"] == nil {
				reason = "collection_"
			}
		}
	case at == nil || at.After(c.now):
		reason = "observation_time_unknown"
	case c.now.Sub(*at).Seconds() > c.staleAfter:
		state = "stale"
		reason = "evidence_stale"
	case hasProj && (projectedAt == nil || !projectedAt.Equal(*at)):
		reason = "projection_time_unconfirmed"
	case hasProj && projection["status"] != "complete":
		reason = "projection_incomplete"
	default:
		state = "complete"
	}
	out := Record{"state": state, "status": "not_requested"}
	if latest["status"] != nil && latest["status"] != false {
		out["status"] = latest["status"]
	}
	if reason != "" {
		out["reason"] = reason
	}
	if at != nil {
		out["observed_at"] = formatTime(at)
	}
	if t := parseTimestamp(firstPresent(last, "completed_at", "observed_at")); t != nil {
		out["last_complete_at"] = formatTime(t)
	}
	if projection["status"] != nil {
		out["projection_status"] = projection["status"]
	}
	if projectedAt != nil {
		out["projection_observed_at"] = formatTime(projectedAt)
	}
	return out
}

func firstPresent(m Record, keys ...string) any {
	for _, k := range keys {
		if m[k] != nil && m[k] != false {
			return m[k]
		}
	}
	return nil
}
func parseTimestamp(v any) *time.Time {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		return nil
	}
	return &t
}

// asRecord returns v as a Record, or nil (which reads as empty) otherwise.
func asRecord(v any) Record {
	m, _ := v.(Record)
	return m
}
