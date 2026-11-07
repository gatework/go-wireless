package cli

import (
	"context"
	"errors"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/adapter"
	"github.com/gatework/go-wireless/report"
)

// applyDiagnostics retains independent, ordered safe evidence. The visitor
// follows joined/wrapped causes; no Error() text enters execution or stderr.
type applyDiagnostics struct {
	fields wireless.Record
	seen   map[string]map[string]bool
}

func (d *applyDiagnostics) append(field string, entry wireless.Record) {
	if d.fields == nil {
		d.fields = wireless.Record{}
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
	entries, _ := d.fields[field].([]wireless.Record)
	d.fields[field] = append(entries, entry)
}
func (d *applyDiagnostics) failure(field string, f *wireless.Failure) {
	if f == nil {
		return
	}
	if f.Code() == "cancelled" || f.Code() == "deadline_exceeded" {
		field = "cancellation_errors"
	}
	d.append(field, f.ToMap())
}
func (d *applyDiagnostics) collect(err error, field, fallback, stage string) {
	var visit func(error, bool)
	visit = func(err error, unknown bool) {
		if err == nil {
			return
		}
		switch e := err.(type) {
		case *report.Error:
			d.append("report_errors", e.ToMap())
			if errors.Is(e, context.Canceled) {
				d.failure(field, wireless.NewFailure("cancelled", stage))
			} else if errors.Is(e, context.DeadlineExceeded) {
				d.failure(field, wireless.NewFailure("deadline_exceeded", stage))
			}
			return
		case *wireless.Failure:
			d.failure(field, adapter.SafeFailure(e, fallback, stage))
			return
		case *connector.Error:
			d.failure(field, adapter.SafeFailure(e, fallback, stage))
			visit(e.Unwrap(), false)
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
			d.failure(field, adapter.SafeFailure(err, fallback, stage))
		}
	}
	visit(err, true)
}
func (d *applyDiagnostics) attach(state wireless.Record) {
	for field, value := range d.fields {
		state[field] = value
	}
	if entries, ok := d.fields["report_errors"].([]wireless.Record); ok && len(entries) > 0 {
		state["report_error"] = entries[0]
	}
}
