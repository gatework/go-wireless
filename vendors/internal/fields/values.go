package fields

import (
	"github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/parser"
)

// IntegerValue parses an exact non-negative integer as a JSON number value.
func IntegerValue(raw *string) (any, error) {
	v, e := parser.ParseIntegerExact(raw, nil)
	return record.IntegerJSON(v), e
}

// DurationValue parses a duration into exact seconds as a JSON number value.
func DurationValue(raw *string) (any, error) {
	v, e := parser.ParseDurationExact(raw)
	return record.IntegerJSON(v), e
}
