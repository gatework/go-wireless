package wireless

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"time"
)

type Record = map[string]any
type Dimension string
type Status string

const (
	AccessPoints      Dimension = "access_points"
	Radios            Dimension = "radios"
	BSSes             Dimension = "bsses"
	Clients           Dimension = "clients"
	Configuration     Dimension = "configuration"
	APGroups          Dimension = "ap_groups"
	ServiceTemplates  Dimension = "service_templates"
	Dot1xConnections  Dimension = "dot1x_connections"
	MABConnections    Dimension = "mab_connections"
	BlockedClients    Dimension = "blocked_clients"
	ClientIPConflicts Dimension = "client_ip_conflicts"
	APModels          Dimension = "ap_models"
	LLDP              Dimension = "lldp"
	Collection        Dimension = "collection"
	Complete          Status    = "complete"
	Partial           Status    = "partial"
	Failed            Status    = "failed"
	Unsupported       Status    = "unsupported"
	NotRequested      Status    = "not_requested"
)

type ObservationOptions struct {
	Name                   Dimension
	Status                 Status
	Records                []Record
	Issues                 []Record
	Evidence               []Record
	StartedAt, CompletedAt *time.Time
}
type Observation struct {
	name                      Dimension
	status                    Status
	records, issues, evidence []Record
	startedAt, completedAt    *time.Time
}

func NewObservation(o ObservationOptions) (Observation, error) {
	if o.Name == "" {
		return Observation{}, errors.New("wireless observation requires a name")
	}
	switch o.Status {
	case Complete, Partial, Failed, Unsupported, NotRequested:
	default:
		return Observation{}, errors.New("invalid wireless observation status")
	}
	if (o.Status == Failed || o.Status == Unsupported || o.Status == NotRequested) && len(o.Records) > 0 {
		return Observation{}, errors.New("unavailable wireless observation has records")
	}
	r, err := copyRecords(o.Records)
	if err != nil {
		return Observation{}, err
	}
	i, err := copyRecords(o.Issues)
	if err != nil {
		return Observation{}, err
	}
	e, err := copyRecords(o.Evidence)
	if err != nil {
		return Observation{}, err
	}
	return Observation{o.Name, o.Status, r, i, e, copyTime(o.StartedAt), copyTime(o.CompletedAt)}, nil
}
func (o Observation) Name() Dimension         { return o.name }
func (o Observation) Status() Status          { return o.status }
func (o Observation) Records() []Record       { v, _ := copyRecords(o.records); return v }
func (o Observation) Issues() []Record        { v, _ := copyRecords(o.issues); return v }
func (o Observation) Evidence() []Record      { v, _ := copyRecords(o.evidence); return v }
func (o Observation) StartedAt() *time.Time   { return copyTime(o.startedAt) }
func (o Observation) CompletedAt() *time.Time { return copyTime(o.completedAt) }
func (o Observation) Complete() bool          { return o.status == Complete }
func (o Observation) Partial() bool           { return o.status == Partial }
func (o Observation) Available() bool         { return o.Complete() || o.Partial() }
func (o Observation) String() string {
	return fmt.Sprintf("<wireless.Observation name=%s status=%s records=%d>", safeName(string(o.name)), safeName(string(o.status)), len(o.records))
}
func (o Observation) GoString() string             { return o.String() }
func (o Observation) MarshalJSON() ([]byte, error) { return MarshalJSON(o.ToMap()) }
func (o Observation) ToMap() Record {
	return Record{"name": o.name, "status": o.status, "records": o.Records(), "issues": o.Issues(), "evidence": o.Evidence(), "started_at": formatTime(o.startedAt), "completed_at": formatTime(o.completedAt)}
}
func (o *Observation) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name        Dimension       `json:"name"`
		Status      Status          `json:"status"`
		Records     json.RawMessage `json:"records"`
		Issues      json.RawMessage `json:"issues"`
		Evidence    json.RawMessage `json:"evidence"`
		StartedAt   *time.Time      `json:"started_at"`
		CompletedAt *time.Time      `json:"completed_at"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return errors.New("invalid wireless observation JSON")
	}
	options := ObservationOptions{Name: wire.Name, Status: wire.Status, StartedAt: wire.StartedAt, CompletedAt: wire.CompletedAt}
	for _, entry := range []struct {
		raw    json.RawMessage
		target *[]Record
	}{{wire.Records, &options.Records}, {wire.Issues, &options.Issues}, {wire.Evidence, &options.Evidence}} {
		if len(entry.raw) == 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(entry.raw), []byte("null")) {
			return errors.New("invalid wireless observation records")
		}
		decoder := json.NewDecoder(bytes.NewReader(entry.raw))
		decoder.UseNumber()
		if err := decoder.Decode(entry.target); err != nil {
			return errors.New("invalid wireless observation records")
		}
	}
	result, err := NewObservation(options)
	if err == nil {
		*o = result
	}
	return err
}
func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
func formatTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02T15:04:05.000000Z07:00")
}
func safeName(s string) string {
	if len(s) > 80 {
		return "invalid"
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			if r != '_' {
				return "invalid"
			}
		}
	}
	return s
}
func copyRecords(a []Record) ([]Record, error) {
	out := make([]Record, len(a))
	for n, r := range a {
		if r == nil {
			return nil, errors.New("wireless records must be objects")
		}
		v, err := copyJSON(r, make(map[visit]bool))
		if err != nil {
			return nil, err
		}
		out[n], _ = v.(Record)
	}
	return out, nil
}

type visit struct {
	kind reflect.Kind
	ptr  uintptr
}

var jsonNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`)

