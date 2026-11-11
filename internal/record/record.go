package record

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
)

// Records returns v as a list of records, skipping non-record items.
func Records(v any) []map[string]any {
	if a, ok := v.([]map[string]any); ok {
		return a
	}
	if a, ok := v.([]any); ok {
		rows := []map[string]any{}
		for _, r := range a {
			if m, ok := r.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
		return rows
	}
	return []map[string]any{}
}

// StringValue returns v when it is a string, otherwise "".
func StringValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// Scalar returns *v, or nil for a nil pointer.
func Scalar[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}

// IntegerJSON returns value as an exact JSON number, or nil.
func IntegerJSON(value *big.Int) any {
	if value == nil {
		return nil
	}
	if value.IsInt64() {
		return value.Int64()
	}
	return json.Number(value.String())
}

// Strings returns v as strings when it is a list of strings only.
func Strings(v any) ([]string, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, false
	}
	r := []string{}
	for i := range rv.Len() {
		s, ok := reflect.TypeAssert[string](rv.Index(i))
		if !ok {
			return nil, false
		}
		r = append(r, s)
	}
	return r, true
}

// Map returns v as a map, or an empty map.
func Map(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
