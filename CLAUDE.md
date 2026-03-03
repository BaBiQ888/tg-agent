# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o bot          # compile
./bot                    # run (requires .env and config.json)
go build ./...           # check compilation across all packages
```

No test suite or linter is configured. Docker build: `docker build -t tg-agent .` (multi-stage, golang:1.21.6-alpine → alpine:3.19).

## Architecture

**Go Telegram Bot Agent platform** — manages multiple Telegram bots, each with configurable agents, roles, knowledge bases, and RAG-powered chat via an external Python AI service.

### Startup Flow (main.go)

1. `config.GetConfig()` — singleton via `sync.Once`, loads `config.json` with `${ENV_VAR}` placeholder substitution
2. `models.InitDB(cfg)` — initializes `pgxpool` singleton (`models.DB()` accessor)
3. Queries `bots` table filtered by `RAILWAY_ENVIRONMENT_NAME` → creates one `TelegramHandler` per bot
4. Registers bots in `WebhookHandler` (thread-safe map), sets webhook or starts polling
5. Starts `fasthttp.Server` with manual path-based routing (switch/case, no router library)

### Package Dependencies

```
main → config, models, handlers
handlers → config, models, services, interfaces
services → config, models, interfaces
models → config (DB singleton)
interfaces → (no deps — single interface: StartCmdHandler)
```

### Key Layers

- **handlers/telegram.go** — Telegram message/command processing, in-memory user state (`map[int64]*UserState` keyed by chat ID — ephemeral, lost on restart), KB workflow state machine
- **handlers/api_handler.go** — REST API endpoints under `/api/` (fasthttp), API key auth via header
- **services/ai_client.go** — HTTP client for Python AI service with exponential backoff retry (3 attempts), circuit breaker (5 failures → 30s cooldown), `X-API-Key` auth
- **services/kb_service.go** — Knowledge base operations (text/file/link ingestion, dataset CRUD) via AIClient → Python service
- **services/action_service.go** — Chat dispatch: `LocalFunction` branch for built-in commands, otherwise routes to Python AI service `/api/v1/chat/completions`
- **models/** — Data structs + DB queries. Package-level `*pgxpool.Pool` singleton. Uses `pgx.QueryExecModeSimpleProtocol` (Supabase pooler compatibility)

### External Python AI Service

Deployed separately. Go communicates via REST JSON + `X-API-Key` header. Endpoints:

- `POST /api/v1/chat/completions` — RAG chat (embed query → Milvus search → context build → LLM)
- `POST /api/v1/collections/{text|link|file}` — document ingestion
- `POST /api/v1/datasets` / `DELETE /api/v1/datasets/{id}` — dataset management
- `GET /health`, `GET /readiness` — health checks

### Config Pattern

`config.json` contains structure with `${ENV_VAR}` placeholders. At load time, `config.GetConfig()` replaces them from `os.Getenv()`. Key env vars: `DB_PASSWORD` (URL-escaped), `AI_SERVICE_URL`, `AI_SERVICE_API_KEY`, `API_KEY`, `PROXY_URL`.

### Database Patterns

- All deletes are **soft-deletes**: `SET deleted_at = CURRENT_TIMESTAMP`
- Transactions: `tx.Begin()` → `defer tx.Rollback()` → ops → `tx.Commit()` (Rollback after Commit is no-op)
- Complex atomic inserts use CTEs (e.g., `CreateAgent` inserts into `agents` + `agent_chats` + `agent_actions` in one statement)
- `pgx.ErrNoRows` checked and mapped to user-facing error messages in handlers
- Schema scripts in `sql/`, migration scripts in `migrations/`

### Two Model Files for Roles

`models/agent.go` has a minimal `AgentRole` struct (id, name, description) used by `GetRoleByID()`. `models/role.go` has a full `Role` struct with all fields (personality, skills, constraints, etc.) used by `GetAllRoles()`. Both query `agent_roles` table — use the appropriate one based on which fields you need.
