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

type TuneHandler struct {
	db *pgxpool.Pool
}

func NewTuneHandler(db *pgxpool.Pool) *TuneHandler {
	return &TuneHandler{db: db}
}

// List godoc
// @Summary List all tune cycles for an agent
// @Tags tune
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param page query int false "Page" default(1)
// @Param limit query int false "Limit" default(20)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/tune-cycles [get]
func (h *TuneHandler) List(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)
	pg := utils.ParsePagination(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, agent_id, COALESCE(failure_type_addressed,''), changes,
		        context_refreshed, COALESCE(outcome_notes,''), applied_build_id, created_at
		 FROM agent_tune_cycles
		 WHERE agent_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		agentID, pg.Limit, pg.Offset(),
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch tune cycles")
		return
	}
	defer rows.Close()

	cycles := make([]models.AgentTuneCycle, 0)
	for rows.Next() {
		tc, err := scanTuneCycle(rows)
		if err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan tune cycle")
			return
		}
		cycles = append(cycles, tc)
	}

	utils.JSON(w, http.StatusOK, cycles)
}

// Create godoc
// @Summary Create a new tune cycle
// @Tags tune
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "Tune cycle payload"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/tune-cycles [post]
func (h *TuneHandler) Create(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		FailureTypeAddressed string          `json:"failure_type_addressed"`
		Changes              json.RawMessage `json:"changes"`
		ContextRefreshed     bool            `json:"context_refreshed"`
		OutcomeNotes         string          `json:"outcome_notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var tc models.AgentTuneCycle
	var changesBytes []byte
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_tune_cycles
		   (agent_id, failure_type_addressed, changes, context_refreshed, outcome_notes)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, agent_id, COALESCE(failure_type_addressed,''), changes,
		           context_refreshed, COALESCE(outcome_notes,''), applied_build_id, created_at`,
		agentID, req.FailureTypeAddressed, nullableJSON(req.Changes),
		req.ContextRefreshed, req.OutcomeNotes,
	).Scan(&tc.ID, &tc.AgentID, &tc.FailureTypeAddressed, &changesBytes,
		&tc.ContextRefreshed, &tc.OutcomeNotes, &tc.AppliedBuildID, &tc.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create tune cycle")
		return
	}
	tc.Changes = jsonOrNull(changesBytes)

	utils.JSON(w, http.StatusCreated, tc)
}

