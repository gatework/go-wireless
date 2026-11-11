package parsetext

import (
	"regexp"
	"strings"
)

var decimalPattern = regexp.MustCompile(`^\d+$`)

// Strip trims ASCII whitespace and NUL; other Unicode characters are facts.
func Strip(s string) string { return strings.Trim(s, " \t\r\n\v\f\x00") }

// SplitWords splits s on ASCII whitespace.
func SplitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\v' || r == '\f' })
}

// IsDecimal reports whether text contains only decimal digits.
func IsDecimal(text string) bool { return decimalPattern.MatchString(text) }

var bannerPattern = regexp.MustCompile(`^(?:banner\s+\S+|header\s+\S+(?:\s+information)?)\s+(.+)$`)

// Banner tracks a multi-line login banner so its lines are not parsed as
// configuration.
type Banner struct{ delimiter string }

// Consume reports whether text belongs to a banner, opening or closing it as
// needed.
func (b *Banner) Consume(text string) bool {
	if b.delimiter != "" {
		if strings.Contains(text, b.delimiter) {
			b.delimiter = ""
		}
		return true
	}
	m := bannerPattern.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	r := []rune(m[1])
	n := 1
	if r[0] == '^' && len(r) > 1 {
		n = 2
	}
	delimiter := string(r[:n])
	if !strings.Contains(strings.TrimPrefix(m[1], delimiter), delimiter) {
		b.delimiter = delimiter
	}
	return true
}

var separatorLine = regexp.MustCompile(`^[-=]+$`)

// IsSeparator recognizes a table divider rather than a data row.
func IsSeparator(text string) bool { return separatorLine.MatchString(text) }

// Open reports an unterminated multiline banner.
func (b *Banner) Open() bool { return b.delimiter != "" }
