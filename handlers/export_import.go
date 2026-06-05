package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/daedalus/daedalus-be/models"
	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/utils"
)

// ExportHandler handles full agent export and import.
type ExportHandler struct {
	db *pgxpool.Pool
}

func NewExportHandler(db *pgxpool.Pool) *ExportHandler {
	return &ExportHandler{db: db}
}

type agentExport struct {
	ExportedAt       time.Time       `json:"exported_at"`
	Version          string          `json:"version"`
	Agent            json.RawMessage `json:"agent"`
	Definitions      json.RawMessage `json:"definitions"`
	Builds           json.RawMessage `json:"builds"`
	ContextSnapshots json.RawMessage `json:"context_snapshots"`
	Evals            json.RawMessage `json:"evals"`
	EvalCases        json.RawMessage `json:"eval_cases"`
	Observations     json.RawMessage `json:"observations"`
	TuneCycles       json.RawMessage `json:"tune_cycles"`
	PhaseHistory     json.RawMessage `json:"phase_history"`
}

// Export godoc
// @Summary Export full agent data as JSON
// @Tags agents
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/export [get]
func (h *ExportHandler) Export(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	exp := agentExport{
		ExportedAt: time.Now().UTC(),
		Version:    "1.0",
	}

	// Agent row as JSON
	var agentRaw []byte
	err := h.db.QueryRow(r.Context(),
		`SELECT to_jsonb(a) FROM (
		   SELECT id, user_id, name, COALESCE(description,'') AS description,
		          status, confidence_score, current_phase, created_at, updated_at
		   FROM agents WHERE id = $1 AND deleted_at IS NULL
		 ) a`,
		agentID,
	).Scan(&agentRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "agent not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to export agent")
		return
	}
	exp.Agent = json.RawMessage(agentRaw)

	// Each sub-table via json_agg
	type tableQuery struct {
		dest *json.RawMessage
		sql  string
	}
	queries := []tableQuery{
		{&exp.Definitions, `SELECT COALESCE(json_agg(to_jsonb(d) ORDER BY d.version),'[]') FROM agent_definitions d WHERE d.agent_id=$1`},
		{&exp.Builds, `SELECT COALESCE(json_agg(to_jsonb(b) ORDER BY b.version),'[]') FROM agent_builds b WHERE b.agent_id=$1`},
		{&exp.ContextSnapshots, `SELECT COALESCE(json_agg(to_jsonb(c) ORDER BY c.created_at),'[]') FROM agent_context_snapshots c WHERE c.agent_id=$1`},
		{&exp.Evals, `SELECT COALESCE(json_agg(to_jsonb(e) ORDER BY e.created_at),'[]') FROM agent_evals e WHERE e.agent_id=$1`},
		{&exp.EvalCases, `SELECT COALESCE(json_agg(to_jsonb(ec) ORDER BY ec.created_at),'[]') FROM agent_eval_cases ec WHERE ec.agent_id=$1`},
		{&exp.Observations, `SELECT COALESCE(json_agg(to_jsonb(o) ORDER BY o.created_at),'[]') FROM agent_observations o WHERE o.agent_id=$1`},
		{&exp.TuneCycles, `SELECT COALESCE(json_agg(to_jsonb(tc) ORDER BY tc.created_at),'[]') FROM agent_tune_cycles tc WHERE tc.agent_id=$1`},
		{&exp.PhaseHistory, `SELECT COALESCE(json_agg(to_jsonb(ph) ORDER BY ph.entered_at),'[]') FROM agent_phase_history ph WHERE ph.agent_id=$1`},
	}
	for _, q := range queries {
		var raw []byte
		if scanErr := h.db.QueryRow(r.Context(), q.sql, agentID).Scan(&raw); scanErr == nil && raw != nil {
			*q.dest = json.RawMessage(raw)
		} else {
			*q.dest = json.RawMessage("[]")
		}
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="agent-%s.json"`, agentID))
	utils.JSON(w, http.StatusOK, exp)
}

// Import godoc
// @Summary Import an agent from a previously exported JSON
// @Tags agents
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body map[string]interface{} true "Exported agent JSON"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Router /api/agents/import [post]
func (h *ExportHandler) Import(w http.ResponseWriter, r *http.Request) {
	userID := appMiddleware.GetUserID(r)

	var payload agentExport
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid export JSON")
		return
	}

	var agentData map[string]interface{}
	if err := json.Unmarshal(payload.Agent, &agentData); err != nil || agentData == nil {
		utils.Err(w, http.StatusBadRequest, "invalid agent field in export")
		return
	}
	name, _ := agentData["name"].(string)
	description, _ := agentData["description"].(string)
	if name == "" {
		utils.Err(w, http.StatusBadRequest, "agent name is required")
		return
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck

	var newID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO agents (user_id, name, description, status, current_phase)
		 VALUES ($1, $2, $3, 'defining', 'define') RETURNING id`,
		userID, name+" (import)", description,
	).Scan(&newID)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create agent")
		return
	}

	// Helper utilities
	str := func(m map[string]interface{}, k string) string { v, _ := m[k].(string); return v }
	num := func(m map[string]interface{}, k string) float64 {
		switch v := m[k].(type) {
		case float64:
			return v
		case json.Number:
			f, _ := v.Float64()
			return f
		}
		return 0
	}
	rawJSON := func(m map[string]interface{}, k string) interface{} {
		if v := m[k]; v != nil {
			b, _ := json.Marshal(v)
			return json.RawMessage(b)
		}
		return nil
	}
	boolVal := func(m map[string]interface{}, k string) bool { v, _ := m[k].(bool); return v }

	insertAll := func(data json.RawMessage, fn func(map[string]interface{}) error) error {
		if len(data) == 0 || string(data) == "null" || string(data) == "[]" {
			return nil
		}
		var rows []map[string]interface{}
		if err := json.Unmarshal(data, &rows); err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
		}
		return nil
	}

	// Definitions
	if err := insertAll(payload.Definitions, func(d map[string]interface{}) error {
		_, e := tx.Exec(r.Context(),
			`INSERT INTO agent_definitions (agent_id,version,goals,intended_behaviors,constraints,success_metrics,unsafe_zones,confidence_threshold,sops)
			 VALUES ($1,(SELECT COALESCE(MAX(version),0)+1 FROM agent_definitions WHERE agent_id=$1),$2,$3,$4,$5,$6,$7,$8)`,
			newID, str(d, "goals"), rawJSON(d, "intended_behaviors"), rawJSON(d, "constraints"),
			rawJSON(d, "success_metrics"), str(d, "unsafe_zones"), num(d, "confidence_threshold"), str(d, "sops"),
		)
		return e
	}); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to import definitions")
		return
	}

	// Builds
	if err := insertAll(payload.Builds, func(d map[string]interface{}) error {
		_, e := tx.Exec(r.Context(),
			`INSERT INTO agent_builds (agent_id,version,model_provider,model_name,temperature,max_tokens,system_prompt,tools,orchestration_notes)
			 VALUES ($1,(SELECT COALESCE(MAX(version),0)+1 FROM agent_builds WHERE agent_id=$1),$2,$3,$4,$5,$6,$7,$8)`,
			newID, str(d, "model_provider"), str(d, "model_name"),
			num(d, "temperature"), int(num(d, "max_tokens")),
			str(d, "system_prompt"), rawJSON(d, "tools"), str(d, "orchestration_notes"),
		)
		return e
	}); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to import builds")
		return
	}

	// Context snapshots
	if err := insertAll(payload.ContextSnapshots, func(d map[string]interface{}) error {
		_, e := tx.Exec(r.Context(),
			`INSERT INTO agent_context_snapshots (agent_id,tools_audit,knowledge_sources,memory_notes)
			 VALUES ($1,$2,$3,$4)`,
			newID, rawJSON(d, "tools_audit"), rawJSON(d, "knowledge_sources"), str(d, "memory_notes"),
		)
		return e
	}); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to import context")
		return
	}

	// Evals
	if err := insertAll(payload.Evals, func(d map[string]interface{}) error {
		_, e := tx.Exec(r.Context(),
			`INSERT INTO agent_evals (agent_id,score,failure_type,test_cases_passed,test_cases_failed,notes,source)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			newID, num(d, "score"), str(d, "failure_type"),
			int(num(d, "test_cases_passed")), int(num(d, "test_cases_failed")),
			str(d, "notes"), str(d, "source"),
		)
		return e
	}); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to import evals")
		return
	}

	// Eval cases
	if err := insertAll(payload.EvalCases, func(d map[string]interface{}) error {
		_, e := tx.Exec(r.Context(),
			`INSERT INTO agent_eval_cases (agent_id,input,expected_behavior,category,is_active)
			 VALUES ($1,$2,$3,$4,$5)`,
			newID, str(d, "input"), str(d, "expected_behavior"), str(d, "category"), boolVal(d, "is_active"),
		)
		return e
	}); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to import eval cases")
		return
	}

	// Observations
	if err := insertAll(payload.Observations, func(d map[string]interface{}) error {
		_, e := tx.Exec(r.Context(),
			`INSERT INTO agent_observations (agent_id,pattern,severity,source,routed_to,tags)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			newID, str(d, "pattern"), str(d, "severity"), str(d, "source"), str(d, "routed_to"), rawJSON(d, "tags"),
		)
		return e
	}); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to import observations")
		return
	}

	// Tune cycles
	if err := insertAll(payload.TuneCycles, func(d map[string]interface{}) error {
		_, e := tx.Exec(r.Context(),
			`INSERT INTO agent_tune_cycles (agent_id,failure_type_addressed,changes,context_refreshed,outcome_notes)
			 VALUES ($1,$2,$3,$4,$5)`,
			newID, str(d, "failure_type_addressed"), rawJSON(d, "changes"), boolVal(d, "context_refreshed"), str(d, "outcome_notes"),
		)
		return e
	}); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to import tune cycles")
		return
	}

	// Phase history (best-effort — non-fatal)
	insertAll(payload.PhaseHistory, func(d map[string]interface{}) error { //nolint:errcheck
		tx.Exec(r.Context(),
			`INSERT INTO agent_phase_history (agent_id,phase,triggered_by) VALUES ($1,$2,'import')`,
			newID, str(d, "phase"),
		)
		return nil
	})

	// Seed the initial phase entry
	tx.Exec(r.Context(), //nolint:errcheck
		`INSERT INTO agent_phase_history (agent_id,phase,triggered_by) VALUES ($1,'define','import')`,
		newID,
	)

	if err := tx.Commit(r.Context()); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to commit import")
		return
	}

	var agent models.Agent
	h.db.QueryRow(r.Context(), //nolint:errcheck
		`SELECT id,user_id,name,COALESCE(description,''),status,confidence_score,current_phase,created_at,updated_at
		 FROM agents WHERE id=$1`,
		newID,
	).Scan(&agent.ID, &agent.UserID, &agent.Name, &agent.Description, &agent.Status,
		&agent.ConfidenceScore, &agent.CurrentPhase, &agent.CreatedAt, &agent.UpdatedAt)

	utils.JSON(w, http.StatusCreated, agent)
}
