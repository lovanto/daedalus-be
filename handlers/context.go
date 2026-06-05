package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/models"
	"github.com/daedalus/daedalus-be/utils"
)

type ContextHandler struct {
	db *pgxpool.Pool
}

func NewContextHandler(db *pgxpool.Pool) *ContextHandler {
	return &ContextHandler{db: db}
}

// List godoc
// @Summary List all context snapshots for an agent
// @Tags context
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/context [get]
func (h *ContextHandler) List(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)
	pg := utils.ParsePagination(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, agent_id, tools_audit, knowledge_sources, COALESCE(memory_notes,''), created_at
		 FROM agent_context_snapshots
		 WHERE agent_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		agentID, pg.Limit, pg.Offset(),
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch context snapshots")
		return
	}
	defer rows.Close()

	snapshots := make([]models.AgentContextSnapshot, 0)
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan snapshot")
			return
		}
		snapshots = append(snapshots, s)
	}

	utils.JSON(w, http.StatusOK, snapshots)
}

// Create godoc
// @Summary Take a new context snapshot
// @Tags context
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "Context snapshot payload"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/context [post]
func (h *ContextHandler) Create(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		ToolsAudit       json.RawMessage `json:"tools_audit"`
		KnowledgeSources json.RawMessage `json:"knowledge_sources"`
		MemoryNotes      string          `json:"memory_notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var s models.AgentContextSnapshot
	var taBytes, ksBytes []byte
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_context_snapshots (agent_id, tools_audit, knowledge_sources, memory_notes)
		 VALUES ($1,$2,$3,$4)
		 RETURNING id, agent_id, tools_audit, knowledge_sources, COALESCE(memory_notes,''), created_at`,
		agentID, nullableJSON(req.ToolsAudit), nullableJSON(req.KnowledgeSources), req.MemoryNotes,
	).Scan(&s.ID, &s.AgentID, &taBytes, &ksBytes, &s.MemoryNotes, &s.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create context snapshot")
		return
	}
	s.ToolsAudit = jsonOrNull(taBytes)
	s.KnowledgeSources = jsonOrNull(ksBytes)

	utils.JSON(w, http.StatusCreated, s)
}

// GetLatest godoc
// @Summary Get the most recent context snapshot
// @Tags context
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/context/latest [get]
func (h *ContextHandler) GetLatest(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var s models.AgentContextSnapshot
	var taBytes, ksBytes []byte
	err := h.db.QueryRow(r.Context(),
		`SELECT id, agent_id, tools_audit, knowledge_sources, COALESCE(memory_notes,''), created_at
		 FROM agent_context_snapshots
		 WHERE agent_id = $1
		 ORDER BY created_at DESC LIMIT 1`,
		agentID,
	).Scan(&s.ID, &s.AgentID, &taBytes, &ksBytes, &s.MemoryNotes, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "no context snapshots found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch snapshot")
		return
	}
	s.ToolsAudit = jsonOrNull(taBytes)
	s.KnowledgeSources = jsonOrNull(ksBytes)

	utils.JSON(w, http.StatusOK, s)
}

// MarkToolVerified godoc
// @Summary Mark a specific tool in a context snapshot as verified
// @Tags context
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param snapshot_id path string true "Snapshot ID"
// @Param tool_name path string true "Tool name"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/context/{snapshot_id}/tools/{tool_name} [patch]
func (h *ContextHandler) MarkToolVerified(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	snapshotID := chi.URLParam(r, "snapshot_id")
	toolName := chi.URLParam(r, "tool_name")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	// Fetch current tools_audit
	var taBytes []byte
	err := h.db.QueryRow(r.Context(),
		`SELECT tools_audit FROM agent_context_snapshots WHERE id = $1 AND agent_id = $2`,
		snapshotID, agentID,
	).Scan(&taBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "snapshot not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch snapshot")
		return
	}

	// Merge verified status into the tool entry
	var audit map[string]interface{}
	if len(taBytes) > 0 {
		json.Unmarshal(taBytes, &audit)
	}
	if audit == nil {
		audit = make(map[string]interface{})
	}

	existing, _ := audit[toolName].(map[string]interface{})
	if existing == nil {
		existing = make(map[string]interface{})
	}
	existing["status"] = "verified"
	existing["verified_at"] = fmt.Sprintf("%v", r.Context().Value("now")) // use DB time below
	audit[toolName] = existing

	updatedBytes, _ := json.Marshal(audit)

	// Write back and use DB timestamp for verified_at
	var s models.AgentContextSnapshot
	var newTABytes, ksBytes []byte
	err = h.db.QueryRow(r.Context(),
		`UPDATE agent_context_snapshots
		 SET tools_audit = jsonb_set(
		       COALESCE(tools_audit,'{}'),
		       ARRAY[$1::text],
		       COALESCE(tools_audit->$1,'{}') ||
		       jsonb_build_object('status','verified','verified_at', NOW()::text)
		     )
		 WHERE id = $2 AND agent_id = $3
		 RETURNING id, agent_id, tools_audit, knowledge_sources, COALESCE(memory_notes,''), created_at`,
		toolName, snapshotID, agentID,
	).Scan(&s.ID, &s.AgentID, &newTABytes, &ksBytes, &s.MemoryNotes, &s.CreatedAt)

	_ = updatedBytes // used the SQL approach instead

	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to update tool status")
		return
	}
	s.ToolsAudit = jsonOrNull(newTABytes)
	s.KnowledgeSources = jsonOrNull(ksBytes)

	utils.JSON(w, http.StatusOK, s)
}

func scanSnapshot(rows pgx.Rows) (models.AgentContextSnapshot, error) {
	var s models.AgentContextSnapshot
	var taBytes, ksBytes []byte
	err := rows.Scan(&s.ID, &s.AgentID, &taBytes, &ksBytes, &s.MemoryNotes, &s.CreatedAt)
	if err != nil {
		return s, err
	}
	s.ToolsAudit = jsonOrNull(taBytes)
	s.KnowledgeSources = jsonOrNull(ksBytes)
	return s, nil
}
