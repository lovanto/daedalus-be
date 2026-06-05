package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/models"
	"github.com/daedalus/daedalus-be/utils"
)

type DefinitionsHandler struct {
	db *pgxpool.Pool
}

func NewDefinitionsHandler(db *pgxpool.Pool) *DefinitionsHandler {
	return &DefinitionsHandler{db: db}
}

// List godoc
// @Summary List all definition versions for an agent
// @Tags definitions
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/definitions [get]
func (h *DefinitionsHandler) List(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)
	pg := utils.ParsePagination(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, agent_id, version, COALESCE(goals,''), intended_behaviors, constraints,
		        success_metrics, COALESCE(unsafe_zones,''), confidence_threshold, COALESCE(sops,''), created_at
		 FROM agent_definitions
		 WHERE agent_id = $1
		 ORDER BY version DESC
		 LIMIT $2 OFFSET $3`,
		agentID, pg.Limit, pg.Offset(),
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch definitions")
		return
	}
	defer rows.Close()

	defs := make([]models.AgentDefinition, 0)
	for rows.Next() {
		d, err := scanDefinition(rows)
		if err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan definition")
			return
		}
		defs = append(defs, d)
	}

	utils.JSON(w, http.StatusOK, defs)
}

// Create godoc
// @Summary Save a new definition version
// @Tags definitions
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "Definition payload"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/definitions [post]
func (h *DefinitionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		Goals               string          `json:"goals"`
		IntendedBehaviors   json.RawMessage `json:"intended_behaviors"`
		Constraints         json.RawMessage `json:"constraints"`
		SuccessMetrics      json.RawMessage `json:"success_metrics"`
		UnsafeZones         string          `json:"unsafe_zones"`
		ConfidenceThreshold float64         `json:"confidence_threshold"`
		SOPs                string          `json:"sops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ConfidenceThreshold == 0 {
		req.ConfidenceThreshold = 75
	}

	// Auto-increment version
	var nextVersion int
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(MAX(version), 0) + 1 FROM agent_definitions WHERE agent_id = $1`,
		agentID,
	).Scan(&nextVersion)

	row := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_definitions
		   (agent_id, version, goals, intended_behaviors, constraints, success_metrics,
		    unsafe_zones, confidence_threshold, sops)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, agent_id, version, COALESCE(goals,''), intended_behaviors, constraints,
		           success_metrics, COALESCE(unsafe_zones,''), confidence_threshold, COALESCE(sops,''), created_at`,
		agentID, nextVersion, req.Goals,
		nullableJSON(req.IntendedBehaviors), nullableJSON(req.Constraints),
		nullableJSON(req.SuccessMetrics),
		req.UnsafeZones, req.ConfidenceThreshold, req.SOPs,
	)

	def, err := scanDefinitionRow(row)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create definition")
		return
	}

	utils.JSON(w, http.StatusCreated, def)
}

// GetByVersion godoc
// @Summary Get a specific definition version
// @Tags definitions
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param version path int true "Version number"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/definitions/{version} [get]
func (h *DefinitionsHandler) GetByVersion(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	versionStr := chi.URLParam(r, "version")
	userID := appMiddleware.GetUserID(r)

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid version number")
		return
	}

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	row := h.db.QueryRow(r.Context(),
		`SELECT id, agent_id, version, COALESCE(goals,''), intended_behaviors, constraints,
		        success_metrics, COALESCE(unsafe_zones,''), confidence_threshold, COALESCE(sops,''), created_at
		 FROM agent_definitions
		 WHERE agent_id = $1 AND version = $2`,
		agentID, version,
	)

	def, err := scanDefinitionRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.Err(w, http.StatusNotFound, "definition version not found")
			return
		}
		utils.Err(w, http.StatusInternalServerError, "failed to fetch definition")
		return
	}

	utils.JSON(w, http.StatusOK, def)
}

// scanDefinition scans a definition from pgx.Rows.
func scanDefinition(rows pgx.Rows) (models.AgentDefinition, error) {
	var d models.AgentDefinition
	var behaviors, constraints, metrics []byte
	err := rows.Scan(
		&d.ID, &d.AgentID, &d.Version, &d.Goals,
		&behaviors, &constraints, &metrics,
		&d.UnsafeZones, &d.ConfidenceThreshold, &d.SOPs, &d.CreatedAt,
	)
	if err != nil {
		return d, err
	}
	d.IntendedBehaviors = jsonOrNull(behaviors)
	d.Constraints = jsonOrNull(constraints)
	d.SuccessMetrics = jsonOrNull(metrics)
	return d, nil
}

// scanDefinitionRow scans a definition from pgx.Row.
func scanDefinitionRow(row pgx.Row) (models.AgentDefinition, error) {
	var d models.AgentDefinition
	var behaviors, constraints, metrics []byte
	err := row.Scan(
		&d.ID, &d.AgentID, &d.Version, &d.Goals,
		&behaviors, &constraints, &metrics,
		&d.UnsafeZones, &d.ConfidenceThreshold, &d.SOPs, &d.CreatedAt,
	)
	if err != nil {
		return d, err
	}
	d.IntendedBehaviors = jsonOrNull(behaviors)
	d.Constraints = jsonOrNull(constraints)
	d.SuccessMetrics = jsonOrNull(metrics)
	return d, nil
}

// jsonOrNull returns json.RawMessage("null") for nil byte slices.
func jsonOrNull(b []byte) json.RawMessage {
	if b == nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

// nullableJSON returns nil when the json.RawMessage is empty or "null".
func nullableJSON(j json.RawMessage) interface{} {
	if len(j) == 0 || string(j) == "null" {
		return nil
	}
	return j
}
