package parsetext

import (
	"strings"

	"github.com/gatework/parser"
)

const maxSourceBytes = 32 << 20
const maxSourceLines = 400_000

// NewContent indexes one controller response without changing its raw bytes.
// The line budget admits large verbose client tables; exceeding either budget
// rejects the entire source instead of parsing a truncated document.
func NewContent(text string) (*parser.Content, error) {
	return parser.New(text, parser.Limits{MaxBytes: maxSourceBytes, MaxLines: maxSourceLines})
}

// SourceFits admits raw input before terminal guards allocate a physical index.
// CRLF is one terminator; a non-terminated final line counts once.
func SourceFits(text string) bool {
	if len(text) > maxSourceBytes {
		return false
	}
	lines := strings.Count(text, "\n") + strings.Count(text, "\r") - strings.Count(text, "\r\n")
	if text != "" && !strings.HasSuffix(text, "\n") && !strings.HasSuffix(text, "\r") {
		lines++
	}
	return lines <= maxSourceLines
}
