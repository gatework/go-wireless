package wireless

import (
	"errors"
	"fmt"
)

type Projection struct{ dimensions Record }

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
func (p Projection) Dimensions() Record {
	if p.dimensions == nil {
		return Record{}
	}
	return cloneRecord(p.dimensions)
}
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
func (p Projection) GoString() string { return p.String() }
func (p Projection) MarshalJSON() ([]byte, error) {
	return MarshalJSON(Record{"dimensions": p.Dimensions()})
}
