package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/models"
	"github.com/daedalus/daedalus-be/utils"
)

type BuildsHandler struct {
	db *pgxpool.Pool
}

func NewBuildsHandler(db *pgxpool.Pool) *BuildsHandler {
	return &BuildsHandler{db: db}
}

// List godoc
// @Summary List all build snapshots for an agent
// @Tags builds
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/builds [get]
func (h *BuildsHandler) List(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)
	pg := utils.ParsePagination(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, agent_id, version, COALESCE(model_provider,''), COALESCE(model_name,''),
		        COALESCE(temperature,0), COALESCE(max_tokens,0), COALESCE(system_prompt,''),
		        tools, COALESCE(orchestration_notes,''), created_at
		 FROM agent_builds
		 WHERE agent_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		agentID, pg.Limit, pg.Offset(),
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch builds")
		return
	}
	defer rows.Close()

	builds := make([]models.AgentBuild, 0)
	for rows.Next() {
		b, err := scanBuild(rows)
		if err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan build")
			return
		}
		builds = append(builds, b)
	}

	utils.JSON(w, http.StatusOK, builds)
}

// Create godoc
// @Summary Save a new build snapshot
// @Tags builds
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "Build payload"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/builds [post]
func (h *BuildsHandler) Create(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		ModelProvider      string          `json:"model_provider"`
		ModelName          string          `json:"model_name"`
		Temperature        float64         `json:"temperature"`
		MaxTokens          int             `json:"max_tokens"`
		SystemPrompt       string          `json:"system_prompt"`
		Tools              json.RawMessage `json:"tools"`
		OrchestrationNotes string          `json:"orchestration_notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var nextVersion int
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(MAX(version), 0) + 1 FROM agent_builds WHERE agent_id = $1`,
		agentID,
	).Scan(&nextVersion)

	var b models.AgentBuild
	var toolsBytes []byte
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_builds
		   (agent_id, version, model_provider, model_name, temperature, max_tokens,
		    system_prompt, tools, orchestration_notes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, agent_id, version, COALESCE(model_provider,''), COALESCE(model_name,''),
		           COALESCE(temperature,0), COALESCE(max_tokens,0), COALESCE(system_prompt,''),
		           tools, COALESCE(orchestration_notes,''), created_at`,
		agentID, nextVersion, req.ModelProvider, req.ModelName, req.Temperature,
		req.MaxTokens, req.SystemPrompt, nullableJSON(req.Tools), req.OrchestrationNotes,
	).Scan(&b.ID, &b.AgentID, &b.Version, &b.ModelProvider, &b.ModelName,
		&b.Temperature, &b.MaxTokens, &b.SystemPrompt, &toolsBytes,
		&b.OrchestrationNotes, &b.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create build")
		return
	}
	b.Tools = jsonOrNull(toolsBytes)

	utils.JSON(w, http.StatusCreated, b)
}

// GetLatest godoc
// @Summary Get the most recent build snapshot
// @Tags builds
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/builds/latest [get]
func (h *BuildsHandler) GetLatest(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var b models.AgentBuild
	var toolsBytes []byte
	err := h.db.QueryRow(r.Context(),
		`SELECT id, agent_id, version, COALESCE(model_provider,''), COALESCE(model_name,''),
		        COALESCE(temperature,0), COALESCE(max_tokens,0), COALESCE(system_prompt,''),
		        tools, COALESCE(orchestration_notes,''), created_at
		 FROM agent_builds
		 WHERE agent_id = $1
		 ORDER BY created_at DESC LIMIT 1`,
		agentID,
	).Scan(&b.ID, &b.AgentID, &b.Version, &b.ModelProvider, &b.ModelName,
		&b.Temperature, &b.MaxTokens, &b.SystemPrompt, &toolsBytes,
		&b.OrchestrationNotes, &b.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "no builds found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch build")
		return
	}
	b.Tools = jsonOrNull(toolsBytes)

	utils.JSON(w, http.StatusOK, b)
}

func scanBuild(rows pgx.Rows) (models.AgentBuild, error) {
	var b models.AgentBuild
	var toolsBytes []byte
	err := rows.Scan(&b.ID, &b.AgentID, &b.Version, &b.ModelProvider, &b.ModelName,
		&b.Temperature, &b.MaxTokens, &b.SystemPrompt, &toolsBytes,
		&b.OrchestrationNotes, &b.CreatedAt)
	if err != nil {
		return b, err
	}
	b.Tools = jsonOrNull(toolsBytes)
	return b, nil
}
