package utils

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentBelongsToUser returns true when the agent exists, is not deleted, and belongs to userID.
func AgentBelongsToUser(ctx context.Context, db *pgxpool.Pool, agentID, userID string) bool {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM agents
		   WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		 )`,
		agentID, userID,
	).Scan(&exists)
	return err == nil && exists
}
