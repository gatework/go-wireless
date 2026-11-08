package parser

import (
	"encoding/json"
	"math/big"

	"github.com/gatework/go-wireless/internal/record"

	wireless "github.com/gatework/go-wireless"
)

// Result is one vendor parser view before profile validation and ownership copying.
type Result struct {
	Status          wireless.Status
	Records, Issues []wireless.Record
}

func FailedResult(code string) Result {
	return Result{Status: wireless.Failed, Records: []wireless.Record{}, Issues: []wireless.Record{{"code": code}}}
}

func UniqueIssues(issues []wireless.Record) []wireless.Record {
	seen := map[string]bool{}
	out := []wireless.Record{}
	for _, i := range issues {
		encoded, _ := json.Marshal(i)
		k := string(encoded)
		if !seen[k] {
			seen[k] = true
			out = append(out, i)
		}
	}
	return out
}

// FieldsResult verifies count and identity evidence shared by vendor tables.
func FieldsResult(records []wireless.Record, issues []wireless.Record, complete bool, total *big.Int, identity []string) Result {
	if total != nil && total.Cmp(big.NewInt(int64(len(records)))) != 0 {
		issues = append(issues, wireless.Record{"code": "count_mismatch", "expected": record.IntegerJSON(total), "parsed": len(records)})
	}
	if !(complete && (total != nil || len(records) > 0)) {
		issues = append(issues, wireless.Record{"code": "missing_completeness_evidence"})
	}
	seen := map[string]bool{}
	unique := []wireless.Record{}
	for _, r := range records {
		values := make([]any, len(identity))
		for i, k := range identity {
			values[i] = r[k]
		}
		b, _ := json.Marshal(values)
		key := string(b)
		if seen[key] {
			issues = append(issues, wireless.Record{"code": "duplicate_identity"})
		} else {
			unique = append(unique, r)
			seen[key] = true
		}
	}
	issues = UniqueIssues(issues)
	status := wireless.Complete
	if len(issues) > 0 {
		status = wireless.Partial
		if len(unique) == 0 {
			status = wireless.Failed
		}
	}
	return Result{status, unique, issues}
}
func HasDuplicates(records []wireless.Record, identity []string) bool {
	seen := map[string]bool{}
	for _, r := range records {
		values := make([]any, len(identity))
		for i, k := range identity {
			values[i] = r[k]
		}
		b, _ := json.Marshal(values)
		if seen[string(b)] {
			return true
		}
		seen[string(b)] = true
	}
	return false
}
