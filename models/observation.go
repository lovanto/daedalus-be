package models

import (
	"encoding/json"
	"time"
)

type AgentObservation struct {
	ID        string          `json:"id"`
	AgentID   string          `json:"agent_id"`
	Pattern   string          `json:"pattern"`
	Severity  string          `json:"severity"`
	Source    string          `json:"source"`
	RoutedTo  string          `json:"routed_to"`
	Tags      json.RawMessage `json:"tags"`
	CreatedAt time.Time       `json:"created_at"`
}

type ObservationCluster struct {
	Tag          string             `json:"tag"`
	Observations []AgentObservation `json:"observations"`
}
