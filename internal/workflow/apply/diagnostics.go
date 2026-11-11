package apply

import (
	"context"
	"errors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/nativeerr"
)

// publicationDiagnostic supplies the journal's safe publication metadata.
type publicationDiagnostic interface {
	publicationStatus
	ToMap() wireless.Record
}

// diagnostics retains independent, ordered safe evidence. The visitor
// follows joined/wrapped causes; no Error() text enters execution or stderr.
type diagnostics struct {
	fields map[string][]wireless.Record
	seen   map[string]map[string]bool
}

func (d *diagnostics) append(field string, entry wireless.Record) {
	if d.fields == nil {
		d.fields = map[string][]wireless.Record{}
		d.seen = map[string]map[string]bool{}
	}
	encoded, _ := wireless.MarshalJSON(entry)
	if d.seen[field] == nil {
		d.seen[field] = map[string]bool{}
	}
	key := string(encoded)
	if d.seen[field][key] {
		return
	}
	d.seen[field][key] = true
	d.fields[field] = append(d.fields[field], wireless.CloneRecord(entry))
}
func (d *diagnostics) failure(field string, f *wireless.FailureError) {
	if f == nil {
		return
	}
	if f.Code() == "cancelled" || f.Code() == "deadline_exceeded" {
		field = "cancellation_errors"
	}
	d.append(field, f.ToMap())
}
func (d *diagnostics) collect(err error, field, fallback, stage string) {
	var visit func(error, bool)
	//nolint:errorlint // visit walks the error tree itself; each node is classified on its own
	visit = func(err error, unknown bool) {
		if err == nil {
			return
		}
		switch e := err.(type) {
		case publicationDiagnostic:
			d.append("report_errors", e.ToMap())
			if errors.Is(e, context.Canceled) {
				d.failure(field, wireless.NewFailure("cancelled", stage))
			} else if errors.Is(e, context.DeadlineExceeded) {
				d.failure(field, wireless.NewFailure("deadline_exceeded", stage))
			}
			return
		case *wireless.FailureError:
			d.failure(field, nativeerr.Classify(e, fallback, stage))
			return
		}
		if failure, ok, cause := nativeerr.ClassifyNative(err, fallback, stage); ok {
			d.failure(field, failure)
			visit(cause, false)
			return
		}
		if many, ok := err.(interface{ Unwrap() []error }); ok {
			for _, child := range many.Unwrap() {
				visit(child, unknown)
			}
			return
		}
		if one, ok := err.(interface{ Unwrap() error }); ok && one.Unwrap() != nil {
			visit(one.Unwrap(), unknown)
			return
		}
		if unknown || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			d.failure(field, nativeerr.Classify(err, fallback, stage))
		}
	}
	visit(err, true)
}
func (d *diagnostics) attach(state wireless.Record) {
	for field, entries := range d.fields {
		owned := make([]wireless.Record, len(entries))
		for i, entry := range entries {
			owned[i] = wireless.CloneRecord(entry)
		}
		state[field] = owned
	}
	if entries := d.fields["report_errors"]; len(entries) > 0 {
		state["report_error"] = wireless.CloneRecord(entries[0])
	}
}
