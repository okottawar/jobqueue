# Job Queue — Reliable Background Job Processing Service

A background job processing service written in Go: submit asynchronous jobs
over a REST API, have them processed concurrently by a configurable worker
pool, with automatic retries (exponential backoff), persistent job tracking
in PostgreSQL, graceful shutdown, and a live web dashboard.

Deployable for **$0/month** on Render (free web service) + Supabase (free
PostgreSQL).

## Features

- REST API: `POST/GET /api/jobs`, `GET/DELETE /api/jobs/:id`, `GET /api/stats`
- Concurrent worker pool (goroutines + channels), configurable size
- Automatic retries with exponential backoff + jitter, configurable max attempts
- Atomic job claiming (`FOR UPDATE SKIP LOCKED`) — safe with multiple workers,
  no duplicate processing
- Graceful shutdown: stops accepting new jobs, waits for in-flight jobs (bounded
  by `SHUTDOWN_GRACE`), then exits
- Structured JSON logs, `/api/stats` for operational visibility
- Web dashboard (HTML/CSS/vanilla JS) with live stats, job submission, and
  filtering
- Dockerized for local dev (`docker compose up`) and production (Render)

## Quick start (local)

```bash
docker compose up --build
```

Then open http://localhost:8080 for the dashboard, or:

```bash
curl -X POST localhost:8080/api/jobs \
  -H 'Content-Type: application/json' \
  -d '{"type":"email","payload":{"to":"user@example.com","subject":"Welcome"}}'

curl localhost:8080/api/stats
```

## Running without Docker

Requires Go 1.22+ and a PostgreSQL instance.

```bash
cp .env.example .env      # edit DATABASE_URL etc.
export $(cat .env | xargs)
go run ./cmd/server
```

> **First-time setup:** run `go mod tidy` once after cloning to generate
> `go.sum` (omitted from this deliverable since it was produced in a
> network-restricted sandbox — see note below).

## Job types

Three demo handlers are registered out of the box: `email`, `webhook`, and
`report` (see `internal/job/handler.go`). They simulate realistic I/O latency
and occasional transient failures so you can watch retries/backoff in action.
Add your own by calling `registry.Register("mytype", myHandlerFunc)` in
`cmd/server/main.go` before starting the pool.

## Configuration (environment variables)

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | *(required)* | PostgreSQL connection string |
| `PORT` | `8080` | HTTP listen port |
| `WORKER_COUNT` | `5` | Number of concurrent workers |
| `MAX_RETRIES` | `3` | Default max attempts (used as job default) |
| `QUEUE_SIZE` | `100` | In-memory channel buffer size |
| `POLL_INTERVAL` | `2s` | How often workers poll the DB for due jobs |
| `BASE_BACKOFF` | `2s` | Base for exponential backoff |
| `MAX_BACKOFF` | `60s` | Backoff cap |
| `SHUTDOWN_GRACE` | `20s` | Max time to wait for in-flight jobs on shutdown |

## API reference

```
POST   /api/jobs          Create a job: {"type": "...", "payload": {...}, "max_attempts": 3}
GET    /api/jobs           List jobs (?status=pending|processing|retry|completed|failed&limit=&offset=)
GET    /api/jobs/:id       Get a single job
DELETE /api/jobs/:id       Delete a job
GET    /api/stats          Aggregate counts + active worker info
GET    /healthz            Health check (used by Render)
```

## Deployment ($0/month)

1. **Supabase**: create a free project, copy the PostgreSQL connection string
   (use the pooled "connection string" with `sslmode=require` if needed).
2. **Render**: create a new Web Service from this repo (Docker environment —
   `render.yaml` is included as a blueprint). Set `DATABASE_URL` to the
   Supabase connection string. Render builds from the `Dockerfile` and
   auto-deploys on every push to your GitHub repo.
3. The service runs migrations automatically on startup.

## Testing

```bash
go test ./...                                    # unit tests (no DB needed)
TEST_DATABASE_URL=postgres://... go test ./internal/db/...   # DB integration tests
```

Test coverage includes: job creation/state transitions, retry/backoff logic,
max-retry exhaustion, concurrent-worker safety (no duplicate processing),
API request validation, and atomic claim behavior against a real Postgres
instance.

## A note on this deliverable's `go.sum`

This project was built and fully verified (compiled, `go vet`, unit tests,
DB integration tests against a real local PostgreSQL, and a live end-to-end
smoke test of the running server) inside a sandboxed environment whose
network egress is restricted to a small allowlist that does not include
`proxy.golang.org`. To resolve dependencies there, temporary `replace`
directives pointing at GitHub mirrors were needed. Those were removed for
this deliverable so `go.mod` is clean and standard — on a normal machine or
CI runner with regular internet access, `go mod tidy` will resolve everything
directly from the official Go module proxy on the first build.

## Project layout

```
cmd/server/          main.go — wiring, graceful shutdown
internal/config/     environment-based configuration
internal/db/         PostgreSQL pool, migrations, Store (repository)
internal/job/        job model, handler registry, worker pool
internal/api/        HTTP handlers, middleware
web/static/          dashboard (HTML/CSS/JS)
migrations/          SQL schema
Dockerfile, docker-compose.yml, render.yaml
```

## Potential extensions

Redis-backed distributed queue, dead-letter queue, job priorities,
delayed/scheduled jobs, SSE for live dashboard updates, Prometheus metrics,
rate limiting, auth, horizontally-scaled worker instances.
