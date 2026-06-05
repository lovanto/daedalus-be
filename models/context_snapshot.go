package models

import (
	"encoding/json"
	"time"
)

type AgentContextSnapshot struct {
	ID               string          `json:"id"`
	AgentID          string          `json:"agent_id"`
	ToolsAudit       json.RawMessage `json:"tools_audit"`
	KnowledgeSources json.RawMessage `json:"knowledge_sources"`
	MemoryNotes      string          `json:"memory_notes"`
	CreatedAt        time.Time       `json:"created_at"`
}
