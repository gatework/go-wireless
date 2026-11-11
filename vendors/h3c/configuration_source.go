package h3c

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	parsetext "github.com/gatework/go-wireless/internal/parsetext"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

// SourceLine contains deliberately explicit accessors for potentially sensitive raw source.
type SourceLine struct {
	index, indent int
	raw, text     string
}

func (l SourceLine) Index() int   { return l.index }
func (l SourceLine) Number() int  { return l.index + 1 }
func (l SourceLine) Raw() string  { return l.raw }
func (l SourceLine) Text() string { return l.text }
func (l SourceLine) Indent() int  { return l.indent }
func (l SourceLine) String() string {
	return fmt.Sprintf("<wireless.SourceLine number=%d>", l.Number())
}
func (l SourceLine) GoString() string { return l.String() }
func (l SourceLine) MarshalJSON() ([]byte, error) {
	return json.Marshal(wireless.Record{"number": l.Number()})
}

type sourceBlock struct {
	index, last int
	children    []*sourceBlock
}
type ConfigurationCapture struct {
	source      *ConfigurationSource
	first, last int
	data        any
	path        []any
}

func (c ConfigurationCapture) Data() any {
	v, _ := wireless.CopyValue(c.data)
	return v
}
func (c ConfigurationCapture) Path() []any {
	v, _ := wireless.CopyValue(c.path)
	list, _ := v.([]any)
	return list
}
func (c ConfigurationCapture) FirstLine() int { return c.first + 1 }
func (c ConfigurationCapture) LastLine() int  { return c.last + 1 }
func (c ConfigurationCapture) Content() string {
	var b strings.Builder
	for _, line := range c.source.lines[c.first : c.last+1] {
		b.WriteString(line.raw)
	}
	return b.String()
}
func (c ConfigurationCapture) Complete() bool {
	for _, state := range c.source.states[c.first : c.last+1] {
		if state != "parsed" && state != "ignored" {
			return false
		}
	}
	return true
}
func (c ConfigurationCapture) String() string {
	return fmt.Sprintf("<wireless.ConfigurationCapture lines=%d..%d>", c.FirstLine(), c.LastLine())
}
func (c ConfigurationCapture) GoString() string { return c.String() }
func (c ConfigurationCapture) MarshalJSON() ([]byte, error) {
	return json.Marshal(wireless.Record{"first_line": c.FirstLine(), "last_line": c.LastLine(), "complete": c.Complete()})
}

type ConfigurationSource struct {
	content, checksum string
	lines             []SourceLine
	states            []string
	roots             []*sourceBlock
	captures          []ConfigurationCapture
	observation       wireless.Observation
	issues            []wireless.Record
}

