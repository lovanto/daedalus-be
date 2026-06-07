package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/utils"
)

// AIProxyHandler forwards requests to the Python AI service, injecting
// the agent's latest definition and metadata into every request body.
type AIProxyHandler struct {
	db         *pgxpool.Pool
	aiBaseURL  string
	httpClient *http.Client
}

func NewAIProxyHandler(db *pgxpool.Pool, aiBaseURL string) *AIProxyHandler {
	return &AIProxyHandler{
		db:         db,
		aiBaseURL:  aiBaseURL,
		httpClient: &http.Client{},
	}
}

// ---------------------------------------------------------------------------
// Annotated route methods — each maps to one Python AI endpoint
// ---------------------------------------------------------------------------

// AssistDefine godoc
// @Summary AI suggestions for a Define section
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "section, agent_name, existing_content"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/assist/define [post]
func (h *AIProxyHandler) AssistDefine(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/assist/define")(w, r)
}

// AssistSystemPrompt godoc
// @Summary Generate a system prompt from the agent definition
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} false "Optional overrides"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/assist/system-prompt [post]
func (h *AIProxyHandler) AssistSystemPrompt(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/assist/build/system-prompt")(w, r)
}

// SuggestEvalCases godoc
// @Summary AI-suggested eval test cases
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} false "count (default 10)"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/suggest-eval-cases [post]
func (h *AIProxyHandler) SuggestEvalCases(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/eval/suggest-cases")(w, r)
}

// ClassifyFailure godoc
// @Summary Classify an eval failure as behavioral, structural, or scope
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "failure_description"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/classify-failure [post]
func (h *AIProxyHandler) ClassifyFailure(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/eval/classify-failure")(w, r)
}

// RunEvalCase godoc
// @Summary Run a test case against Ollama and score it
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "test_case, system_prompt"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/run-eval-case [post]
func (h *AIProxyHandler) RunEvalCase(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/eval/run-case")(w, r)
}

// AnalyzePatterns godoc
// @Summary Analyze observation patterns and recommend a routing action
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} false "observations list (auto-injected if omitted)"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/analyze-patterns [post]
func (h *AIProxyHandler) AnalyzePatterns(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/analyze/patterns")(w, r)
}

// CheckScopeDrift godoc
// @Summary Detect scope drift against the agent definition
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} false "observations list (auto-injected if omitted)"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/check-scope-drift [post]
func (h *AIProxyHandler) CheckScopeDrift(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/analyze/scope-drift")(w, r)
}

// SuggestTuneFix godoc
// @Summary Suggest specific changes to fix a tune-cycle failure
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "failure_type, failure_description"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/suggest-tune-fix [post]
func (h *AIProxyHandler) SuggestTuneFix(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/assist/tune/suggest-fix")(w, r)
}

// RewriteTunePrompt godoc
// @Summary Rewrite a system prompt to apply a tune cycle's changes
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "current_prompt, failure_type, changes"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/rewrite-tune-prompt [post]
func (h *AIProxyHandler) RewriteTunePrompt(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/assist/tune/rewrite-prompt")(w, r)
}

// TuneApplyPlan godoc
// @Summary Build a multi-phase apply plan (Build config + Define) from a tune cycle
// @Tags ai
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Agent ID"
// @Param body body map[string]interface{} true "current_prompt, failure_type, changes, current_build"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/agents/{id}/ai/tune-apply-plan [post]
func (h *AIProxyHandler) TuneApplyPlan(w http.ResponseWriter, r *http.Request) {
	h.proxy("/ai/assist/tune/apply-plan")(w, r)
}

// AIHealth godoc
// @Summary Proxy the Python AI service health check
// @Description Returns AI service status, Ollama reachability, and available models.
// @Tags system
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/ai/health [get]
func (h *AIProxyHandler) AIHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.aiBaseURL+"/ai/health", nil)
	if err != nil {
		utils.JSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "unavailable", "ollama_reachable": false, "available_models": []string{},
		})
		return
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		utils.JSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "unavailable", "ollama_reachable": false, "available_models": []string{},
		})
		return
	}
	defer resp.Body.Close()

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		utils.Err(w, http.StatusInternalServerError, "failed to decode AI health response")
		return
	}
	utils.JSON(w, resp.StatusCode, data)
}

// ---------------------------------------------------------------------------
// Core proxy engine
// ---------------------------------------------------------------------------

