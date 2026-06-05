package models

import (
	"encoding/json"
	"time"
)

type AgentBuild struct {
	ID                 string          `json:"id"`
	AgentID            string          `json:"agent_id"`
	Version            int             `json:"version"`
	ModelProvider      string          `json:"model_provider"`
	ModelName          string          `json:"model_name"`
	Temperature        float64         `json:"temperature"`
	MaxTokens          int             `json:"max_tokens"`
	SystemPrompt       string          `json:"system_prompt"`
	Tools              json.RawMessage `json:"tools"`
	OrchestrationNotes string          `json:"orchestration_notes"`
	CreatedAt          time.Time       `json:"created_at"`
}