func ParseConfigurationSource(content string, complete bool) (*ConfigurationSource, error) {
	if _, err := parser.NormalizeText(content, true); err != nil {
		s := &ConfigurationSource{}
		s.observation = parser.Unavailable(wireless.Configuration, wireless.Failed, "invalid_output")
		return s, nil //nolint:nilerr // invalid text is reported as a failed observation, not an error
	}
	if parser.TerminalUnsupported(content) {
		s := &ConfigurationSource{}
		s.observation = parser.Unavailable(wireless.Configuration, wireless.Unsupported, "unsupported_command")
		return s, nil
	}
	s := readConfigurationSource(content)
	p := newConfigurationParser(s)
	result := p.parse(complete)
	s.observation = parser.ResultObservation(wireless.Configuration, result)
	slices.SortStableFunc(s.captures, func(a, b ConfigurationCapture) int { return cmp.Compare(a.first, b.first) })
	return s, nil
}
func (s *ConfigurationSource) Observation() wireless.Observation { return s.observation }
func (s *ConfigurationSource) Content() string                   { return s.content }
func (s *ConfigurationSource) Checksum() string                  { return s.checksum }
func (s *ConfigurationSource) Lines() []SourceLine               { return append([]SourceLine{}, s.lines...) }
func (s *ConfigurationSource) States() []string                  { return append([]string{}, s.states...) }
func (s *ConfigurationSource) Captures() []ConfigurationCapture {
	return append([]ConfigurationCapture{}, s.captures...)
}
func (s *ConfigurationSource) CapturesFor(path []any) []ConfigurationCapture {
	out := []ConfigurationCapture{}
	for _, c := range s.captures {
		if reflect.DeepEqual(path, c.path) {
			out = append(out, c)
		}
	}
	return out
}
func (s *ConfigurationSource) UnparsedContent() string {
	var b strings.Builder
	for i, line := range s.lines {
		if s.states[i] != "parsed" && s.states[i] != "ignored" {
			b.WriteString(line.raw)
		}
	}
	return b.String()
}
func (s *ConfigurationSource) State(index int) (string, error) {
	if index < 0 || index >= len(s.states) {
		return "", errors.New("line index outside configuration document")
	}
	return s.states[index], nil
}
func (s *ConfigurationSource) Issues() []wireless.Record {
	out, _ := wireless.CopyRecords(s.issues)
	return out
}
func (s *ConfigurationSource) String() string {
	return fmt.Sprintf("<wireless.ConfigurationSource lines=%d captures=%d>", len(s.lines), len(s.captures))
}
func (s *ConfigurationSource) GoString() string { return s.String() }
func (s *ConfigurationSource) MarshalJSON() ([]byte, error) {
	counts := wireless.Record{}
	for _, state := range s.states {
		v, _ := counts[state].(int)
		counts[state] = v + 1
	}
	return json.Marshal(wireless.Record{"lines": len(s.lines), "states": counts, "captures": len(s.captures)})
}
func (s *ConfigurationSource) capture(block *sourceBlock, data any, path []any) {
	v, _ := wireless.CopyValue(data)
	p, _ := wireless.CopyValue(path)
	s.captures = append(s.captures, ConfigurationCapture{s, block.index, block.last, v, asList(p)})
}
func readConfigurationSource(content string) *ConfigurationSource {
	hash := sha256.Sum256([]byte(content))
	s := &ConfigurationSource{content: content, checksum: hex.EncodeToString(hash[:]), roots: []*sourceBlock{}, captures: []ConfigurationCapture{}, issues: []wireless.Record{}}
	s.lines = splitSourceLines(content)
	s.states = make([]string, len(s.lines))
	for i := range s.states {
		s.states[i] = "pending"
	}
	s.buildBlocks()
	return s
}

// splitSourceLines splits content into physical lines, keeping each line's
// terminator (CRLF, CR or LF) in raw.
func splitSourceLines(content string) []SourceLine {
	var lines []SourceLine
	for start := 0; start < len(content); {
		end := start
		for end < len(content) && content[end] != '\r' && content[end] != '\n' {
			end++
		}
		if end < len(content) {
			if content[end] == '\r' && end+1 < len(content) && content[end+1] == '\n' {
				end += 2
			} else {
				end++
			}
		}
		raw := content[start:end]
		lines = append(lines, SourceLine{len(lines), len(raw) - len(strings.TrimLeft(raw, " \t")), raw, parsetext.Strip(raw)})
		start = end
	}
	return lines
}

// buildBlocks nests lines into blocks by indentation; "#" and "return" close
// every open block, and banners, blank and terminal-invalid lines are not
// blocks.
func (s *ConfigurationSource) buildBlocks() {
	var stack []*sourceBlock
	closeTo := func(depth, last int) {
		for len(stack) > depth {
			stack[len(stack)-1].last = last
			stack = stack[:len(stack)-1]
		}
	}
	banner := parsetext.Banner{}
	for index, line := range s.lines {
		switch {
		case banner.Consume(line.text):
			s.states[index] = "ignored"
			continue
		case parser.TerminalInvalid(line.raw):
			s.issues = append(s.issues, wireless.Record{"code": "incomplete_configuration", "line": index + 1})
			s.states[index] = "invalid"
			continue
		case line.text == "#" || line.text == "return":
			closeTo(0, index-1)
			s.states[index] = "ignored"
			continue
		case line.text == "":
			s.states[index] = "ignored"
			continue
		}
		depth := len(stack)
		for depth > 0 && line.indent <= s.lines[stack[depth-1].index].indent {
			depth--
		}
		closeTo(depth, index-1)
		block := &sourceBlock{index: index, last: -1, children: []*sourceBlock{}}
		if len(stack) == 0 {
			s.roots = append(s.roots, block)
		} else {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, block)
		}
		stack = append(stack, block)
	}
	for _, block := range stack {
		block.last = len(s.lines) - 1
	}
	if banner.Open() {
		s.issues = append(s.issues, wireless.Record{"code": "incomplete_banner"})
	}
}

// asList returns v as a list, or nil when it is not one.
func asList(v any) []any {
	list, _ := v.([]any)
	return list
}
