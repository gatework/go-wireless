package wireless

import (
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/gatework/go-netops/evidence"
)

// Fingerprint returns the configuration fingerprint that apply compares
// before and after a change: the go-netops canonical SHA-256 of value's wire
// encoding (MarshalJSON). A live record and its stored JSON therefore
// fingerprint alike, including integral float values. Invalid UTF-8 is
// rejected rather than replaced, so distinct byte strings never collide.
func Fingerprint(value any) (string, error) {
	if err := validUTF8(reflect.ValueOf(value)); err != nil {
		return "", err
	}
	body, err := MarshalJSON(value)
	if err != nil {
		return "", fmt.Errorf("wireless: fingerprint: %w", err)
	}
	digest, err := evidence.Fingerprint(json.RawMessage(body))
	if err != nil {
		return "", fmt.Errorf("wireless: fingerprint: %w", err)
	}
	return digest, nil
}

// errInvalidUTF8 reports a string that is not valid UTF-8.
var errInvalidUTF8 = fmt.Errorf("%w: invalid UTF-8", ErrDocument)

func validUTF8(v reflect.Value) error {
	switch v.Kind() {
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return errInvalidUTF8
		}
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			return validUTF8(v.Elem())
		}
	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			if err := validUTF8(it.Key()); err != nil {
				return err
			}
			if err := validUTF8(it.Value()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := validUTF8(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				if err := validUTF8(v.Field(i)); err != nil {
					return err
				}
			}
		}
	default:
	}
	return nil
}
