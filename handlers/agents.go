package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/models"
	"github.com/daedalus/daedalus-be/services"
	"github.com/daedalus/daedalus-be/utils"
)

// versionSuffix matches a trailing " vN" so Evolve can bump the version number.
var versionSuffix = regexp.MustCompile(`\sv(\d+)$`)

// nextVersionName turns "Support Bot" → "Support Bot v2" and "Support Bot v2" → "Support Bot v3".
func nextVersionName(name string) string {
	if m := versionSuffix.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		base := strings.TrimSuffix(name, m[0])
		return fmt.Sprintf("%s v%d", base, n+1)
	}
	return name + " v2"
}

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
		`SELECT id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at, parent_agent_id
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
			&a.ConfidenceScore, &a.CurrentPhase, &a.CreatedAt, &a.UpdatedAt, &a.ParentAgentID); err != nil {
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
		`SELECT id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at, parent_agent_id
		 FROM agents
		 WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		id, userID,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt, &agent.ParentAgentID)
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
		`SELECT id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at, parent_agent_id
		 FROM agents
		 WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		id, userID,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt, &agent.ParentAgentID)
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
		 RETURNING id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at, parent_agent_id`,
		agent.Name, agent.Description, agent.Status, agent.CurrentPhase, agent.ConfidenceScore, id, userID,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt, &agent.ParentAgentID)
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

// ListDeleted godoc
// @Summary List soft-deleted agents that can be restored
// @Tags agents
// @Security BearerAuth
// @Produce json
// @Success 200 {array} models.Agent
// @Failure 401 {object} map[string]interface{}
// @Router /api/agents/deleted [get]
func (h *AgentHandler) ListDeleted(w http.ResponseWriter, r *http.Request) {
	userID := appMiddleware.GetUserID(r)

	rows, err := h.db.Query(r.Context(),
		`SELECT id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at, deleted_at
		 FROM agents
		 WHERE user_id = $1 AND deleted_at IS NOT NULL
		 ORDER BY deleted_at DESC`,
		userID,
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch deleted agents")
		return
	}
	defer rows.Close()

	agents := make([]models.Agent, 0)
	for rows.Next() {
		var a models.Agent
		if err := rows.Scan(&a.ID, &a.UserID, &a.Name, &a.Description, &a.Status,
			&a.ConfidenceScore, &a.CurrentPhase, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan agent")
			return
		}
		agents = append(agents, a)
	}

	utils.JSON(w, http.StatusOK, agents)
}

// Restore godoc
// @Summary Restore a soft-deleted agent
// @Tags agents
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/restore [post]
func (h *AgentHandler) Restore(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	tag, err := h.db.Exec(r.Context(),
		`UPDATE agents SET deleted_at = NULL WHERE id = $1 AND user_id = $2 AND deleted_at IS NOT NULL`,
		id, userID,
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to restore agent")
		return
	}
	if tag.RowsAffected() == 0 {
		utils.Err(w, http.StatusNotFound, "deleted agent not found")
		return
	}

	utils.JSON(w, http.StatusOK, map[string]string{"message": "agent restored"})
}

// Evolve godoc
// @Summary Create a new version of an agent, carrying its configuration forward
// @Description Clones the source agent's latest definition, build, and context snapshot into a
// @Description fresh v2 agent. Runtime history (evals, observations, tune cycles, confidence) starts
// @Description clean, and the original agent is left untouched.
// @Tags agents
// @Security BearerAuth
// @Produce json
// @Param id path string true "Source Agent ID"
// @Success 201 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/evolve [post]
func (h *AgentHandler) Evolve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	var srcName, srcDesc string
	err := h.db.QueryRow(r.Context(),
		`SELECT name, COALESCE(description,'') FROM agents WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		id, userID,
	).Scan(&srcName, &srcDesc)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "agent not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch agent")
		return
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck

	var agent models.Agent
	err = tx.QueryRow(r.Context(),
		`INSERT INTO agents (user_id, name, description, status, current_phase, parent_agent_id)
		 VALUES ($1, $2, $3, 'defining', 'define', $4)
		 RETURNING id, user_id, name, COALESCE(description,''), status, confidence_score, current_phase, created_at, updated_at, parent_agent_id`,
		userID, nextVersionName(srcName), srcDesc, id,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt, &agent.ParentAgentID)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create new version")
		return
	}

	// Carry the configuration forward: clone the latest definition, build, and
	// context snapshot as v1 of the new agent. INSERT...SELECT inserts zero rows
	// (no error) when the source phase is empty, so a half-built agent evolves fine.
	if _, err = tx.Exec(r.Context(),
		`INSERT INTO agent_definitions
		   (agent_id, version, goals, intended_behaviors, constraints, success_metrics, unsafe_zones, confidence_threshold, sops)
		 SELECT $1, 1, goals, intended_behaviors, constraints, success_metrics, unsafe_zones, confidence_threshold, sops
		 FROM agent_definitions WHERE agent_id = $2 ORDER BY version DESC LIMIT 1`,
		agent.ID, id,
	); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to clone definition")
		return
	}

	if _, err = tx.Exec(r.Context(),
		`INSERT INTO agent_builds
		   (agent_id, version, model_provider, model_name, temperature, max_tokens, system_prompt, tools, orchestration_notes)
		 SELECT $1, 1, model_provider, model_name, temperature, max_tokens, system_prompt, tools, orchestration_notes
		 FROM agent_builds WHERE agent_id = $2 ORDER BY version DESC LIMIT 1`,
		agent.ID, id,
	); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to clone build")
		return
	}

	if _, err = tx.Exec(r.Context(),
		`INSERT INTO agent_context_snapshots
		   (agent_id, tools_audit, knowledge_sources, memory_notes)
		 SELECT $1, tools_audit, knowledge_sources, memory_notes
		 FROM agent_context_snapshots WHERE agent_id = $2 ORDER BY created_at DESC LIMIT 1`,
		agent.ID, id,
	); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to clone context")
		return
	}

	tx.Exec(r.Context(), //nolint:errcheck
		`INSERT INTO agent_phase_history (agent_id, phase, triggered_by) VALUES ($1, 'define', 'evolve')`,
		agent.ID,
	)

	if err := tx.Commit(r.Context()); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to commit new version")
		return
	}

	utils.JSON(w, http.StatusCreated, agent)
}

// PermanentDelete godoc
// @Summary Permanently delete a soft-deleted agent and all its data
// @Description Hard-deletes the agent row; child records cascade. Only works on agents that are
// @Description already in the trash (deleted_at set) — active agents must be soft-deleted first.
// @Tags agents
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/permanent [delete]
func (h *AgentHandler) PermanentDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	// The deleted_at IS NOT NULL guard makes this irreversible action reachable
	// only from the trash — you can never hard-delete an active agent directly.
	tag, err := h.db.Exec(r.Context(),
		`DELETE FROM agents WHERE id = $1 AND user_id = $2 AND deleted_at IS NOT NULL`,
		id, userID,
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to permanently delete agent")
		return
	}
	if tag.RowsAffected() == 0 {
		utils.Err(w, http.StatusNotFound, "deleted agent not found")
		return
	}

	utils.JSON(w, http.StatusOK, map[string]string{"message": "agent permanently deleted"})
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
