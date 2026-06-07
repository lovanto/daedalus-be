package models

import (
	"encoding/json"
	"time"
)

type AgentTuneCycle struct {
	ID                   string          `json:"id"`
	AgentID              string          `json:"agent_id"`
	FailureTypeAddressed string          `json:"failure_type_addressed"`
	Changes              json.RawMessage `json:"changes"`
	ContextRefreshed     bool            `json:"context_refreshed"`
	OutcomeNotes         string          `json:"outcome_notes"`
	AppliedBuildID       *string         `json:"applied_build_id"`
	CreatedAt            time.Time       `json:"created_at"`
}
