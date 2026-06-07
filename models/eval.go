package models

import "time"

type AgentEval struct {
	ID               string    `json:"id"`
	AgentID          string    `json:"agent_id"`
	Score            float64   `json:"score"`
	FailureType      string    `json:"failure_type"`
	TestCasesPassed  int       `json:"test_cases_passed"`
	TestCasesFailed  int       `json:"test_cases_failed"`
	Notes            string    `json:"notes"`
	Source           string    `json:"source"`
	CreatedAt        time.Time `json:"created_at"`
}

type AgentEvalWithGateA struct {
	AgentEval
	GateA GateADecision `json:"gate_a"`
}

type GateADecision struct {
	Result string `json:"result"` // pass | fail
	Route  string `json:"route"`  // observe | tune | build | define
	Reason string `json:"reason"`
}

func GateA(failureType string) GateADecision {
	switch failureType {
	case "behavioral":
		return GateADecision{Result: "fail", Route: "tune", Reason: "behavioral failure detected"}
	case "structural":
		return GateADecision{Result: "fail", Route: "build", Reason: "structural failure detected"}
	case "scope":
		return GateADecision{Result: "fail", Route: "define", Reason: "scope failure — agent definition needs revision"}
	default:
		return GateADecision{Result: "pass", Route: "observe", Reason: "all tests passed"}
	}
}

type AgentEvalCase struct {
	ID               string    `json:"id"`
	AgentID          string    `json:"agent_id"`
	Input            string    `json:"input"`
	ExpectedBehavior string    `json:"expected_behavior"`
	Category         string    `json:"category"`
	IsActive         bool      `json:"is_active"`
	CreatedAt        time.Time `json:"created_at"`
}

// AgentEvalCaseRun is one recorded execution of a single eval case.
type AgentEvalCaseRun struct {
	ID           string    `json:"id"`
	CaseID       string    `json:"case_id"`
	AgentID      string    `json:"agent_id"`
	Passed       bool      `json:"passed"`
	ActualOutput string    `json:"actual_output"`
	Reasoning    string    `json:"reasoning"`
	CreatedAt    time.Time `json:"created_at"`
}
