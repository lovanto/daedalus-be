package services

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AgentService struct {
	db *pgxpool.Pool
}

func NewAgentService(db *pgxpool.Pool) *AgentService {
	return &AgentService{db: db}
}

// CalculateConfidenceScore computes the rolling average of the last 3 eval scores
// and persists the result on the agent row. Returns the updated score.
func (s *AgentService) CalculateConfidenceScore(ctx context.Context, agentID string) (float64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT score FROM agent_evals
		 WHERE agent_id = $1
		 ORDER BY created_at DESC LIMIT 3`,
		agentID,
	)
	if err != nil {
		return 0, fmt.Errorf("query evals: %w", err)
	}
	defer rows.Close()

	var scores []float64
	for rows.Next() {
		var score float64
		if err := rows.Scan(&score); err != nil {
			return 0, fmt.Errorf("scan score: %w", err)
		}
		scores = append(scores, score)
	}

	if len(scores) == 0 {
		return 0, nil
	}

	var sum float64
	for _, sc := range scores {
		sum += sc
	}
	avg := sum / float64(len(scores))

	if _, err := s.db.Exec(ctx,
		`UPDATE agents SET confidence_score = $1, updated_at = NOW() WHERE id = $2`,
		avg, agentID,
	); err != nil {
		return 0, fmt.Errorf("update confidence score: %w", err)
	}

	return avg, nil
}

type GateBResult string

const (
	GateBDeployReady  GateBResult = "deploy_ready"
	GateBScopeDrift   GateBResult = "scope_drift"
	GateBContinueEval GateBResult = "continue_eval"
)

// CheckGateB evaluates whether an agent is ready to deploy.
// Returns deploy_ready if the last 3 evals all meet threshold,
// scope_drift if any of those evals has failure_type='scope',
// or continue_eval otherwise.
func (s *AgentService) CheckGateB(ctx context.Context, agentID string, threshold float64) (GateBResult, error) {
	rows, err := s.db.Query(ctx,
		`SELECT score, failure_type FROM agent_evals
		 WHERE agent_id = $1
		 ORDER BY created_at DESC LIMIT 3`,
		agentID,
	)
	if err != nil {
		return GateBContinueEval, fmt.Errorf("query evals: %w", err)
	}
	defer rows.Close()

	type evalRow struct {
		score       float64
		failureType string
	}
	var evals []evalRow
	for rows.Next() {
		var e evalRow
		if err := rows.Scan(&e.score, &e.failureType); err != nil {
			return GateBContinueEval, fmt.Errorf("scan eval: %w", err)
		}
		evals = append(evals, e)
	}

	if len(evals) < 3 {
		return GateBContinueEval, nil
	}

	for _, e := range evals {
		if e.failureType == "scope" {
			return GateBScopeDrift, nil
		}
	}

	for _, e := range evals {
		if e.score < threshold {
			return GateBContinueEval, nil
		}
	}

	return GateBDeployReady, nil
}
