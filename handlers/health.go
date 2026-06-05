package handlers

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/daedalus/daedalus-be/utils"
)

type HealthHandler struct {
	db *pgxpool.Pool
}

func NewHealthHandler(db *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{db: db}
}

// Health godoc
// @Summary Service and dependency health check
// @Tags system
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	dbOK := h.db.Ping(r.Context()) == nil

	status := "ok"
	code := http.StatusOK
	if !dbOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	utils.JSON(w, code, map[string]any{
		"status":    status,
		"service":   "daedalus-api",
		"timestamp": time.Now().UTC(),
		"checks": map[string]any{
			"database": map[string]bool{"ok": dbOK},
		},
	})
}
