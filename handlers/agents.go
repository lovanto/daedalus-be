package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/models"
	"github.com/daedalus/daedalus-be/services"
	"github.com/daedalus/daedalus-be/utils"
)

type AgentHandler struct {
	db      *pgxpool.Pool
	service *services.AgentService
}

func NewAgentHandler(db *pgxpool.Pool, service *services.AgentService) *AgentHandler {
	return &AgentHandler{db: db, service: service}
}

// List godoc
// @Summary List all agents for the current user
// @Tags agents
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Router /api/agents [get]
func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := appMiddleware.GetUserID(r)

	rows, err := h.db.Query(r.Context(),
		`SELECT id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at
		 FROM agents
		 WHERE user_id = $1 AND deleted_at IS NULL
		 ORDER BY updated_at DESC`,
		userID,
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch agents")
		return
	}
	defer rows.Close()

	agents := make([]models.Agent, 0)
	for rows.Next() {
		var a models.Agent
		if err := rows.Scan(&a.ID, &a.UserID, &a.Name, &a.Description, &a.Status,
			&a.ConfidenceScore, &a.CurrentPhase, &a.CreatedAt, &a.UpdatedAt); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan agent")
			return
		}
		agents = append(agents, a)
	}

	utils.JSON(w, http.StatusOK, agents)
}

// Create godoc
// @Summary Create a new agent
// @Tags agents
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body map[string]interface{} true "name, description"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Router /api/agents [post]
func (h *AgentHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := appMiddleware.GetUserID(r)

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		utils.Err(w, http.StatusBadRequest, "name is required")
		return
	}

	var agent models.Agent
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agents (user_id, name, description, status, current_phase)
		 VALUES ($1, $2, $3, 'defining', 'define')
		 RETURNING id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at`,
		userID, req.Name, req.Description,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create agent")
		return
	}

	// Log initial phase entry
	h.db.Exec(r.Context(),
		`INSERT INTO agent_phase_history (agent_id, phase, triggered_by) VALUES ($1, 'define', 'system')`,
		agent.ID,
	)

	utils.JSON(w, http.StatusCreated, agent)
}

// Get godoc
// @Summary Get a single agent
// @Tags agents
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id} [get]
func (h *AgentHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	var agent models.Agent
	err := h.db.QueryRow(r.Context(),
		`SELECT id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at
		 FROM agents
		 WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		id, userID,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "agent not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch agent")
		return
	}

	utils.JSON(w, http.StatusOK, agent)
}

// Update godoc
// @Summary Partially update an agent
// @Tags agents
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "Fields to update"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id} [patch]
func (h *AgentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	// Fetch current values first to allow partial patch
	var agent models.Agent
	err := h.db.QueryRow(r.Context(),
		`SELECT id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at
		 FROM agents
		 WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		id, userID,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "agent not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch agent")
		return
	}

	var req struct {
		Name            *string  `json:"name"`
		Description     *string  `json:"description"`
		Status          *string  `json:"status"`
		CurrentPhase    *string  `json:"current_phase"`
		ConfidenceScore *float64 `json:"confidence_score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil {
		v := strings.TrimSpace(*req.Name)
		if v == "" {
			utils.Err(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		agent.Name = v
	}
	if req.Description != nil {
		agent.Description = *req.Description
	}
	if req.Status != nil {
		agent.Status = *req.Status
	}
	if req.CurrentPhase != nil {
		agent.CurrentPhase = *req.CurrentPhase
	}
	if req.ConfidenceScore != nil {
		agent.ConfidenceScore = *req.ConfidenceScore
	}

	err = h.db.QueryRow(r.Context(),
		`UPDATE agents
		 SET name = $1, description = $2, status = $3, current_phase = $4, confidence_score = $5, updated_at = NOW()
		 WHERE id = $6 AND user_id = $7 AND deleted_at IS NULL
		 RETURNING id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at`,
		agent.Name, agent.Description, agent.Status, agent.CurrentPhase, agent.ConfidenceScore, id, userID,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to update agent")
		return
	}

	utils.JSON(w, http.StatusOK, agent)
}

// Delete godoc
// @Summary Soft-delete an agent
// @Tags agents
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id} [delete]
func (h *AgentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	tag, err := h.db.Exec(r.Context(),
		`UPDATE agents SET deleted_at = NOW() WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		id, userID,
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to delete agent")
		return
	}
	if tag.RowsAffected() == 0 {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	utils.JSON(w, http.StatusOK, map[string]string{"message": "agent deleted"})
}

// Summary godoc
// @Summary Dashboard summary stats and recent activity
// @Tags dashboard
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Router /api/dashboard/summary [get]
func (h *AgentHandler) Summary(w http.ResponseWriter, r *http.Request) {
	userID := appMiddleware.GetUserID(r)

	summary := models.DashboardSummary{}

	err := h.db.QueryRow(r.Context(),
		`SELECT
		   COUNT(*)                                                                       AS total,
		   COUNT(*) FILTER (WHERE status IN ('building','evaluating','observing','tuning')) AS active_loops,
		   COALESCE(AVG(confidence_score), 0)                                            AS avg_confidence,
		   COUNT(*) FILTER (WHERE status = 'deploy_ready')                               AS deploy_ready
		 FROM agents
		 WHERE user_id = $1 AND deleted_at IS NULL`,
		userID,
	).Scan(&summary.TotalAgents, &summary.ActiveLoops, &summary.AvgConfidenceScore, &summary.DeployReadyCount)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch summary stats")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT ph.id, ph.agent_id, a.name, ph.phase, ph.entered_at, COALESCE(ph.triggered_by,'')
		 FROM agent_phase_history ph
		 JOIN agents a ON a.id = ph.agent_id
		 WHERE a.user_id = $1 AND a.deleted_at IS NULL
		 ORDER BY ph.entered_at DESC
		 LIMIT 5`,
		userID,
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch recent activity")
		return
	}
	defer rows.Close()

	summary.RecentActivity = make([]models.RecentActivityItem, 0)
	for rows.Next() {
		var item models.RecentActivityItem
		if err := rows.Scan(&item.ID, &item.AgentID, &item.AgentName, &item.Phase, &item.EnteredAt, &item.TriggeredBy); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan activity")
			return
		}
		summary.RecentActivity = append(summary.RecentActivity, item)
	}

	utils.JSON(w, http.StatusOK, summary)
}
