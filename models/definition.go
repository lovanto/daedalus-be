package models

import (
	"encoding/json"
	"time"
)

type AgentDefinition struct {
	ID                  string          `json:"id"`
	AgentID             string          `json:"agent_id"`
	Version             int             `json:"version"`
	Goals               string          `json:"goals"`
	IntendedBehaviors   json.RawMessage `json:"intended_behaviors"`
	Constraints         json.RawMessage `json:"constraints"`
	SuccessMetrics      json.RawMessage `json:"success_metrics"`
	UnsafeZones         string          `json:"unsafe_zones"`
	ConfidenceThreshold float64         `json:"confidence_threshold"`
	SOPs                string          `json:"sops"`
	CreatedAt           time.Time       `json:"created_at"`
}
