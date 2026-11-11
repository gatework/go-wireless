package parser

import (
	"errors"
	"math/big"
	"strings"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"
)

// ParseIntegerExact preserves Ruby's unbounded decimal integer semantics.
// Nil means unknown; a nonnil maximum must be nonnegative.
//
//nolint:nilnil // an absent field (nil) parses to an absent value
func ParseIntegerExact(raw *string, maximum *big.Int) (*big.Int, error) {
	if maximum != nil && maximum.Sign() < 0 {
		return nil, errors.New("invalid wireless integer limits")
	}
	if raw == nil {
		return nil, nil
	}
	if !parsetext.IsDecimal(*raw) {
		return nil, errors.New("invalid wireless integer")
	}
	value, ok := new(big.Int).SetString(*raw, 10)
	if !ok {
		return nil, errors.New("invalid wireless integer")
	}
	if maximum != nil && value.Cmp(maximum) > 0 {
		return nil, errors.New("wireless integer out of range")
	}
	return value, nil
}

// ParseDurationExact returns a fresh arbitrary-precision duration in seconds.
//
//nolint:nilnil // an absent field (nil) parses to an absent value
func ParseDurationExact(raw *string) (*big.Int, error) {
	if raw == nil {
		return nil, nil
	}
	total := new(big.Int)
	if durationColon.MatchString(*raw) {
		parts := strings.Split(*raw, ":")
		multipliers := []int64{86400, 3600, 60, 1}
		for i, part := range parts {
			v, err := ParseIntegerExact(&part, nil)
			if err != nil {
				return nil, errors.New("invalid wireless duration")
			}
			limit := int64(0)
			if i == 1 {
				limit = 24
			} else if i > 1 {
				limit = 60
			}
			if limit > 0 && v.Cmp(big.NewInt(limit)) >= 0 {
				return nil, errors.New("invalid wireless duration")
			}
			total.Add(total, new(big.Int).Mul(v, big.NewInt(multipliers[i])))
		}
		return total, nil
	}
	parts := durationWords.FindAllStringSubmatch(*raw, -1)
	remainder := strings.NewReplacer(" ", "", ":", "", ",", "").Replace(durationWords.ReplaceAllString(*raw, ""))
	if len(parts) == 0 || remainder != "" {
		return nil, errors.New("invalid wireless duration")
	}
	for _, part := range parts {
		amount, err := ParseIntegerExact(&part[1], nil)
		if err != nil {
			return nil, errors.New("invalid wireless duration")
		}
		multiplier := map[byte]int64{'d': 86400, 'h': 3600, 'm': 60, 's': 1}[strings.ToLower(part[2])[0]]
		total.Add(total, new(big.Int).Mul(amount, big.NewInt(multiplier)))
	}
	return total, nil
}

// ParseChannelWidthExact preserves positive MHz widths without a machine integer limit.
// Unknown or noncontiguous widths remain nil, matching the Ruby field conversion.
func ParseChannelWidthExact(raw *string) *big.Int {
	if raw == nil {
		return nil
	}
	match := widthPattern.FindStringSubmatch(*raw)
	if match == nil {
		return nil
	}
	value, err := ParseIntegerExact(&match[1], nil)
	if err != nil || value.Sign() <= 0 {
		return nil
	}
	return value
}
