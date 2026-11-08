package wireless

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"
)

// ObjectOrder retains source object key order for derived evidence arrays.
// Arrays always retain input order. Live Go maps use lexical key order.
type ObjectOrder map[uintptr][]string

func (o ObjectOrder) Keys(m Record) []string {
	if k, ok := o[reflect.ValueOf(m).Pointer()]; ok {
		return k
	}
	k := make([]string, 0, len(m))
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

// DecodeJSONDocument preserves arbitrary integer/float intent, rejects trailing
// content and invalid UTF-8, and records object order without altering facts.
func DecodeJSONDocument(body []byte) (Record, ObjectOrder, error) {
	if !utf8.Valid(body) || !validJSONSurrogates(body) {
		return nil, nil, ErrDocument
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	order := ObjectOrder{}
	var read func() (any, error)
	read = func() (any, error) {
		token, e := d.Token()
		if e != nil {
			return nil, e
		}
		switch delim := token.(type) {
		case json.Delim:
			switch delim {
			case '{':
				m := Record{}
				keys := []string{}
				for d.More() {
					key, e := d.Token()
					if e != nil {
						return nil, e
					}
					k, ok := key.(string)
					if !ok {
						return nil, ErrDocument
					}
					v, e := read()
					if e != nil {
						return nil, e
					}
					if _, exists := m[k]; !exists {
						keys = append(keys, k)
					}
					m[k] = v
				}
				if _, e = d.Token(); e != nil {
					return nil, e
				}
				order[reflect.ValueOf(m).Pointer()] = keys
				return m, nil
			case '[':
				a := []any{}
				for d.More() {
					v, e := read()
					if e != nil {
						return nil, e
					}
					a = append(a, v)
				}
				_, e = d.Token()
				return a, e
			default:
				return nil, ErrDocument
			}
		default:
			return token, nil
		}
	}
	v, e := read()
	if e != nil {
		return nil, nil, ErrDocument
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, nil, ErrDocument
	}
	m, ok := v.(Record)
	if !ok {
		return nil, nil, ErrDocument
	}
	if _, e = CanonicalJSON(m); e != nil {
		return nil, nil, e
	}
	return m, order, nil
}

// encoding/json replaces malformed surrogate escapes; Ruby rejects them. Check
// escaped UTF-16 pairs before the generic decoder can erase this distinction.
func validJSONSurrogates(body []byte) bool {
	inString := false
	for i := 0; i < len(body); i++ {
		if body[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || body[i] != '\\' {
			continue
		}
		i++
		if i >= len(body) {
			return false
		}
		if body[i] != 'u' {
			continue
		}
		if i+4 >= len(body) {
			return false
		}
		n, e := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
