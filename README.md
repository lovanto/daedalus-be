# Daedalus API

Go 1.25 · Chi · pgx/v5 · JWT — REST API for the Agent Development Lifecycle (ADLC) platform.

## Overview

The API is the single entry point for the frontend. It handles authentication, all ADLC phase data, and proxies AI assist requests to the Python AI layer. The Python service is never called directly by the frontend.

All responses follow a consistent envelope:

```json
{ "data": ..., "error": null }
{ "data": null, "error": "message" }
```

## Tech stack

| Dependency | Version |
|---|---|
| Go | 1.25 |
| chi | v5.1.0 |
| pgx | v5.6.0 |
| golang-jwt | v5.2.1 |
| swaggo/swag | v1.16.6 |
| godotenv | v1.5.1 |

## Project layout

```
backend/api/
├── main.go              # Entry point — router setup, graceful shutdown
├── config/
│   └── config.go        # Env-based config loading
├── db/
│   └── db.go            # pgx connection pool
├── handlers/            # HTTP handlers (one file per domain)
│   ├── auth.go          # register, login, /me
│   ├── agents.go        # agent CRUD + dashboard summary
│   ├── phases.go        # phase list & add
│   ├── definitions.go   # Define phase data
│   ├── builds.go        # Build phase data
│   ├── context.go       # Context snapshots
│   ├── evals.go         # Eval runs & cases
│   ├── observations.go  # Observe phase data
│   ├── tune.go          # Tune cycles
│   ├── ai_proxy.go      # Proxy to Python AI service
│   ├── export_import.go # Agent JSON export/import
│   └── health.go        # /health
├── middleware/
│   ├── auth.go          # JWT Bearer verification
│   ├── cors.go          # CORS policy
│   ├── logger.go        # Structured request logging
│   ├── rate_limit.go    # Per-IP rate limiting
│   └── request_id.go    # x-request-id propagation
├── models/              # Domain structs (no ORM)
├── services/
│   └── agent_service.go # Gate B logic, confidence score
├── utils/
│   ├── response.go      # JSON response helpers
│   ├── pagination.go    # Cursor/offset pagination
│   └── ownership.go     # Agent ownership checks
└── docs/                # Generated Swagger spec (do not edit manually)
```

## Configuration

Copy `.env.example` from the repo root, or set these environment variables:

```env
DATABASE_URL=postgres://daedalus:daedalus@localhost:5432/daedalus
JWT_SECRET=change-me-in-production
GO_API_PORT=3010
PYTHON_AI_URL=http://localhost:8001
GO_ENV=development   # set to "production" for JSON logs
```

## Running locally

```bash
cd backend/api

# Download dependencies
go mod download

# Start the server
go run .
```

The server logs all registered routes on startup. Swagger UI is available at:

```
http://localhost:3010/swagger/index.html
```

## API routes

### Auth

```
POST   /api/auth/register
POST   /api/auth/login
GET    /api/auth/me              (JWT required)
```

### Agents

```
GET    /api/agents/              (JWT required)
POST   /api/agents/
POST   /api/agents/import
GET    /api/agents/{id}
PATCH  /api/agents/{id}
DELETE /api/agents/{id}
GET    /api/agents/{id}/export
```

### ADLC Phase data (all require JWT)

```
GET/POST  /api/agents/{id}/phases
GET/POST  /api/agents/{id}/definitions
GET/POST  /api/agents/{id}/builds
GET/POST  /api/agents/{id}/context
GET/POST  /api/agents/{id}/evals
GET/POST  /api/agents/{id}/eval-cases
GET/POST  /api/agents/{id}/observations
GET/POST  /api/agents/{id}/tune-cycles
```

### AI proxy (all require JWT)

```
POST  /api/agents/{id}/ai/assist/define
POST  /api/agents/{id}/ai/assist/system-prompt
POST  /api/agents/{id}/ai/suggest-eval-cases
POST  /api/agents/{id}/ai/classify-failure
POST  /api/agents/{id}/ai/run-eval-case
POST  /api/agents/{id}/ai/analyze-patterns
POST  /api/agents/{id}/ai/check-scope-drift
POST  /api/agents/{id}/ai/suggest-tune-fix
GET   /api/ai/health
```

### Dashboard

```
GET  /api/dashboard/summary      (JWT required)
```

## Authentication

JWT Bearer tokens — 7-day expiry. Include in every authenticated request:

```
Authorization: Bearer <token>
```

## Key business rules

- **Confidence score** — rolling average of the last 3 eval scores.
- **Gate B** — three consecutive evals all >= `confidence_threshold` unlocks deploy.
- DB migrations are immutable — never edit applied files; add new ones instead.

## Regenerating Swagger docs

```bash
go install github.com/swaggo/swag/cmd/swag@latest
swag init
```

Re-run after any handler change that modifies `@Summary`, `@Router`, or `@Param` annotations.

## Docker

```bash
docker build -t daedalus-api .
docker run -p 8000:8000 --env-file .env daedalus-api
```

Or via the root `docker-compose.yml`:

```bash
docker-compose up api
```
