package wireless

import (
	"strings"
)

var terminalInvalid = rx(`(?i)--[ \t]*More[ \t]*--|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
var terminalUnsupported = rx(`(?i)^[ \t]*(?:%[ \t]*)?(?:Unrecognized command\b|Wrong parameter\b|Invalid (?:input|command)\b|Unknown command\b|Error:[^\r\n]*\bcommand\b)[^\r\n]*`)
var bannerPattern = rx(`^(?:banner\s+\S+|header\s+\S+(?:\s+information)?)\s+(.+)$`)

type banner struct{ delimiter string }

func (b *banner) consume(text string) bool {
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
func TerminalUnsupported(text string) bool {
	b := banner{}
	for _, line := range sourceLines(text) {
		if !b.consume(strip(line)) && terminalUnsupported.MatchString(line) {
			return true
		}
	}
	return false
}
func TerminalInvalid(text string) bool { return terminalInvalid.MatchString(text) }
func TerminalIssue(text string) Record {
	if !TerminalInvalid(text) {
		return nil
	}
	for n, line := range sourceLines(text) {
		if TerminalInvalid(line) {
			return Record{"code": "invalid_terminal_output", "line": n + 1}
		}
	}
	return nil
}
func sourceLines(text string) []string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