// UpdateOutcome godoc
// @Summary Update the outcome notes for a tune cycle
// @Tags tune
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param cycle_id path string true "Tune cycle ID"
// @Param body body map[string]interface{} true "Update payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/tune-cycles/{cycle_id} [patch]
func (h *TuneHandler) UpdateOutcome(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	cycleID := chi.URLParam(r, "cycle_id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		OutcomeNotes     *string `json:"outcome_notes"`
		ContextRefreshed *bool   `json:"context_refreshed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var tc models.AgentTuneCycle
	var changesBytes []byte
	err := h.db.QueryRow(r.Context(),
		`UPDATE agent_tune_cycles
		 SET outcome_notes      = COALESCE($1, outcome_notes),
		     context_refreshed  = COALESCE($2, context_refreshed)
		 WHERE id = $3 AND agent_id = $4
		 RETURNING id, agent_id, COALESCE(failure_type_addressed,''), changes,
		           context_refreshed, COALESCE(outcome_notes,''), applied_build_id, created_at`,
		req.OutcomeNotes, req.ContextRefreshed, cycleID, agentID,
	).Scan(&tc.ID, &tc.AgentID, &tc.FailureTypeAddressed, &changesBytes,
		&tc.ContextRefreshed, &tc.OutcomeNotes, &tc.AppliedBuildID, &tc.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "tune cycle not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to update tune cycle")
		return
	}
	tc.Changes = jsonOrNull(changesBytes)

	utils.JSON(w, http.StatusOK, tc)
}

// Apply godoc
// @Summary Apply a tune cycle's system-prompt fix as a new build version
// @Tags tune
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param cycle_id path string true "Tune cycle ID"
// @Param body body map[string]interface{} true "Approved system prompt"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/tune-cycles/{cycle_id}/apply [post]
func (h *TuneHandler) Apply(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	cycleID := chi.URLParam(r, "cycle_id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SystemPrompt == "" {
		utils.Err(w, http.StatusBadRequest, "system_prompt is required")
		return
	}

	// The cycle must exist and belong to this agent.
	var alreadyApplied *string
	err := h.db.QueryRow(r.Context(),
		`SELECT applied_build_id FROM agent_tune_cycles WHERE id = $1 AND agent_id = $2`,
		cycleID, agentID,
	).Scan(&alreadyApplied)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "tune cycle not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch tune cycle")
		return
	}
	if alreadyApplied != nil {
		utils.Err(w, http.StatusBadRequest, "tune cycle already applied to a build")
		return
	}

	// Clone the latest build, swapping in the approved system prompt as a new version.
	var src models.AgentBuild
	var srcTools []byte
	err = h.db.QueryRow(r.Context(),
		`SELECT version, COALESCE(model_provider,''), COALESCE(model_name,''),
		        COALESCE(temperature,0), COALESCE(max_tokens,0), tools,
		        COALESCE(orchestration_notes,'')
		 FROM agent_builds
		 WHERE agent_id = $1
		 ORDER BY created_at DESC LIMIT 1`,
		agentID,
	).Scan(&src.Version, &src.ModelProvider, &src.ModelName, &src.Temperature,
		&src.MaxTokens, &srcTools, &src.OrchestrationNotes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusBadRequest, "no build to apply onto — create a build first")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch latest build")
		return
	}

	nextVersion := src.Version + 1
	note := "Applied from tune cycle " + cycleID

	var b models.AgentBuild
	var toolsBytes []byte
	err = h.db.QueryRow(r.Context(),
		`INSERT INTO agent_builds
		   (agent_id, version, model_provider, model_name, temperature, max_tokens,
		    system_prompt, tools, orchestration_notes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, agent_id, version, COALESCE(model_provider,''), COALESCE(model_name,''),
		           COALESCE(temperature,0), COALESCE(max_tokens,0), COALESCE(system_prompt,''),
		           tools, COALESCE(orchestration_notes,''), created_at`,
		agentID, nextVersion, src.ModelProvider, src.ModelName, src.Temperature,
		src.MaxTokens, req.SystemPrompt, nullableJSON(srcTools), note,
	).Scan(&b.ID, &b.AgentID, &b.Version, &b.ModelProvider, &b.ModelName,
		&b.Temperature, &b.MaxTokens, &b.SystemPrompt, &toolsBytes,
		&b.OrchestrationNotes, &b.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create build")
		return
	}
	b.Tools = jsonOrNull(toolsBytes)

	// Link the cycle to the build it produced.
	if _, err := h.db.Exec(r.Context(),
		`UPDATE agent_tune_cycles SET applied_build_id = $1 WHERE id = $2 AND agent_id = $3`,
		b.ID, cycleID, agentID,
	); err != nil {
		utils.Err(w, http.StatusInternalServerError, "build created but failed to link tune cycle")
		return
	}

	utils.JSON(w, http.StatusCreated, b)
}

func scanTuneCycle(rows pgx.Rows) (models.AgentTuneCycle, error) {
	var tc models.AgentTuneCycle
	var changesBytes []byte
	err := rows.Scan(&tc.ID, &tc.AgentID, &tc.FailureTypeAddressed, &changesBytes,
		&tc.ContextRefreshed, &tc.OutcomeNotes, &tc.AppliedBuildID, &tc.CreatedAt)
	if err != nil {
		return tc, err
	}
	tc.Changes = jsonOrNull(changesBytes)
	return tc, nil
}
