package wireless

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrDocument identifies a value that cannot be retained as exact JSON evidence.
var ErrDocument = errors.New("wireless: invalid evidence document")

// Valid reports whether the dimension belongs to the wireless evidence schema.
func (d Dimension) Valid() bool {
	switch d {
	case AccessPoints, Radios, BSSes, Clients, Configuration, APGroups, ServiceTemplates,
		Dot1xConnections, MABConnections, BlockedClients, ClientIPConflicts, APModels, LLDP:
		return true
	default:
		return false
	}
}

// CopyValue validates and owns a plain JSON value without invoking marshalers.
func CopyValue(value any) (any, error) { return copyJSON(value, make(map[visit]bool)) }

// CopyRecords validates and owns records without changing numeric or null values.
func CopyRecords(records []Record) ([]Record, error) { return copyRecords(records) }

// CloneRecord returns an independent view of already validated evidence.
// Invalid values return nil; untrusted inputs must first pass CopyValue.
func CloneRecord(record Record) Record { return cloneRecord(record) }

// ValidCommand reports whether a command is a nonempty single terminal line.
func ValidCommand(command string) bool {
	return strings.TrimSpace(command) != "" && utf8.ValidString(command) && !strings.ContainsFunc(command, unicode.IsControl)
}

// FailureFrom retains only safe diagnostic classifications from an error.
func FailureFrom(err error, fallback, stage string) *FailureError {
	return safeFailure(err, fallback, stage)
}

// CloneFailure returns an independently owned safe failure, including nil.
func CloneFailure(failure *FailureError) *FailureError { return cloneFailure(failure) }

// ValidJSONNumber checks the exact JSON numeric grammar.
func ValidJSONNumber(number string) bool { return jsonNumberPattern.MatchString(number) }
