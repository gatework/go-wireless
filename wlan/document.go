package wlan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gatework/go-netops/evidence"
)

// ErrDocument reports a controller report that lacks identity or dimensions.
var ErrDocument = errors.New("wlan: invalid controller document")

// DecodeDocument builds a controller from a collected report: "device"
// identity and "snapshot.dimensions" observations. The envelope is strict:
// device name and host are strings, snapshot is present (null for a report
// without evidence), every dimension has a valid status and records and
// issues are arrays of objects. The "collection" pseudo-dimension and unknown
// dimensions are then ignored; fields inside records are judged per
// dimension, an unmodeled one making that dimension partial.
func DecodeDocument(body []byte) (*Controller, error) {
	var doc struct {
		Device struct {
			Identity
			Name *string `json:"name"`
		} `json:"device"`
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDocument, err)
	}
	if doc.Device.Host == "" || doc.Device.Name == nil {
		return nil, fmt.Errorf("%w: device name and host", ErrDocument)
	}
	doc.Device.Identity.Name = *doc.Device.Name
	if doc.Snapshot == nil {
		return nil, fmt.Errorf("%w: missing snapshot", ErrDocument)
	}
	var snapshot *struct {
		Dimensions *map[evidence.Dimension]*struct {
			Status  evidence.Status    `json:"status"`
			Records *[]json.RawMessage `json:"records"`
			Issues  *[]json.RawMessage `json:"issues"`
		} `json:"dimensions"`
	}
	if err := json.Unmarshal(doc.Snapshot, &snapshot); err != nil {
		return nil, fmt.Errorf("%w: snapshot: %w", ErrDocument, err)
	}
	dims := map[evidence.Dimension]Observed{}
	if snapshot != nil {
		if snapshot.Dimensions == nil {
			return nil, fmt.Errorf("%w: missing snapshot dimensions", ErrDocument)
		}
		for name, o := range *snapshot.Dimensions {
			if o == nil || !o.Status.Valid() || o.Records == nil || !objects(*o.Records) {
				return nil, fmt.Errorf("%w: dimension %s", ErrDocument, name)
			}
			if o.Issues != nil && !objects(*o.Issues) {
				return nil, fmt.Errorf("%w: dimension %s issues", ErrDocument, name)
			}
			if name == "collection" {
				continue
			}
			var issues []json.RawMessage
			if o.Issues != nil {
				issues = *o.Issues
			}
			dims[name] = Observed{Status: o.Status, Records: *o.Records, Issues: issues}
		}
	}
	return NewController(doc.Device.Identity, dims), nil
}

// objects reports whether every raw value is a JSON object.
func objects(values []json.RawMessage) bool {
	for _, v := range values {
		if t := bytes.TrimLeft(v, " \t\r\n"); len(t) == 0 || t[0] != '{' {
			return false
		}
	}
	return true
}
