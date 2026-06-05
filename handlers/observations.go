package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/models"
	"github.com/daedalus/daedalus-be/utils"
)

type ObservationsHandler struct {
	db *pgxpool.Pool
}

func NewObservationsHandler(db *pgxpool.Pool) *ObservationsHandler {
	return &ObservationsHandler{db: db}
}

// List godoc
// @Summary List observations for an agent (filterable by severity, source, routed_to)
// @Tags observations
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Param severity query string false "Filter by severity (low|medium|high|critical)"
// @Param source query string false "Filter by source (live|simulated|user_feedback|manual)"
// @Param routed_to query string false "Filter by route (none|tune|build|define|gate_b)"
// @Param page query int false "Page" default(1)
// @Param limit query int false "Limit" default(20)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/observations [get]
func (h *ObservationsHandler) List(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)
	pg := utils.ParsePagination(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	severity := r.URL.Query().Get("severity")
	source := r.URL.Query().Get("source")
	routedTo := r.URL.Query().Get("routed_to")

	rows, err := h.db.Query(r.Context(),
		`SELECT id, agent_id, pattern, severity, source, routed_to, tags, created_at
		 FROM agent_observations
		 WHERE agent_id = $1
		   AND ($2 = '' OR severity::text = $2)
		   AND ($3 = '' OR source::text = $3)
		   AND ($4 = '' OR routed_to::text = $4)
		 ORDER BY created_at DESC
		 LIMIT $5 OFFSET $6`,
		agentID, severity, source, routedTo, pg.Limit, pg.Offset(),
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch observations")
		return
	}
	defer rows.Close()

	observations := make([]models.AgentObservation, 0)
	for rows.Next() {
		var o models.AgentObservation
		var tagsBytes []byte
		if err := rows.Scan(&o.ID, &o.AgentID, &o.Pattern, &o.Severity,
			&o.Source, &o.RoutedTo, &tagsBytes, &o.CreatedAt); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan observation")
			return
		}
		o.Tags = jsonOrNull(tagsBytes)
		observations = append(observations, o)
	}

	utils.JSON(w, http.StatusOK, observations)
}

// Create godoc
// @Summary Log a new observation
// @Tags observations
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "Observation payload"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/observations [post]
func (h *ObservationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		Pattern  string          `json:"pattern"`
		Severity string          `json:"severity"`
		Source   string          `json:"source"`
		RoutedTo string          `json:"routed_to"`
		Tags     json.RawMessage `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Pattern == "" {
		utils.Err(w, http.StatusBadRequest, "pattern is required")
		return
	}
	if req.Severity == "" {
		req.Severity = "low"
	}
	if req.Source == "" {
		req.Source = "manual"
	}
	if req.RoutedTo == "" {
		req.RoutedTo = "none"
	}

	var o models.AgentObservation
	var tagsBytes []byte
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO agent_observations (agent_id, pattern, severity, source, routed_to, tags)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, agent_id, pattern, severity, source, routed_to, tags, created_at`,
		agentID, req.Pattern, req.Severity, req.Source, req.RoutedTo, nullableJSON(req.Tags),
	).Scan(&o.ID, &o.AgentID, &o.Pattern, &o.Severity, &o.Source, &o.RoutedTo, &tagsBytes, &o.CreatedAt)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to create observation")
		return
	}
	o.Tags = jsonOrNull(tagsBytes)

	utils.JSON(w, http.StatusCreated, o)
}

// Clusters godoc
// @Summary Group observations by tag
// @Tags observations
// @Security BearerAuth
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /api/agents/{id}/observations/clusters [get]
func (h *ObservationsHandler) Clusters(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	userID := appMiddleware.GetUserID(r)

	if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
		utils.Err(w, http.StatusNotFound, "agent not found")
		return
	}

	// Observations with no tags land in an "untagged" bucket; others are fanned out per tag.
	rows, err := h.db.Query(r.Context(),
		`SELECT
		   COALESCE(t.tag, 'untagged')     AS tag,
		   o.id, o.agent_id, o.pattern, o.severity, o.source, o.routed_to, o.tags, o.created_at
		 FROM agent_observations o
		 LEFT JOIN LATERAL jsonb_array_elements_text(
		   CASE WHEN jsonb_typeof(o.tags) = 'array' THEN o.tags ELSE '[]'::jsonb END
		 ) AS t(tag) ON true
		 WHERE o.agent_id = $1
		 ORDER BY tag, o.created_at DESC`,
		agentID,
	)
	if err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to fetch clusters")
		return
	}
	defer rows.Close()

	clusterMap := make(map[string]*models.ObservationCluster)
	var order []string

	for rows.Next() {
		var tag string
		var o models.AgentObservation
		var tagsBytes []byte
		if err := rows.Scan(&tag, &o.ID, &o.AgentID, &o.Pattern, &o.Severity,
			&o.Source, &o.RoutedTo, &tagsBytes, &o.CreatedAt); err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to scan cluster row")
			return
		}
		o.Tags = jsonOrNull(tagsBytes)

		if _, ok := clusterMap[tag]; !ok {
			clusterMap[tag] = &models.ObservationCluster{Tag: tag, Observations: []models.AgentObservation{}}
			order = append(order, tag)
		}
		clusterMap[tag].Observations = append(clusterMap[tag].Observations, o)
	}

	clusters := make([]models.ObservationCluster, 0, len(order))
	for _, tag := range order {
		clusters = append(clusters, *clusterMap[tag])
	}

	utils.JSON(w, http.StatusOK, clusters)
}
