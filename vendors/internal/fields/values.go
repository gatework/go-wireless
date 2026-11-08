package fields

import (
	"github.com/gatework/go-wireless/internal/record"
	"github.com/gatework/go-wireless/parser"
)

func IntegerValue(raw *string) (any, error) {
	v, e := parser.ParseIntegerExact(raw, nil)
	return record.IntegerJSON(v), e
}

func DurationValue(raw *string) (any, error) {
	v, e := parser.ParseDurationExact(raw)
	return record.IntegerJSON(v), e
}
