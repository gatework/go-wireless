package wireless

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// MarshalJSON preserves the integer-versus-float intent of facts at the wire
// boundary. Float measurements with integral values retain a decimal token.
// Domain marshalers are honored; arbitrary structs use encoding/json semantics.
func MarshalJSON(value any) ([]byte, error) {
	v, err := wireNumbers(reflect.ValueOf(value), map[visit]bool{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// MarshalJSONIndent is the pretty-printing version of MarshalJSON.
func MarshalJSONIndent(value any, prefix, indent string) ([]byte, error) {
	b, e := MarshalJSON(value)
	if e != nil {
		return nil, e
	} // Re-encode RawMessage to retain numeric tokens.
	return json.MarshalIndent(json.RawMessage(b), prefix, indent)
}

//nolint:nilnil // nil is JSON null
func wireNumbers(v reflect.Value, active map[visit]bool) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, nil
		}
		return wireNumbers(v.Elem(), active)
	}
	if v.CanInterface() {
		if _, ok := reflect.TypeAssert[json.Marshaler](v); ok {
			return v.Interface(), nil
		}
		if _, ok := reflect.TypeAssert[json.Number](v); ok {
			return v.Interface(), nil
		}
	}
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, &json.UnsupportedValueError{Value: v, Str: "non-finite float"}
		}
		s := strconv.FormatFloat(f, 'g', -1, v.Type().Bits())
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return json.Number(s), nil
	case reflect.Map, reflect.Slice:
		if v.IsNil() {
			return nil, nil
		}
		key := visit{kind: v.Kind(), ptr: v.Pointer()}
		if active[key] {
			return nil, &json.UnsupportedValueError{Value: v, Str: "cycle"}
		}
		active[key] = true
		defer delete(active, key)
		if v.Kind() == reflect.Map {
			if v.Type().Key().Kind() != reflect.String {
				return v.Interface(), nil
			}
			m := map[string]any{}
			it := v.MapRange()
			for it.Next() {
				x, e := wireNumbers(it.Value(), active)
				if e != nil {
					return nil, e
				}
				m[it.Key().String()] = x
			}
			return m, nil
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Interface(), nil
		}
		a := make([]any, v.Len())
		for i := range a {
			x, e := wireNumbers(v.Index(i), active)
			if e != nil {
				return nil, e
			}
			a[i] = x
		}
		return a, nil
	case reflect.Array:
		a := make([]any, v.Len())
		for i := range a {
			x, e := wireNumbers(v.Index(i), active)
			if e != nil {
				return nil, e
			}
			a[i] = x
		}
		return a, nil
	default:
		return v.Interface(), nil
	}
}
