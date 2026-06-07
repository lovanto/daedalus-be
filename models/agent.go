package models

import "time"

type Agent struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	ConfidenceScore float64    `json:"confidence_score"`
	CurrentPhase    string     `json:"current_phase"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	ParentAgentID   *string    `json:"parent_agent_id,omitempty"`
}

type AgentPhaseHistory struct {
	ID          string     `json:"id"`
	AgentID     string     `json:"agent_id"`
	Phase       string     `json:"phase"`
	EnteredAt   time.Time  `json:"entered_at"`
	ExitedAt    *time.Time `json:"exited_at"`
	TriggeredBy string     `json:"triggered_by"`
}

type DashboardSummary struct {
	TotalAgents        int                  `json:"total_agents"`
	ActiveLoops        int                  `json:"active_loops"`
	AvgConfidenceScore float64              `json:"avg_confidence_score"`
	DeployReadyCount   int                  `json:"deploy_ready_count"`
	RecentActivity     []RecentActivityItem `json:"recent_activity"`
}

type RecentActivityItem struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	AgentName   string    `json:"agent_name"`
	Phase       string    `json:"phase"`
	EnteredAt   time.Time `json:"entered_at"`
	TriggeredBy string    `json:"triggered_by"`
}
