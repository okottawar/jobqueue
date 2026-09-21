# Job Queue

A small job queue built with Go and PostgreSQL.

Jobs are stored in PostgreSQL, picked up by a configurable worker pool, and retried when processing fails. There is also a simple web dashboard for submitting jobs and watching their status.

**Live demo:** https://jobqueue-h4ez.onrender.com/

## What it does

- REST API for creating, listing, reading, and deleting jobs
- Batch endpoint for creating multiple jobs in one request
- Configurable worker pool built with goroutines and channels
- PostgreSQL-backed job storage
- Automatic retries with exponential backoff and jitter
- Safe concurrent job claiming with PostgreSQL row locking
- Graceful shutdown and recovery of jobs left in `processing`
- Simple dashboard with live stats, filters, job details, and deletion
- Docker support for local development and deployment

## How it works

The basic flow is:

```text
Client
  |
  v
Go API
  |
  v
PostgreSQL
  |
  v
Worker Pool
  |
  v
Job Handler
```

The API only puts work into the queue. The worker pool is responsible for processing it.

Jobs move through states such as:

```text
pending -> processing -> completed
                 |
                 +-> retry -> processing
                 |
                 +-> failed
```

Retries use exponential backoff with jitter. Each job keeps its attempt count and maximum attempt count in PostgreSQL.

## Job types

The project includes three demo handlers:

- `email`
- `webhook`
- `report`

They simulate asynchronous work and occasional transient failures, so the retry behavior can be seen without depending on real external services.

The handlers are in `internal/job/handler.go`.

## API

### Create one job

```http
POST /api/jobs
Content-Type: application/json
```

Example:

```json
{
  "type": "email",
  "payload": {
    "to": "user@example.com",
    "subject": "Welcome"
  },
  "max_attempts": 3
}
```

### Create multiple jobs

```http
POST /api/jobs/batch
Content-Type: application/json
```

Example:

```json
{
  "jobs": [
    {
      "type": "email",
      "payload": {
        "to": "alice@example.com",
        "subject": "Weekly summary"
      },
      "max_attempts": 3
    },
    {
      "type": "report",
      "payload": {},
      "max_attempts": 3
    },
    {
      "type": "webhook",
      "payload": {
        "url": "https://example.com/webhook"
      },
      "max_attempts": 3
    }
  ]
}
```

The batch endpoint accepts up to 100 jobs and creates them in a single database transaction.

### Other endpoints

| Method | Endpoint | Purpose |
|---|---|---|
| POST | `/api/jobs` | Create a job |
| POST | `/api/jobs/batch` | Create multiple jobs |
| GET | `/api/jobs` | List jobs |
| GET | `/api/jobs/:id` | Get one job |
| DELETE | `/api/jobs/:id` | Delete a job |
| GET | `/api/stats` | Job and worker statistics |
| GET | `/healthz` | Health check |

For `GET /api/jobs`, the dashboard uses the `status`, `limit`, and `offset` query parameters.

## Dashboard

The dashboard is plain HTML, CSS, and JavaScript. It shows:

- total, pending, processing, completed, and failed jobs
- active workers
- job status and attempt counts
- automatic refresh
- job details
- single-job submission
- batch submission with different job types in the same batch

The goal of the UI is to make the queue behavior easy to see rather than to build a large frontend.

## Run locally

### Docker

The easiest option is:

```bash
docker compose up --build
```

Then open:

http://localhost:8080

### Without Docker

You need Go 1.25+ and PostgreSQL.

Set the database connection string:

```bash
export DATABASE_URL="postgres://..."
go run ./cmd/server
```

The server runs on port `8080` by default.

Environment variables include:

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | required | PostgreSQL connection string |
| `PORT` | `8080` | HTTP port |
| `WORKER_COUNT` | `5` | Number of workers |
| `QUEUE_SIZE` | `100` | In-memory worker queue size |
| `POLL_INTERVAL` | `2s` | Database polling interval |
| `BASE_BACKOFF` | `2s` | Initial retry backoff |
| `MAX_BACKOFF` | `60s` | Maximum retry backoff |
| `SHUTDOWN_GRACE` | `20s` | Shutdown grace period |

## Testing

Run the normal test suite with:

```bash
go test ./...
```

It is also useful to run the race detector and static checks:

```bash
go test -race ./...
go vet ./...
gofmt -l ./cmd ./internal
go build ./...
```

The tests cover API validation, job handling, retry behavior, and worker-pool behavior.

## Deployment

The project is deployed with:

- **Render** for the Go web service
- **Supabase** for PostgreSQL
- **Docker** for the application image

The repository includes `Dockerfile` and `render.yaml`.

The app runs database migrations when it starts.

## Project structure

```text
cmd/server/       application entry point
internal/api/     HTTP handlers and middleware
internal/config/  environment-based configuration
internal/db/      PostgreSQL access and migrations
internal/job/     job model, handlers, and worker pool
web/static/       dashboard
migrations/       database schema
```

## Why I built it

I wanted a project where the interesting part was the backend behavior rather than just CRUD.

The main things I wanted to work with were:

- concurrent workers in Go
- reliable job state transitions
- retries and backoff
- PostgreSQL as the queue's source of truth
- graceful shutdown
- a small dashboard to make the system observable

It is intentionally a relatively small implementation, so the important parts are easy to read and experiment with.
