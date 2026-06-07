package handlers

import (
	"context"
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
// @Summary Apply a tune cycle as a new build version (+ optional definition version)
// @Description Creates a new build version with the approved system prompt and any
// @Description build-config overrides (temperature/model/tools). If a definition
// @Description patch is supplied, also writes a new definition version. Returns both.
// @Tags tune
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param cycle_id path string true "Tune cycle ID"
// @Param body body map[string]interface{} true "Approved build + optional definition patch"
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

	// Build-config overrides are optional: a nil pointer means "keep the current
	// build's value". A non-nil definition patch additionally writes a new
	// definition version (only the fields it carries are changed).
	var req struct {
		SystemPrompt  string          `json:"system_prompt"`
		ModelProvider *string         `json:"model_provider"`
		ModelName     *string         `json:"model_name"`
		Temperature   *float64        `json:"temperature"`
		MaxTokens     *int            `json:"max_tokens"`
		Tools         json.RawMessage `json:"tools"`
		Definition    *struct {
			Goals             *string         `json:"goals"`
			IntendedBehaviors json.RawMessage `json:"intended_behaviors"`
			Constraints       json.RawMessage `json:"constraints"`
			SuccessMetrics    json.RawMessage `json:"success_metrics"`
			UnsafeZones       *string         `json:"unsafe_zones"`
			SOPs              *string         `json:"sops"`
		} `json:"definition"`
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

	// Clone the latest build, then apply the approved prompt and any config overrides.
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

	// Resolve final build config: override where provided, else clone from src.
	provider := src.ModelProvider
	if req.ModelProvider != nil {
		provider = *req.ModelProvider
	}
	modelName := src.ModelName
	if req.ModelName != nil {
		modelName = *req.ModelName
	}
	temperature := src.Temperature
	if req.Temperature != nil {
		temperature = *req.Temperature
	}
	maxTokens := src.MaxTokens
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}
	tools := nullableJSON(srcTools)
	if len(req.Tools) > 0 && string(req.Tools) != "null" {
		tools = nullableJSON(req.Tools)
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
		agentID, nextVersion, provider, modelName, temperature,
		maxTokens, req.SystemPrompt, tools, note,
	).Scan(&b.ID, &b.AgentID, &b.Version, &b.ModelProvider, &b.ModelName,
		&b.Temperature, &b.MaxTokens, &b.SystemPrompt, &toolsBytes,
		&b.OrchestrationNotes, &b.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create build")
		return
	}
	b.Tools = jsonOrNull(toolsBytes)

	// Optionally write a new definition version, cloning the latest and patching
	// only the fields the tune cycle changed.
	var newDef *models.AgentDefinition
	if req.Definition != nil {
		def, derr := h.applyDefinitionPatch(r.Context(), agentID, cycleID,
			req.Definition.Goals, req.Definition.IntendedBehaviors, req.Definition.Constraints,
			req.Definition.SuccessMetrics, req.Definition.UnsafeZones, req.Definition.SOPs)
		if derr != nil {
			utils.Err(w, http.StatusInternalServerError, "build created but failed to write definition: "+derr.Error())
			return
		}
		newDef = def
	}

	// Link the cycle to the build it produced.
	if _, err := h.db.Exec(r.Context(),
		`UPDATE agent_tune_cycles SET applied_build_id = $1 WHERE id = $2 AND agent_id = $3`,
		b.ID, cycleID, agentID,
	); err != nil {
		utils.Err(w, http.StatusInternalServerError, "build created but failed to link tune cycle")
		return
	}

	utils.JSON(w, http.StatusCreated, map[string]interface{}{
		"build":      b,
		"definition": newDef,
	})
}

// applyDefinitionPatch clones the agent's latest definition into a new version,
// overriding only the non-nil fields from a tune cycle's definition changes.
func (h *TuneHandler) applyDefinitionPatch(
	ctx context.Context,
	agentID, cycleID string,
	goals *string,
	behaviors, constraints, metrics json.RawMessage,
	unsafeZones, sops *string,
) (*models.AgentDefinition, error) {
	// Start from the latest definition (or sensible defaults if none exists yet).
	var (
		curGoals, curUnsafe, curSOPs             string
		curThreshold                             float64
		curBehaviors, curConstraints, curMetrics []byte
	)
	err := h.db.QueryRow(ctx,
		`SELECT COALESCE(goals,''), intended_behaviors, constraints, success_metrics,
		        COALESCE(unsafe_zones,''), COALESCE(confidence_threshold,75), COALESCE(sops,'')
		 FROM agent_definitions
		 WHERE agent_id = $1
		 ORDER BY version DESC LIMIT 1`,
		agentID,
	).Scan(&curGoals, &curBehaviors, &curConstraints, &curMetrics, &curUnsafe, &curThreshold, &curSOPs)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		curThreshold = 75
	}

	// Patch only the fields the cycle carried.
	if goals != nil {
		curGoals = *goals
	}
	if unsafeZones != nil {
		curUnsafe = *unsafeZones
	}
	if sops != nil {
		curSOPs = *sops
	}
	if len(behaviors) > 0 && string(behaviors) != "null" {
		curBehaviors = behaviors
	}
	if len(constraints) > 0 && string(constraints) != "null" {
		curConstraints = constraints
	}
	if len(metrics) > 0 && string(metrics) != "null" {
		curMetrics = metrics
	}

	var nextVersion int
	h.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM agent_definitions WHERE agent_id = $1`,
		agentID,
	).Scan(&nextVersion)

	row := h.db.QueryRow(ctx,
		`INSERT INTO agent_definitions
		   (agent_id, version, goals, intended_behaviors, constraints, success_metrics,
		    unsafe_zones, confidence_threshold, sops)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, agent_id, version, COALESCE(goals,''), intended_behaviors, constraints,
		           success_metrics, COALESCE(unsafe_zones,''), confidence_threshold, COALESCE(sops,''), created_at`,
		agentID, nextVersion, curGoals,
		nullableJSON(jsonOrNull(curBehaviors)), nullableJSON(jsonOrNull(curConstraints)),
		nullableJSON(jsonOrNull(curMetrics)),
		curUnsafe, curThreshold, curSOPs,
	)
	def, err := scanDefinitionRow(row)
	if err != nil {
		return nil, err
	}
	return &def, nil
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
