# Taskflow

A verbose, production-shaped Go HTTP service that demonstrates **PostgreSQL,
MongoDB, and Redis in one request path**, plus the patterns a NestJS developer
expects to find in a Go project: DTOs, validation, dependency injection, layered
architecture, generated API docs, retries, and structured logs.

The service manages tasks in PostgreSQL, notes in MongoDB, and caches/counters in
Redis. Every task write also appends an audit event to MongoDB. One endpoint,
`GET /api/v1/tasks/{id}/overview`, intentionally reads all three stores.

> **Why no ORM?** Go has no ecosystem-standard ORM comparable to Prisma or
> TypeORM. The idiomatic choice is explicit SQL with `pgx` plus a migration tool
> (`goose`) — that is what this project uses. Queries stay visible, index usage
> is predictable, and there is no reflection-heavy mapping layer.

## What this project demonstrates

- **PostgreSQL** task CRUD using `pgx/v5` with explicit SQL.
- **MongoDB** note CRUD and append-only task audit events using the official
  Go driver.
- **Redis** read-through caching, atomic counters, and a visible retry wrapper.
- **DTOs** for requests/responses with `go-playground/validator` metadata.
- **Flexible date input** (`2026-12-31`, `31/12/2026`, RFC3339) with UTC storage
  and human/relative date fields computed at the edge.
- **Automated API docs** generated from handler annotations into Swagger UI.
- **Verbose logs** for every connection attempt, retry, query, cache hit/miss,
  write, disconnect, and failure.
- **Graceful startup/shutdown**: connect with retries, migrate, seed, serve, then
  close every dependency in reverse order with logged results.
- **Readiness checks** that return the concrete dependency error, not just
  `"not ready"`.

## Quick start

Prerequisites: Go 1.24+ and network access to the three databases.

```bash
# 1. Create the local environment file (already present in this workspace).
cp .env.example .env
# edit .env with real connection strings

# 2. Run the API. Startup connects to all stores, runs migrations, and seeds
#    demo data only when the tasks table is empty.
make run

# 3. In another terminal, exercise the full stack.
make smoke
```

The server listens on `http://localhost:8080` by default.

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

Swagger UI: <http://localhost:8080/swagger/index.html>

## API

| Method   | Path                          | Store(s)                       | Purpose                              |
| -------- | ----------------------------- | ------------------------------ | ------------------------------------ |
| `GET`    | `/healthz`                    | –                              | Liveness                             |
| `GET`    | `/readyz`                     | PG + Mongo + Redis             | Readiness with per-dependency errors |
| `GET`    | `/swagger/index.html`         | generated                      | Swagger UI                           |
| `GET`    | `/api/v1/tasks`               | PostgreSQL                     | List/filter tasks                    |
| `POST`   | `/api/v1/tasks`               | PG + Redis + Mongo             | Create task, cache it, audit it      |
| `GET`    | `/api/v1/tasks/{id}`          | Redis → PostgreSQL             | Cache-aside read                     |
| `PATCH`  | `/api/v1/tasks/{id}`          | PG + Redis + Mongo             | Partial update, refresh cache, audit |
| `DELETE` | `/api/v1/tasks/{id}`          | PG + Redis + Mongo             | Delete, invalidate cache, audit      |
| `GET`    | `/api/v1/tasks/{id}/overview` | PG (via Redis) + Mongo + Redis | Cross-database aggregate             |
| `GET`    | `/api/v1/notes`               | MongoDB                        | List/filter notes                    |
| `POST`   | `/api/v1/notes`               | MongoDB                        | Create note                          |
| `GET`    | `/api/v1/notes/{id}`          | MongoDB                        | Get note                             |
| `PATCH`  | `/api/v1/notes/{id}`          | MongoDB                        | Partial update                       |
| `DELETE` | `/api/v1/notes/{id}`          | MongoDB                        | Delete note                          |

Every success response wraps its payload in `{"data": ...}`. Errors look like:

```json
{
  "error": {
    "code": "validation_error",
    "message": "title: is required"
  }
}
```

### Create a task with a flexible due date

```bash
curl -sS -X POST http://localhost:8080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Write API documentation",
    "description": "Generate Swagger and verify the contract",
    "priority": "high",
    "due_date": "2026-12-31",
    "timezone": "Asia/Karachi"
  }'
```