// copyJSON deep-copies a JSON-compatible value, rejecting invalid numbers,
// cycles, non-text keys and other types.
//
//nolint:nilnil // nil is JSON null
func copyJSON(v any, seen map[visit]bool) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case string, bool:
		return v, nil
	case Dimension:
		return string(x), nil
	case Status:
		return string(x), nil
	case json.Number:
		if !jsonNumberPattern.MatchString(string(x)) {
			return nil, errors.New("invalid wireless JSON number")
		}
		return x, nil
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v, nil
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(r.Float()) || math.IsInf(r.Float(), 0) {
			return nil, errors.New("invalid wireless JSON number")
		}
		return v, nil
	case reflect.Map, reflect.Slice:
		if r.IsNil() {
			return nil, nil
		}
		key := visit{r.Kind(), r.Pointer()}
		if seen[key] {
			return nil, errors.New("cyclic wireless data")
		}
		seen[key] = true
		defer delete(seen, key)
		if r.Kind() == reflect.Map {
			return copyMap(v, r, seen)
		}
		return copySlice(r, seen)
	default:
		return nil, errors.New("wireless data must be JSON compatible")
	}
}

func copyMap(v any, r reflect.Value, seen map[visit]bool) (any, error) {
	if r.Type().Key().Kind() != reflect.String {
		return nil, errors.New("wireless object keys must be text")
	}
	out := make(Record, r.Len())
	// Evidence already uses Record. Native iteration avoids allocating
	// reflected keys and values for every field; named maps retain the
	// general path below, under the same cycle and value validation.
	if record, ok := v.(Record); ok {
		for key, child := range record {
			value, err := copyJSON(child, seen)
			if err != nil {
				return nil, err
			}
			out[key] = value
		}
		return out, nil
	}
	for it := r.MapRange(); it.Next(); {
		value, err := copyJSON(it.Value().Interface(), seen)
		if err != nil {
			return nil, err
		}
		out[it.Key().String()] = value
	}
	return out, nil
}

func copySlice(r reflect.Value, seen map[visit]bool) (any, error) {
	out := make([]any, r.Len())
	for i := range out {
		value, err := copyJSON(r.Index(i).Interface(), seen)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}
func cloneRecord(r Record) Record {
	if r == nil {
		return nil
	}
	v, _ := copyJSON(r, map[visit]bool{})
	clone, _ := v.(Record)
	return clone
}
