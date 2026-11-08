package cli

import (
	"embed"
	"encoding/json"
	"slices"
	"strconv"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/internal/bootstrap"
)

//go:embed templates/*.txt
var templates embed.FS

func in(s string, values ...string) bool        { return slices.Contains(values, s) }
func StringValue(v any) string                  { s, _ := v.(string); return s }
func copyMap(m wireless.Record) wireless.Record { return wireless.CloneRecord(m) }
func stringList(v any) ([]string, error) {
	a, ok := v.([]any)
	if !ok {
		if b, ok := v.([]string); ok {
			return slices.Clone(b), nil
		}
		return nil, bootstrap.ErrOptions
	}
	b := make([]string, len(a))
	for i, v := range a {
		s, ok := v.(string)
		if !ok {
			return nil, bootstrap.ErrOptions
		}
		b[i] = s
	}
	return b, nil
}
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, e := strconv.ParseFloat(string(n), 64)
		return f, e == nil
	}
	return 0, false
}
