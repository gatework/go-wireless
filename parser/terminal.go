package parser

import (
	source "github.com/gatework/parser"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"

	wireless "github.com/gatework/go-wireless"
)

var terminalInvalid = rx(`(?i)--[ \t]*More[ \t]*--|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
var terminalUnsupported = rx(`(?i)^[ \t]*(?:%[ \t]*)?(?:Unrecognized command\b|Wrong parameter\b|Invalid (?:input|command)\b|Unknown command\b|Error:[^\r\n]*\bcommand\b)[^\r\n]*`)

// TerminalUnsupported reports whether the device rejected the command
// (outside a login banner).
func TerminalUnsupported(text string) bool {
	b := parsetext.Banner{}
	for _, line := range SourceLines(text) {
		if !b.Consume(parsetext.Strip(line)) && terminalUnsupported.MatchString(line) {
			return true
		}
	}
	return false
}

// TerminalInvalid reports paging prompts or control characters, which make
// output untrustworthy.
func TerminalInvalid(text string) bool { return terminalInvalid.MatchString(text) }

// TerminalIssue returns an invalid_terminal_output issue for the first
// invalid line, or nil.
func TerminalIssue(text string) wireless.Record {
	if !TerminalInvalid(text) {
		return nil
	}
	for n, line := range SourceLines(text) {
		if TerminalInvalid(line) {
			return wireless.Record{"code": "invalid_terminal_output", "line": n + 1}
		}
	}
	return nil
}

// SourceLines splits text into lines on LF, CRLF or CR, without a trailing
// empty line.
func SourceLines(text string) []string {
	// An in-memory string cannot have more physical lines than bytes.
	content, _ := source.New(text, source.Limits{MaxBytes: max(1, len(text)), MaxLines: max(1, len(text))})
	lines := make([]string, 0, content.Len())
	for line := range content.All() {
		lines = append(lines, line.Text)
	}
	return lines
}
