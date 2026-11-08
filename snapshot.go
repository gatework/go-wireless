package wireless

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"
)

type SnapshotOptions struct {
	Target     Record
	Dimensions map[Dimension]Observation
	ObservedAt time.Time
}
type Snapshot struct {
	target     Record
	dimensions map[Dimension]Observation
	observedAt time.Time
}

func NewSnapshot(o SnapshotOptions) (Snapshot, error) {
	if o.Target == nil || len(o.Dimensions) == 0 || o.ObservedAt.IsZero() {
		return Snapshot{}, errors.New("invalid wireless snapshot")
	}
	v, err := copyJSON(o.Target, map[visit]bool{})
	if err != nil {
		return Snapshot{}, err
	}
	dims := make(map[Dimension]Observation, len(o.Dimensions))
	for n, obs := range o.Dimensions {
		if (!n.Valid() && n != Collection) || obs.Name() != n || obs.Status() == "" {
			return Snapshot{}, errors.New("invalid wireless snapshot")
		}
		dims[n] = obs
	}
	return Snapshot{v.(Record), dims, o.ObservedAt}, nil
}
func (s Snapshot) Target() Record { return cloneRecord(s.target) }
func (s Snapshot) Dimensions() map[Dimension]Observation {
	d := map[Dimension]Observation{}
	maps.Copy(d, s.dimensions)
	return d
}
func (s Snapshot) ObservedAt() time.Time { return s.observedAt }
func (s Snapshot) Dimension(n Dimension) Observation {
	if o, ok := s.dimensions[n]; ok {
		return o
	}
	o, _ := NewObservation(ObservationOptions{Name: n, Status: NotRequested})
	return o
}
func (s Snapshot) Records(n Dimension) []Record { return s.Dimension(n).Records() }
func (s Snapshot) Complete() bool {
	selected := false
	for _, o := range s.dimensions {
		if o.Status() == NotRequested {
			continue
		}
		selected = true
		if !o.Complete() {
			return false
		}
	}
	return selected
}
func (s Snapshot) String() string {
	return fmt.Sprintf("<wireless.Snapshot complete=%t>", s.Complete())
}
func (s Snapshot) GoString() string { return s.String() }
func (s Snapshot) ToMap() Record {
	return Record{"target": s.Target(), "dimensions": s.Dimensions(), "observed_at": formatTime(&s.observedAt)}
}
func (s Snapshot) MarshalJSON() ([]byte, error) { return MarshalJSON(s.ToMap()) }
func (s *Snapshot) UnmarshalJSON(data []byte) error {
	var wire struct {
		Target     json.RawMessage           `json:"target"`
		Dimensions map[Dimension]Observation `json:"dimensions"`
		ObservedAt time.Time                 `json:"observed_at"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return errors.New("invalid wireless snapshot JSON")
	}
	var target Record
	decoder := json.NewDecoder(bytes.NewReader(wire.Target))
	decoder.UseNumber()
	if err := decoder.Decode(&target); err != nil {
		return errors.New("invalid wireless snapshot target")
	}
	result, err := NewSnapshot(SnapshotOptions{target, wire.Dimensions, wire.ObservedAt})
	if err == nil {
		*s = result
	}
	return err
}