The response includes stored UTC fields and computed fields:

```json
{
  "data": {
    "id": "...",
    "due_date": "2026-12-30T19:00:00Z",
    "due_date_human": "2 months from now",
    "due_week": "2026-W53",
    "is_overdue": false,
    "days_until_due": 72
  }
}
```

### Create a note in MongoDB

```bash
curl -sS -X POST http://localhost:8080/api/v1/notes \
  -H 'Content-Type: application/json' \
  -d '{
    "task_id": "TASK_ID",
    "author": "yotech",
    "body": "Follow up with the client on Monday.",
    "tags": ["follow-up", "client"]
  }'
```

### Read all three stores in one request

```bash
curl -sS http://localhost:8080/api/v1/tasks/TASK_ID/overview
```

```json
{
  "data": {
    "task": { "...": "from PostgreSQL (through Redis)" },
    "notes_count": 2,
    "recent_notes": [{ "...": "from MongoDB" }],
    "view_count": 42,
    "week_label": "2026-W43",
    "generated_at": "2026-10-19T12:00:00Z",
    "sources": ["postgresql", "mongodb", "redis"]
  }
}
```

## DTOs (for NestJS developers)

NestJS has a framework-level DTO concept; Go does not, but the pattern is
idiomatic and this project implements it explicitly in
`internal/transport/httpapi/dto`:

1. **Request DTOs** (`CreateTaskRequest`, `UpdateTaskRequest`,
   `CreateNoteRequest`, …) have `json` and `validate` tags.
2. **`validateDTO`** runs `go-playground/validator` and converts the first
   violation into `field: message`.
3. **Handlers map DTOs to domain inputs.** The domain never imports HTTP types.
4. **Response DTOs** (`Task`, `Note`, `OverviewResponse`, …) are separate from
   domain entities, so presentation fields such as `due_date_human` never leak
   into storage.
5. **Explicit conversion** in `mappers.go` replaces NestJS's
   `plainToInstance`/`class-transformer` magic with code you can step through.

## Date handling

Dates are the messiest part of most APIs, so the project centralizes them in
`internal/platform/dates`:

| Concern                 | Choice                                                                                    |
| ----------------------- | ----------------------------------------------------------------------------------------- |
| Flexible input          | `github.com/araddon/dateparse` accepts ISO dates, RFC3339, and common regional formats.   |
| Timezone interpretation | `time.LoadLocation` + `dateparse.ParseIn`; date-only values use the request's `timezone`. |
| Storage                 | Always UTC (`time.Time` → `TIMESTAMPTZ` / BSON date).                                     |
| Calendar math           | `StartOfDay`, `EndOfDay`, `DaysUntil`, `AddBusinessDays` (skips weekends).                |
| Human output            | `github.com/dustin/go-humanize` produces phrases such as `in 3 days`.                     |
| Grouping                | `time.ISOWeek` produces labels such as `2026-W43`.                                        |
| Clearing a deadline     | `"clear_due_date": true` on PATCH.                                                        |

Seed data uses business-day math, and one seeded task is intentionally overdue so
`is_overdue` and `due_date_human` are easy to see.

## Retry and failure visibility

Redis is wrapped by `internal/platform/retryx`, which uses
`github.com/avast/retry-go/v4`:

- every failed attempt logs `retry: attempt failed` with the operation, attempt
  number, and underlying error;
- `redis.Nil` (a normal cache miss) is **not** retried;
- the final failure logs `retry: operation failed permanently`;
- context cancellation stops retries immediately.

Startup connections for PostgreSQL, MongoDB, and Redis use the same helper, so a
wrong host or password produces a sequence of visible warnings followed by a
clear fatal error. During normal operation:

- a Redis cache read failure logs an error and falls back to PostgreSQL;
- a Redis write failure logs an error but does not roll back the committed
  PostgreSQL task;
- a MongoDB audit failure logs an error but does not fail the task request;
- `/readyz` returns `503` plus the concrete error and latency for each
  unhealthy dependency;
- shutdown logs every `Close`/`Disconnect` result.

You can watch retries without stopping the managed Redis by starting the service
with an intentionally wrong address:

