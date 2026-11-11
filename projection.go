package wireless

import (
	"errors"
	"fmt"
)

// Projection is the status of every dimension a parser's output projects,
// used to judge coverage without the facts.
type Projection struct{ dimensions Record }

// NewProjection validates that every dimension entry carries a status.
func NewProjection(dimensions Record) (Projection, error) {
	if dimensions == nil {
		dimensions = Record{}
	}
	v, err := copyJSON(dimensions, map[visit]bool{})
	if err != nil {
		return Projection{}, err
	}
	owned, _ := v.(Record)
	for _, entry := range owned {
		m, ok := entry.(Record)
		if !ok || m["status"] == nil {
			return Projection{}, errors.New("invalid wireless projection")
		}
	}
	return Projection{owned}, nil
}

// Dimensions returns a copy of the per-dimension entries.
func (p Projection) Dimensions() Record {
	if p.dimensions == nil {
		return Record{}
	}
	return cloneRecord(p.dimensions)
}

// Complete reports whether every projected dimension is complete.
func (p Projection) Complete() bool {
	for _, v := range p.dimensions {
		if entry, _ := v.(Record); fmt.Sprint(entry["status"]) != "complete" {
			return false
		}
	}
	return true
}
func (p Projection) String() string {
	return fmt.Sprintf("<wireless.Projection dimensions=%d>", len(p.dimensions))
}

// GoString formats metadata only.
func (p Projection) GoString() string { return p.String() }

// MarshalJSON encodes the per-dimension entries.
func (p Projection) MarshalJSON() ([]byte, error) {
	return MarshalJSON(Record{"dimensions": p.Dimensions()})
}
