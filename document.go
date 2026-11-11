package wireless

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

// DecodeJSONDocument decodes one JSON object into generic records. Numbers
// keep their source text (json.Number), nested objects are Records, and
// invalid UTF-8, malformed UTF-16 surrogate escapes and trailing content are
// rejected.
func DecodeJSONDocument(body []byte) (Record, error) {
	if !utf8.Valid(body) || !validJSONSurrogates(body) {
		return nil, ErrDocument
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	v, err := readJSONValue(d)
	if err != nil {
		return nil, ErrDocument
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, ErrDocument
	}
	m, ok := v.(Record)
	if !ok {
		return nil, ErrDocument
	}
	return m, nil
}

// readJSONValue reads one value, decoding every object as a Record.
func readJSONValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		m := Record{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			k, ok := key.(string)
			if !ok {
				return nil, ErrDocument
			}
			if m[k], err = readJSONValue(d); err != nil {
				return nil, err
			}
		}
		_, err = d.Token()
		return m, err
	case '[':
		a := []any{}
		for d.More() {
			v, err := readJSONValue(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	default:
		return nil, ErrDocument
	}
}

// validJSONSurrogates reports whether every \\u escape in body forms valid
// UTF-16. encoding/json would silently replace a malformed surrogate, which
// could make two different documents decode alike.
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