// proxy returns an http.HandlerFunc that enriches the request body with
// agent context and forwards it to the given Python AI path.
func (h *AIProxyHandler) proxy(targetPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := chi.URLParam(r, "id")
		userID := appMiddleware.GetUserID(r)

		if !utils.AgentBelongsToUser(r.Context(), h.db, agentID, userID) {
			utils.Err(w, http.StatusNotFound, "agent not found")
			return
		}

		// Read client body (may be empty)
		rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB cap
		if err != nil {
			utils.Err(w, http.StatusBadRequest, "failed to read request body")
			return
		}

		// Parse as JSON map; start with empty map if body is empty/invalid
		payload := make(map[string]interface{})
		if len(rawBody) > 0 {
			json.Unmarshal(rawBody, &payload) //nolint:errcheck — fallback to empty map on error
		}

		// Inject agent context
		h.injectContext(r.Context(), agentID, payload)

		enriched, err := json.Marshal(payload)
		if err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to marshal enriched payload")
			return
		}

		// Build and fire the proxy request
		targetURL := h.aiBaseURL + targetPath
		proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(enriched))
		if err != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to build proxy request")
			return
		}
		proxyReq.Header.Set("Content-Type", "application/json")

		resp, err := h.httpClient.Do(proxyReq)
		if err != nil {
			log.Printf("[ai-proxy] agent=%s path=%s error=%v", agentID, targetPath, err)
			utils.Err(w, http.StatusServiceUnavailable, "AI service unavailable")
			return
		}
		defer resp.Body.Close()

		// Map Python 503 (Ollama down) → 502 Bad Gateway for the caller
		statusCode := resp.StatusCode
		if statusCode == http.StatusServiceUnavailable {
			statusCode = http.StatusBadGateway
		}
		if statusCode != http.StatusOK && statusCode != http.StatusCreated {
			log.Printf("[ai-proxy] agent=%s path=%s upstream_status=%d", agentID, targetPath, resp.StatusCode)
		}

		var result map[string]interface{}
		if decErr := json.NewDecoder(resp.Body).Decode(&result); decErr != nil {
			utils.Err(w, http.StatusInternalServerError, "failed to decode AI response")
			return
		}

		if statusCode != http.StatusOK && statusCode != http.StatusCreated {
			errMsg := "AI service error"
			if detail, ok := result["detail"].(string); ok {
				errMsg = detail
			} else if msg, ok := result["error"].(string); ok {
				errMsg = msg
			}
			utils.Err(w, statusCode, errMsg)
			return
		}

		utils.JSON(w, statusCode, result)
	}
}

// ---------------------------------------------------------------------------
// Context injection helpers
// ---------------------------------------------------------------------------

type agentMeta struct {
	Name        string
	Description string
}

// injectContext enriches payload with agent_definition, agent_name, and
// agent_description fetched from the database, skipping fields already set.
func (h *AIProxyHandler) injectContext(ctx context.Context, agentID string, payload map[string]interface{}) {
	meta, err := h.fetchAgentMeta(ctx, agentID)
	if err == nil {
		if _, ok := payload["agent_name"]; !ok {
			payload["agent_name"] = meta.Name
		}
		if _, ok := payload["agent_description"]; !ok {
			payload["agent_description"] = meta.Description
		}
	}

	if _, ok := payload["agent_definition"]; !ok {
		def, err := h.fetchLatestDefinition(ctx, agentID)
		if err == nil && def != nil {
			payload["agent_definition"] = def
		}
	}
}

func (h *AIProxyHandler) fetchAgentMeta(ctx context.Context, agentID string) (agentMeta, error) {
	var m agentMeta
	err := h.db.QueryRow(ctx,
		`SELECT name, COALESCE(description,'') FROM agents WHERE id = $1 AND deleted_at IS NULL`,
		agentID,
	).Scan(&m.Name, &m.Description)
	return m, err
}

func (h *AIProxyHandler) fetchLatestDefinition(ctx context.Context, agentID string) (map[string]interface{}, error) {
	var goals, unsafeZones, sops string
	var threshold float64
	var behaviorsRaw, constraintsRaw, metricsRaw []byte

	err := h.db.QueryRow(ctx,
		`SELECT COALESCE(goals,''), intended_behaviors, constraints, success_metrics,
		        COALESCE(unsafe_zones,''), confidence_threshold, COALESCE(sops,'')
		 FROM agent_definitions
		 WHERE agent_id = $1
		 ORDER BY version DESC LIMIT 1`,
		agentID,
	).Scan(&goals, &behaviorsRaw, &constraintsRaw, &metricsRaw, &unsafeZones, &threshold, &sops)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // no definition yet — not an error
		}
		return nil, err
	}

	def := map[string]interface{}{
		"goals":                goals,
		"unsafe_zones":         unsafeZones,
		"confidence_threshold": threshold,
		"sops":                 sops,
	}

	for key, raw := range map[string][]byte{
		"intended_behaviors": behaviorsRaw,
		"constraints":        constraintsRaw,
		"success_metrics":    metricsRaw,
	} {
		if len(raw) > 0 {
			var v interface{}
			if json.Unmarshal(raw, &v) == nil {
				def[key] = v
			}
		}
	}

	return def, nil
}
