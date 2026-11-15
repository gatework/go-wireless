package parsetext

import "github.com/gatework/parser"

// NewContent indexes one controller response without changing its raw bytes.
// The line budget admits large verbose client tables; exceeding either budget
// rejects the entire source instead of parsing a truncated document.
func NewContent(text string) (*parser.Content, error) {
	return parser.New(text, parser.Limits{MaxBytes: 32 << 20, MaxLines: 400_000})
}
