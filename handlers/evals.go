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
	"github.com/daedalus/daedalus-be/services"
	"github.com/daedalus/daedalus-be/utils"
)

type EvalsHandler struct {
	db      *pgxpool.Pool
	service *services.AgentService
}

func NewEvalsHandler(db *pgxpool.Pool, service *services.AgentService) *EvalsHandler {
	return &EvalsHandler{db: db, service: service}
}

// List godoc
// @Summary List all eval runs for an agent (newest first)
// @Tags evals
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param page query int false "Page" default(1)
// @Param limit query int false "Limit" default(20)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/evals [get]
func (h *EvalsHandler) List(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)
	pg := utils.ParsePagination(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, agent_id, score, failure_type, test_cases_passed, test_cases_failed,
		        COALESCE(notes,''), source, created_at
		 FROM agent_evals
		 WHERE agent_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		agentID, pg.Limit, pg.Offset(),
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch evals")
		return
	}
	defer rows.Close()

	evals := make([]models.AgentEval, 0)
	for rows.Next() {
		var e models.AgentEval
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Score, &e.FailureType,
			&e.TestCasesPassed, &e.TestCasesFailed, &e.Notes, &e.Source, &e.CreatedAt); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan eval")
			return
		}
		evals = append(evals, e)
	}

	utils.JSON(w, http.StatusOK, evals)
}

// Create godoc
// @Summary Record a new eval run (triggers confidence recalculation)
// @Tags evals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "Eval payload"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/evals [post]
func (h *EvalsHandler) Create(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		Score           float64 `json:"score"`
		FailureType     string  `json:"failure_type"`
		TestCasesPassed int     `json:"test_cases_passed"`
		TestCasesFailed int     `json:"test_cases_failed"`
		Notes           string  `json:"notes"`
		Source          string  `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Score < 0 || req.Score > 100 {
		utils.Err(w, http.StatusBadRequest, "score must be between 0 and 100")
		return
	}
	if req.FailureType == "" {
		req.FailureType = "none"
	}
	if req.Source == "" {
		req.Source = "manual"
	}

	var e models.AgentEval
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_evals
		   (agent_id, score, failure_type, test_cases_passed, test_cases_failed, notes, source)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING id, agent_id, score, failure_type, test_cases_passed, test_cases_failed,
		           COALESCE(notes,''), source, created_at`,
		agentID, req.Score, req.FailureType, req.TestCasesPassed,
		req.TestCasesFailed, req.Notes, req.Source,
	).Scan(&e.ID, &e.AgentID, &e.Score, &e.FailureType,
		&e.TestCasesPassed, &e.TestCasesFailed, &e.Notes, &e.Source, &e.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create eval")
		return
	}

	// Recalculate confidence score asynchronously (best-effort)
	go h.service.CalculateConfidenceScore(r.Context(), agentID) //nolint:errcheck

	result := struct {
		models.AgentEval
		GateA models.GateADecision `json:"gate_a"`
	}{
		AgentEval: e,
		GateA:     models.GateA(e.FailureType),
	}

	utils.JSON(w, http.StatusCreated, result)
}

// GetLatest godoc
// @Summary Get the latest eval run with Gate A decision
// @Tags evals
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/evals/latest [get]
func (h *EvalsHandler) GetLatest(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var e models.AgentEval
	err := h.db.QueryRow(r.Context(),
		`SELECT id, agent_id, score, failure_type, test_cases_passed, test_cases_failed,
		        COALESCE(notes,''), source, created_at
		 FROM agent_evals
		 WHERE agent_id = $1
		 ORDER BY created_at DESC LIMIT 1`,
		agentID,
	).Scan(&e.ID, &e.AgentID, &e.Score, &e.FailureType,
		&e.TestCasesPassed, &e.TestCasesFailed, &e.Notes, &e.Source, &e.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "no evals found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch eval")
		return
	}

	result := models.AgentEvalWithGateA{AgentEval: e, GateA: models.GateA(e.FailureType)}
	utils.JSON(w, http.StatusOK, result)
}

// ListCases godoc
// @Summary List all eval test cases for an agent
// @Tags evals
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param page query int false "Page" default(1)
// @Param limit query int false "Limit" default(20)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/eval-cases [get]
func (h *EvalsHandler) ListCases(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)
	pg := utils.ParsePagination(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, agent_id, input, expected_behavior, category, is_active, created_at
		 FROM agent_eval_cases
		 WHERE agent_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		agentID, pg.Limit, pg.Offset(),
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch eval cases")
		return
	}
	defer rows.Close()

	cases := make([]models.AgentEvalCase, 0)
	for rows.Next() {
		var c models.AgentEvalCase
		if err := rows.Scan(&c.ID, &c.AgentID, &c.Input, &c.ExpectedBehavior,
			&c.Category, &c.IsActive, &c.CreatedAt); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan eval case")
			return
		}
		cases = append(cases, c)
	}

	utils.JSON(w, http.StatusOK, cases)
}

