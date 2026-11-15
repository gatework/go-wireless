package wlan

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ServerReference preserves a named portal server or a redacted URL presence.
// Exactly one field may be set. Neither field means an unknown reference.
// URL contents never enter normalized facts.
type ServerReference struct {
	Name       *string
	URLPresent *bool
}

// MarshalJSON preserves the parser's string, boolean or null representation.
func (r ServerReference) MarshalJSON() ([]byte, error) {
	if r.Name != nil {
		if r.URLPresent != nil {
			return nil, errors.New("wlan: ambiguous server reference")
		}
		return json.Marshal(*r.Name)
	}
	return json.Marshal(r.URLPresent)
}

// UnmarshalJSON accepts only a named reference, URL presence, or unknown value.
func (r *ServerReference) UnmarshalJSON(data []byte) error {
	var next ServerReference
	data = bytes.TrimSpace(data)
	switch {
	case bytes.Equal(data, []byte("null")):
	case len(data) > 0 && data[0] == '"':
		if err := json.Unmarshal(data, &next.Name); err != nil {
			return err
		}
	default:
		if err := json.Unmarshal(data, &next.URLPresent); err != nil {
			return errors.New("wlan: invalid server reference")
		}
	}
	*r = next
	return nil
}
