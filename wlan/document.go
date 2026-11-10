package wlan

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gatework/go-netops/evidence"
)

// ErrDocument reports a controller report that lacks identity or dimensions.
var ErrDocument = errors.New("wlan: invalid controller document")

// DecodeDocument builds a controller from a collected report: "device"
// identity and "snapshot.dimensions" observations. The "collection"
// pseudo-dimension and unknown dimensions are ignored.
func DecodeDocument(body []byte) (*Controller, error) {
	var doc struct {
		Device   Identity `json:"device"`
		Snapshot struct {
			Dimensions map[evidence.Dimension]struct {
				Status  evidence.Status   `json:"status"`
				Records []json.RawMessage `json:"records"`
				Issues  []json.RawMessage `json:"issues"`
			} `json:"dimensions"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDocument, err)
	}
	if doc.Device.Host == "" {
		return nil, fmt.Errorf("%w: missing device host", ErrDocument)
	}
	dims := map[evidence.Dimension]Observed{}
	for name, o := range doc.Snapshot.Dimensions {
		if name == "collection" {
			continue
		}
		if !o.Status.Valid() {
			return nil, fmt.Errorf("%w: dimension %s status", ErrDocument, name)
		}
		dims[name] = Observed{Status: o.Status, Records: o.Records, Issues: o.Issues}
	}
	return NewController(doc.Device, dims), nil
}
