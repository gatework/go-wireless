package apply

import (
	"slices"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
)

type executionStatus string

const (
	confirmed             executionStatus = "confirmed"
	executing             executionStatus = "executing"
	failed                executionStatus = "failed"
	uncertain             executionStatus = "uncertain"
	configurationVerified executionStatus = "configuration_verified"
)

// execution owns progress independently of journal records. Durable intent makes
// failures uncertain; only a successful Write acknowledges a command.
type execution struct {
	plan                 change.Plan
	planSHA256           string
	commands             []string
	status               executionStatus
	stage                string
	started              bool
	pendingCommand       *int
	acknowledged         []int
	observedConfigSHA256 string
	readbackVerified     bool
	errorCode            string
	cleanupFailed        bool
	diagnostics          diagnostics
}

func newExecution(plan change.Plan, planSHA256 string) *execution {
	return &execution{plan: plan, planSHA256: planSHA256, commands: plan.Commands(), status: confirmed, stage: "connect"}
}

func (s *execution) beginCommand(index int) {
	s.status = executing
	s.pendingCommand = &index
}

func (s *execution) acknowledge(index int) {
	s.acknowledged = append(s.acknowledged, index)
	s.pendingCommand = nil
}

func (s *execution) fail(interrupted bool) {
	s.status = failed
	if s.started {
		s.status = uncertain
	}
	s.errorCode = "apply_failed"
	if interrupted {
		s.errorCode = "interrupted"
	}
}

// record projects an owned wire document; absent fields stay absent and the
// acknowledgement list remains an explicit empty array before any write.
func (s *execution) record() wireless.Record {
	steps := make([]wireless.Record, len(s.acknowledged))
	for i, index := range s.acknowledged {
		steps[i] = wireless.Record{"index": index, "status": "acknowledged"}
	}
	out := wireless.Record{
		"status": string(s.status), "device": s.plan.Device(), "plan_sha256": s.planSHA256,
		"expected_config_sha256": s.plan.ExpectedConfigSHA256(), "commands": slices.Clone(s.commands), "steps": steps,
	}
	s.projectTerminal(out)
	if s.readbackVerified {
		out["observed_config_sha256"] = s.observedConfigSHA256
		out["readback_verified"] = true
	}
	if s.cleanupFailed {
		out["cleanup_error"] = "session_close_failed"
	}
	return out
}

func (s *execution) summary() wireless.Record {
	out := wireless.Record{"status": string(s.status)}
	s.projectTerminal(out)
	return out
}

func (s *execution) projectTerminal(out wireless.Record) {
	if s.errorCode != "" {
		out["error"] = s.errorCode
	}
	if s.pendingCommand != nil {
		out["pending_command"] = *s.pendingCommand
	}
	s.diagnostics.attach(out)
}
