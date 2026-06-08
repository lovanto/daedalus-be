// Package main is the entry point for the Daedalus REST API.
//
// @title           Daedalus API
// @version         1.0
// @description     Agent Development Lifecycle (ADLC) management API.
// @termsOfService  http://swagger.io/terms/
//
// @contact.name   Daedalus
// @contact.email  rifkylovanto@gmail.com
//
// @host      localhost:3010
// @BasePath  /
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and the JWT token.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/daedalus/daedalus-be/docs"

	"github.com/daedalus/daedalus-be/config"
	"github.com/daedalus/daedalus-be/db"
	"github.com/daedalus/daedalus-be/handlers"
	appMiddleware "github.com/daedalus/daedalus-be/middleware"
	"github.com/daedalus/daedalus-be/services"
)

func main() {
	cfg := config.Load()

	pool, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("[startup] database connection failed: %v", err)
	}
	defer pool.Close()
	log.Println("[startup] database connected")

	// Configure slog: JSON in production, text in dev
	if cfg.IsProduction() {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	}

	// Ping Python AI service — non-fatal, just warn
	pingAIService(cfg.PythonAIURL)

	r := chi.NewRouter()

	r.Use(appMiddleware.RequestID)
	r.Use(appMiddleware.RateLimit)
	if cfg.IsProduction() {
		r.Use(appMiddleware.StructuredLogger)
	} else {
		r.Use(chiMiddleware.Logger)
	}
	r.Use(chiMiddleware.Recoverer)
	r.Use(appMiddleware.CORS)

	r.Get("/swagger/*", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))

	healthHandler := handlers.NewHealthHandler(pool)
	r.Get("/health", healthHandler.Health)

	agentService := services.NewAgentService(pool)

	authHandler         := handlers.NewAuthHandler(pool, cfg.JWTSecret)
	agentHandler        := handlers.NewAgentHandler(pool, agentService)
	phaseHandler        := handlers.NewPhaseHandler(pool)
	definitionsHandler  := handlers.NewDefinitionsHandler(pool)
	buildsHandler       := handlers.NewBuildsHandler(pool)
	contextHandler      := handlers.NewContextHandler(pool)
	evalsHandler        := handlers.NewEvalsHandler(pool, agentService)
	observationsHandler := handlers.NewObservationsHandler(pool)
	tuneHandler         := handlers.NewTuneHandler(pool)
	aiProxy             := handlers.NewAIProxyHandler(pool, cfg.PythonAIURL)
	exportHandler       := handlers.NewExportHandler(pool)

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
		r.Group(func(r chi.Router) {
			r.Use(appMiddleware.Auth(cfg.JWTSecret))
			r.Get("/me", authHandler.Me)
		})
	})

	r.Route("/api/agents", func(r chi.Router) {
		r.Use(appMiddleware.Auth(cfg.JWTSecret))
		r.Get("/", agentHandler.List)
		r.Get("/deleted", agentHandler.ListDeleted)
		r.Post("/", agentHandler.Create)
		r.Post("/import", exportHandler.Import)

		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", agentHandler.Get)
			r.Patch("/", agentHandler.Update)
			r.Delete("/", agentHandler.Delete)
			r.Post("/restore", agentHandler.Restore)
			r.Delete("/permanent", agentHandler.PermanentDelete)
			r.Post("/evolve", agentHandler.Evolve)

			r.Get("/export", exportHandler.Export)

			r.Get("/phases", phaseHandler.List)
			r.Post("/phases", phaseHandler.Add)

			r.Get("/definitions", definitionsHandler.List)
			r.Post("/definitions", definitionsHandler.Create)
			r.Get("/definitions/{version}", definitionsHandler.GetByVersion)

			r.Get("/builds", buildsHandler.List)
			r.Post("/builds", buildsHandler.Create)
			r.Get("/builds/latest", buildsHandler.GetLatest)

			r.Get("/context", contextHandler.List)
			r.Post("/context", contextHandler.Create)
			r.Get("/context/latest", contextHandler.GetLatest)
			r.Patch("/context/{snapshot_id}/tools/{tool_name}", contextHandler.MarkToolVerified)

			r.Get("/evals", evalsHandler.List)
			r.Post("/evals", evalsHandler.Create)
			r.Get("/evals/latest", evalsHandler.GetLatest)
			r.Get("/eval-cases", evalsHandler.ListCases)
			r.Post("/eval-cases", evalsHandler.CreateCase)
			r.Patch("/eval-cases/{case_id}", evalsHandler.UpdateCase)
			r.Get("/eval-case-runs", evalsHandler.ListCaseRuns)
			r.Post("/eval-cases/{case_id}/runs", evalsHandler.CreateCaseRun)

			r.Get("/observations", observationsHandler.List)
			r.Post("/observations", observationsHandler.Create)
			r.Get("/observations/clusters", observationsHandler.Clusters)

			r.Get("/tune-cycles", tuneHandler.List)
			r.Post("/tune-cycles", tuneHandler.Create)
			r.Patch("/tune-cycles/{cycle_id}", tuneHandler.UpdateOutcome)
			r.Post("/tune-cycles/{cycle_id}/apply", tuneHandler.Apply)

			r.Post("/ai/assist/define", aiProxy.AssistDefine)
			r.Post("/ai/assist/system-prompt", aiProxy.AssistSystemPrompt)
			r.Post("/ai/suggest-eval-cases", aiProxy.SuggestEvalCases)
			r.Post("/ai/classify-failure", aiProxy.ClassifyFailure)
			r.Post("/ai/run-eval-case", aiProxy.RunEvalCase)
			r.Post("/ai/analyze-patterns", aiProxy.AnalyzePatterns)
			r.Post("/ai/check-scope-drift", aiProxy.CheckScopeDrift)
			r.Post("/ai/suggest-tune-fix", aiProxy.SuggestTuneFix)
			r.Post("/ai/rewrite-tune-prompt", aiProxy.RewriteTunePrompt)
			r.Post("/ai/tune-apply-plan", aiProxy.TuneApplyPlan)
			r.Post("/ai/chat", aiProxy.Chat)
		})
	})

	r.Route("/api/ai", func(r chi.Router) {
		r.Use(appMiddleware.Auth(cfg.JWTSecret))
		r.Get("/health", aiProxy.AIHealth)
	})

	r.Route("/api/dashboard", func(r chi.Router) {
		r.Use(appMiddleware.Auth(cfg.JWTSecret))
		r.Get("/summary", agentHandler.Summary)
	})

	logRoutes(r)

	// Graceful shutdown
	addr := fmt.Sprintf(":%s", cfg.GoAPIPort)
	server := &http.Server{Addr: addr, Handler: r}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("[startup] daedalus-api listening on %s", addr)
		log.Printf("[startup] swagger UI: http://localhost%s/swagger/index.html", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[server] error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[shutdown] signal received, shutting down gracefully…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[shutdown] forced: %v", err)
	}
	log.Println("[shutdown] done")
}

func pingAIService(aiURL string) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(aiURL + "/ai/health")
	if err != nil {
		log.Printf("[startup] Python AI service not reachable at %s — AI endpoints will return 503 until it starts", aiURL)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		log.Printf("[startup] Python AI service connected at %s", aiURL)
	} else {
		log.Printf("[startup] Python AI service returned HTTP %d", resp.StatusCode)
	}
}

func logRoutes(r *chi.Mux) {
	log.Println("[startup] registered routes:")
	chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		log.Printf("  %-7s %s", method, route)
		return nil
	})
}