```bash
REDIS_ADDR=127.0.0.1:1 HTTP_ADDR=:8081 make run
```

## Logging

Everything is logged with `log/slog` and a `*slog.Logger` that is passed
explicitly (no global logger, so tests can discard output).

| Format   | Rendered by                                    | Use                                    |
| -------- | ---------------------------------------------- | -------------------------------------- |
| `text`   | `github.com/lmittmann/tint` (slog handler)     | Local development, colorized and compact |
| `json`   | `slog.JSONHandler`                             | Production, machine-readable            |

The text format prints one compact line per record:

```
Oct 19 20:33:17.363 INF step 1/9: configuration loaded service=taskflow env=development addr=:8080
```

- the level is a colored badge: `DBG` cyan, `INF` green, `WRN` yellow, `ERR` red;
- the timestamp and the attribute keys are dimmed, the values are normal;
- error values are printed in red, so a failure stands out in a wall of text.

Colors are only written when the output is a terminal (`LOG_COLOR=auto`, the
default). `LOG_COLOR=always` forces them, `LOG_COLOR=never` disables them, and
piping the output to a file or a container log collector keeps it free of escape
sequences. The `json` format never contains colors.

## Automated API documentation

Two artifacts serve different purposes:

| Artifact                              | Generated?                                | Use                                     |
| ------------------------------------- | ----------------------------------------- | --------------------------------------- |
| `docs/swagger/swagger.json` / `.yaml` | **Yes**, `swag` reads handler annotations | Live Swagger UI and client discovery    |
| `api/openapi.yaml`                    | Hand-maintained OpenAPI 3 contract        | Reviewable source for client generation |

Regenerate after changing a handler, DTO, or annotation:

```bash
make docs
```

The command runs `swag init` with `--parseDependency --parseInternal`, writes
`docs/swagger/`, and the router serves it at `/swagger/index.html`. Because the
generated package is committed, `go build` works without running the generator
first.

## Database ownership

The project writes only to isolated namespaces:

| Store      | Namespace           | Contents                                     |
| ---------- | ------------------- | -------------------------------------------- |
| PostgreSQL | schema `taskflow`   | `tasks` table                                |
| PostgreSQL | default schema      | `goose_db_version` (migration metadata only) |
| MongoDB    | database `taskflow` | `notes`, `task_events` collections           |
| Redis      | keys `taskflow:*`   | task cache + view counters                   |

The existing `sample_mflix` MongoDB database and the rest of the PostgreSQL
cluster are never modified.

## Project layout

```text
.
├── .env / .env.example            # Local config (real .env is Git-ignored)
├── api/openapi.yaml               # Hand-maintained OpenAPI 3 contract
├── cmd/
│   ├── api/
│   │   ├── main.go                # Composition root, startup/shutdown, annotations
│   │   └── seed.go                # Idempotent demo tasks + notes
│   └── jsonplaceholder/           # Outbound HTTP demo (unchanged)
├── docs/
│   ├── ARCHITECTURE.md
│   └── swagger/                   # Generated Swagger document + UI source
├── internal/
│   ├── config/                    # .env loading, env parsing, validation
│   ├── domain/
│   │   ├── task/                  # Task entity, rules, cache/audit ports
│   │   └── note/                  # Note entity, rules, Mongo repository port
│   ├── platform/
│   │   ├── dates/                 # Flexible parsing + calendar math
│   │   ├── health/                # Dependency readiness checks
│   │   ├── logger/                # colorized slog setup
│   │   ├── redact/                # Password-safe URL logging
│   │   └── retryx/                # retry-go wrapper with visible logs
│   ├── storage/
│   │   ├── memory/                # Original in-memory adapter (kept for tests)
│   │   ├── mongodb/               # Notes CRUD + audit events + indexes
│   │   ├── postgres/              # pgx task repository + goose migrations
│   │   └── redis/                 # Cache, counters, retry wrapper
│   └── transport/httpapi/
│       ├── dto/                   # Request/response DTOs
│       ├── mappers.go             # Domain ↔ DTO conversion
│       ├── validation.go          # validator setup + messages
│       ├── task_handler.go        # Tasks + overview
│       ├── note_handler.go        # Notes
│       └── router.go              # Routes, middleware, Swagger UI
├── migrations/                    # Embedded goose migrations
├── scripts/smoke.sh               # End-to-end CRUD across all stores
├── Dockerfile
└── Makefile
```

