package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/models"
	"github.com/daedalus/daedalus-be/utils"
)

type PhaseHandler struct {
	db *pgxpool.Pool
}

func NewPhaseHandler(db *pgxpool.Pool) *PhaseHandler {
	return &PhaseHandler{db: db}
}

// List godoc
// @Summary List phase history for an agent
// @Tags phases
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/phases [get]
func (h *PhaseHandler) List(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !h.agentBelongsToUser(r, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, agent_id, phase, entered_at, exited_at, COALESCE(triggered_by,'')
		 FROM agent_phase_history
		 WHERE agent_id = $1
		 ORDER BY entered_at DESC`,
		agentID,
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch phase history")
		return
	}
	defer rows.Close()

	history := make([]models.AgentPhaseHistory, 0)
	for rows.Next() {
		var ph models.AgentPhaseHistory
		if err := rows.Scan(&ph.ID, &ph.AgentID, &ph.Phase, &ph.EnteredAt, &ph.ExitedAt, &ph.TriggeredBy); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan phase history")
			return
		}
		history = append(history, ph)
	}

	utils.JSON(w, http.StatusOK, history)
}

// Add godoc
// @Summary Log a phase transition (auto-closes the current open phase)
// @Tags phases
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "phase, triggered_by"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/phases [post]
func (h *PhaseHandler) Add(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !h.agentBelongsToUser(r, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		Phase       string `json:"phase"`
		TriggeredBy string `json:"triggered_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Phase = strings.TrimSpace(req.Phase)
	if req.Phase == "" {
		utils.Err(w, http.StatusBadRequest, "phase is required")
		return
	}

	// Close any open phase entries (no exited_at yet)
	h.db.Exec(r.Context(),
		`UPDATE agent_phase_history SET exited_at = NOW()
		 WHERE agent_id = $1 AND exited_at IS NULL`,
		agentID,
	)

	var ph models.AgentPhaseHistory
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_phase_history (agent_id, phase, triggered_by)
		 VALUES ($1, $2, $3)
		 RETURNING id, agent_id, phase, entered_at, exited_at, COALESCE(triggered_by,'')`,
		agentID, req.Phase, req.TriggeredBy,
	).Scan(&ph.ID, &ph.AgentID, &ph.Phase, &ph.EnteredAt, &ph.ExitedAt, &ph.TriggeredBy)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to log phase transition")
		return
	}

	// Sync current_phase on the agent row
	h.db.Exec(r.Context(),
		`UPDATE agents SET current_phase = $1, updated_at = NOW() WHERE id = $2`,
		req.Phase, agentID,
	)

	utils.JSON(w, http.StatusCreated, ph)
}

// agentBelongsToUser checks ownership without exposing whether the agent exists.
func (h *PhaseHandler) agentBelongsToUser(r *http.Request, agentID, userID string) bool {
	var exists bool
	err := h.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`,
		agentID, userID,
	).Scan(&exists)
	return err == nil && exists
}

