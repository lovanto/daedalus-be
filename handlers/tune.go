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
		        context_refreshed, COALESCE(outcome_notes,''), created_at
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
		           context_refreshed, COALESCE(outcome_notes,''), created_at`,
		agentID, req.FailureTypeAddressed, nullableJSON(req.Changes),
		req.ContextRefreshed, req.OutcomeNotes,
	).Scan(&tc.ID, &tc.AgentID, &tc.FailureTypeAddressed, &changesBytes,
		&tc.ContextRefreshed, &tc.OutcomeNotes, &tc.CreatedAt)
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
		           context_refreshed, COALESCE(outcome_notes,''), created_at`,
		req.OutcomeNotes, req.ContextRefreshed, cycleID, agentID,
	).Scan(&tc.ID, &tc.AgentID, &tc.FailureTypeAddressed, &changesBytes,
		&tc.ContextRefreshed, &tc.OutcomeNotes, &tc.CreatedAt)
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

type tuneCycleRows interface {
	Scan(dest ...any) error
}

func scanTuneCycle(rows pgx.Rows) (models.AgentTuneCycle, error) {
	var tc models.AgentTuneCycle
	var changesBytes []byte
	err := rows.Scan(&tc.ID, &tc.AgentID, &tc.FailureTypeAddressed, &changesBytes,
		&tc.ContextRefreshed, &tc.OutcomeNotes, &tc.CreatedAt)
	if err != nil {
		return tc, err
	}
	tc.Changes = jsonOrNull(changesBytes)
	return tc, nil
}