## Configuration

`config.Load()` loads `.env` (if present) with `godotenv`, then reads real
environment variables. Shell/container variables win over the file.

| Variable                   | Default       | Meaning                                                             |
| -------------------------- | ------------- | ------------------------------------------------------------------- |
| `APP_ENV`                  | `development` | Environment label                                                   |
| `HTTP_ADDR`                | `:8080`       | Listen address                                                      |
| `HTTP_READ_HEADER_TIMEOUT` | `5s`          | Slow-header protection                                              |
| `HTTP_READ_TIMEOUT`        | `10s`         | Whole-request read timeout                                          |
| `HTTP_WRITE_TIMEOUT`       | `15s`         | Response write timeout                                              |
| `HTTP_IDLE_TIMEOUT`        | `60s`         | Keep-alive idle timeout                                             |
| `SHUTDOWN_TIMEOUT`         | `10s`         | Graceful drain deadline                                             |
| `LOG_LEVEL`                | `info`        | `debug`, `info`, `warn`, `error`                                    |
| `LOG_FORMAT`               | `text`        | `text` or `json`                                                    |
| `LOG_COLOR`                | `auto`        | `auto`, `always`, or `never` (text format only)                     |
| `POSTGRES_URL`             | required      | PostgreSQL connection string                                        |
| `MONGO_URI`                | required      | MongoDB connection string                                           |
| `MONGO_DATABASE`           | `taskflow`    | Database owned by the project                                       |
| `MONGO_CONNECT_TIMEOUT`    | `10s`         | Server selection timeout                                            |
| `REDIS_ADDR`               | required      | `host:port`                                                         |
| `REDIS_USERNAME`           | empty         | Redis ACL username                                                  |
| `REDIS_PASSWORD`           | empty         | Redis ACL password                                                  |
| `REDIS_DB`                 | `0`           | Logical database                                                    |
| `REDIS_DIAL_TIMEOUT`       | `5s`          | TCP connect timeout                                                 |
| `REDIS_READ_TIMEOUT`       | `3s`          | Socket read timeout                                                 |
| `REDIS_WRITE_TIMEOUT`      | `3s`          | Socket write timeout                                                |
| `DB_CONNECT_ATTEMPTS`      | `5`           | Startup attempts per dependency                                     |
| `DB_CONNECT_RETRY_DELAY`   | `2s`          | Delay between startup attempts                                      |
| `REDIS_RETRY_ATTEMPTS`     | `4`           | Attempts per Redis command                                          |
| `REDIS_RETRY_DELAY`        | `250ms`       | Delay between command attempts                                      |
| `CACHE_TTL`                | `5m`          | Task cache TTL                                                      |
| `AUTO_MIGRATE`             | `true`        | Run PostgreSQL/MongoDB setup at startup                             |
| `SEED_DEMO_DATA`           | `true`        | Insert demo data when the task table is empty                       |

Invalid values fail fast at startup with the variable name in the error.

## Development commands

```bash
make help          # list targets
make run           # run the API (loads .env, migrates, seeds)
make build         # build bin/taskflow
make test          # run tests
make test-race     # race detector
make check         # gofmt + go vet + tests
make docs          # regenerate Swagger from annotations
make smoke         # full CRUD across PostgreSQL, MongoDB, Redis
make tidy          # keep go.mod/go.sum consistent
make docker-build  # build the container image
```

To decrypt `.env`

```bash
gpg -o .env -d .env.gpg
```

remember the catchphrase

## Testing

```bash
make test-race
```

- Domain unit tests use a hand-written fake repository.
- HTTP integration tests use `httptest` with the in-memory adapter; they do not
  require databases.
- The smoke script exercises the real PostgreSQL, MongoDB, and Redis stores,
  including notes CRUD, the cross-database overview, cache behavior, and audit
  writes.

The project uses Go 1.24-compatible dependency versions. If `go get` is run
without pinned versions, newer modules may require Go 1.25/1.26; keep the pins in
`go.mod` unless you also upgrade the toolchain and Docker base image.