// CreateCase godoc
// @Summary Add a new eval test case
// @Tags evals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "Test case payload"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/eval-cases [post]
func (h *EvalsHandler) CreateCase(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		Input            string `json:"input"`
		ExpectedBehavior string `json:"expected_behavior"`
		Category         string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Input == "" || req.ExpectedBehavior == "" {
		utils.Err(w, http.StatusBadRequest, "input and expected_behavior are required")
		return
	}
	if req.Category == "" {
		req.Category = "core"
	}

	var c models.AgentEvalCase
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_eval_cases (agent_id, input, expected_behavior, category)
		 VALUES ($1,$2,$3,$4)
		 RETURNING id, agent_id, input, expected_behavior, category, is_active, created_at`,
		agentID, req.Input, req.ExpectedBehavior, req.Category,
	).Scan(&c.ID, &c.AgentID, &c.Input, &c.ExpectedBehavior, &c.Category, &c.IsActive, &c.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create eval case")
		return
	}

	utils.JSON(w, http.StatusCreated, c)
}

// UpdateCase godoc
// @Summary Update or retire an eval test case
// @Tags evals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param case_id path string true "Case ID"
// @Param body body map[string]interface{} true "Update payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/eval-cases/{case_id} [patch]
func (h *EvalsHandler) UpdateCase(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	caseID := chi.URLParam(r, "case_id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		Input            *string `json:"input"`
		ExpectedBehavior *string `json:"expected_behavior"`
		Category         *string `json:"category"`
		IsActive         *bool   `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Fetch current
	var c models.AgentEvalCase
	err := h.db.QueryRow(r.Context(),
		`SELECT id, agent_id, input, expected_behavior, category, is_active, created_at
		 FROM agent_eval_cases WHERE id = $1 AND agent_id = $2`,
		caseID, agentID,
	).Scan(&c.ID, &c.AgentID, &c.Input, &c.ExpectedBehavior, &c.Category, &c.IsActive, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "eval case not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch eval case")
		return
	}

	if req.Input != nil {
		c.Input = *req.Input
	}
	if req.ExpectedBehavior != nil {
		c.ExpectedBehavior = *req.ExpectedBehavior
	}
	if req.Category != nil {
		c.Category = *req.Category
	}
	if req.IsActive != nil {
		c.IsActive = *req.IsActive
	}

	err = h.db.QueryRow(r.Context(),
		`UPDATE agent_eval_cases
		 SET input = $1, expected_behavior = $2, category = $3, is_active = $4
		 WHERE id = $5 AND agent_id = $6
		 RETURNING id, agent_id, input, expected_behavior, category, is_active, created_at`,
		c.Input, c.ExpectedBehavior, c.Category, c.IsActive, caseID, agentID,
	).Scan(&c.ID, &c.AgentID, &c.Input, &c.ExpectedBehavior, &c.Category, &c.IsActive, &c.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to update eval case")
		return
	}

	utils.JSON(w, http.StatusOK, c)
}

// ListCaseRuns godoc
// @Summary List per-test-case run history for an agent (newest first)
// @Tags evals
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param page query int false "Page" default(1)
// @Param limit query int false "Limit" default(20)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/eval-case-runs [get]
func (h *EvalsHandler) ListCaseRuns(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)
	pg := utils.ParsePagination(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, case_id, agent_id, passed, actual_output, reasoning, created_at
		 FROM agent_eval_case_runs
		 WHERE agent_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		agentID, pg.Limit, pg.Offset(),
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch case runs")
		return
	}
	defer rows.Close()

	runs := make([]models.AgentEvalCaseRun, 0)
	for rows.Next() {
		var run models.AgentEvalCaseRun
		if err := rows.Scan(&run.ID, &run.CaseID, &run.AgentID, &run.Passed,
			&run.ActualOutput, &run.Reasoning, &run.CreatedAt); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan case run")
			return
		}
		runs = append(runs, run)
	}

	utils.JSON(w, http.StatusOK, runs)
}

// CreateCaseRun godoc
// @Summary Record a single eval case run result
// @Tags evals
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param case_id path string true "Case ID"
// @Param body body map[string]interface{} true "Run result payload"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/eval-cases/{case_id}/runs [post]
func (h *EvalsHandler) CreateCaseRun(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	caseID := chi.URLParam(r, "case_id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	// Ensure the case belongs to this agent before recording a run against it.
	var exists bool
	if err := h.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM agent_eval_cases WHERE id = $1 AND agent_id = $2)`,
		caseID, agentID,
	).Scan(&exists); err != nil || !exists {
		utils.Err(w, http.StatusNotFound, "eval case not found")
		return
	}

	var req struct {
		Passed       bool   `json:"passed"`
		ActualOutput string `json:"actual_output"`
		Reasoning    string `json:"reasoning"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var run models.AgentEvalCaseRun
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_eval_case_runs (case_id, agent_id, passed, actual_output, reasoning)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, case_id, agent_id, passed, actual_output, reasoning, created_at`,
		caseID, agentID, req.Passed, req.ActualOutput, req.Reasoning,
	).Scan(&run.ID, &run.CaseID, &run.AgentID, &run.Passed,
		&run.ActualOutput, &run.Reasoning, &run.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to record case run")
		return
	}

	utils.JSON(w, http.StatusCreated, run)
}
