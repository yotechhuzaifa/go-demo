# Architecture

This document explains how a request moves through the service, how the three
data stores divide responsibility, and why the packages are arranged this way.

## Dependency direction

```text
                              cmd/api
                                 │
        ┌──────────────┬─────────┼──────────┬─────────────┐
        ▼              ▼         ▼          ▼             ▼
     config         logger    httpapi    postgres     mongodb/redis
                                 │
                    ┌────────────┴────────────┐
                    ▼                         ▼
               domain/task               domain/note
                    ▲                         ▲
                    │                         │
           postgres/task_repository   mongodb/note_repository
           redis cache (port)         mongodb/event_repository
           mongodb events (port)
```

Rules:

- `domain/task` and `domain/note` import only the standard library (plus UUID
  generation in the service layer). They know nothing about HTTP, SQL, BSON, or
  Redis.
- Ports (`task.Repository`, `task.TaskCache`, `task.EventRecorder`,
  `note.Repository`) are declared by the domain and implemented by storage.
- `httpapi` owns DTOs, validation, JSON, status codes, and Swagger annotations.
- `cmd/api` is the only place that chooses concrete adapters.
- `storage` packages never import `httpapi`.
- `platform` contains cross-cutting helpers with no business rules.

## Data ownership

| Store | Namespace | Purpose |
| --- | --- | --- |
| PostgreSQL | schema `taskflow` | Authoritative task rows |
| PostgreSQL | default schema / `goose_db_version` | Migration history only |
| MongoDB | database `taskflow` | Note documents and task audit events |
| Redis | `taskflow:*` keys | Task cache and overview counters |

The project never writes to `sample_mflix` or any schema outside `taskflow`.

### Why these stores?

- **Tasks** need relational guarantees, filtered listings, and an obvious
  transactional source of truth → PostgreSQL.
- **Notes** are self-contained documents with an array of tags and an evolving
  shape → MongoDB.
- **Cache and counters** need sub-millisecond reads and atomic increments →
  Redis.

The overview endpoint makes the trade-off explicit: strongly consistent task
data from PostgreSQL, eventually consistent note counts from MongoDB, and a
best-effort view counter from Redis.

## Request lifecycle: POST /api/v1/tasks

1. **`net/http` server** accepts the connection. Timeouts and a maximum header
   size limit abusive or stalled clients.
2. **Request ID middleware** validates or generates `X-Request-ID` and stores it
   in the request context.
3. **Logging middleware** captures status, bytes, and duration.
4. **Recovery middleware** converts a panic into a logged `500`.
5. **`http.ServeMux`** matches `POST /api/v1/tasks`.
6. **Handler** strictly decodes JSON into `dto.CreateTaskRequest`, rejects
   unknown fields, and runs `go-playground/validator`.
7. **Date adapter** parses `due_date` flexibly, interprets date-only values in
   `timezone`, and converts to UTC.
8. **`task.Service`** trims/validates fields, applies defaults, generates a UUID,
   and calls `task.Repository.Create`.
9. **PostgreSQL adapter** executes an explicit `INSERT` with pgx.
10. **Redis adapter** writes the task as JSON with `CACHE_TTL`.
11. **MongoDB adapter** appends a `task_events` document with the full snapshot.
12. **Mapper** builds `dto.Task`, adding `due_date_human`, `due_week`,
    `is_overdue`, and `days_until_due`.
13. The response travels back through the middleware in reverse order.

Steps 10 and 11 are deliberately **best-effort**. PostgreSQL is the source of
truth; a cache or audit outage must not lose a committed task. Both failures are
logged at error level and surface through `/readyz`.

## Read-through caching

`GET /api/v1/tasks/{id}`:

```text
handler → task.Service.Get
             │
             ├─ Redis GET taskflow:task:{id}
             │     ├─ hit  → return JSON-decoded Task
             │     └─ miss → continue
             │
             ├─ PostgreSQL SELECT
             └─ Redis SET (TTL)
```

- A corrupt cache entry is logged, deleted, and treated as a miss.
- A Redis error is logged and treated as a miss.
- Updates refresh the entry; deletes remove it.
- Listings bypass the cache because filtered/paginated keys would multiply.

## Cross-database overview

`GET /api/v1/tasks/{id}/overview`:

1. Task from `task.Service.Get` (Redis → PostgreSQL).
2. Note count and five most recent notes from MongoDB.
3. Atomic `INCR taskflow:task:{id}:views` in Redis.
4. Response declares `sources: ["postgresql", "mongodb", "redis"]`.

If the Redis counter fails, the handler logs the error and still returns the
task and notes with `view_count: 0`. Losing a view metric should not make the
whole dashboard unavailable.

## Error model

| Domain error | HTTP status | API code |
| --- | --- | --- |
| `*task.ValidationError` | 400 | `validation_error` |
| `*note.ValidationError` | 400 | `validation_error` |
| `task.ErrNotFound` / `note.ErrNotFound` | 404 | `not_found` |
| `task.ErrAlreadyExists` / `note.ErrAlreadyExists` | 409 | `conflict` |
| `context.DeadlineExceeded` | 504 | `timeout` |
| Anything else | 500 | `internal_error` |

Unexpected errors are logged with the request ID but reported generically.

## DTOs and validation

```
JSON request
    │
    ▼
dto.CreateTaskRequest          (json + validate tags)
    │  validateDTO
    ▼
task.CreateInput               (domain types, UTC time.Time)
    │
    ▼
domain rules
    │
    ▼
dto.Task                       (computed presentation fields)
    │
    ▼
JSON response
```

This mirrors the NestJS DTO pattern without reflection-based transformation.
Mappings live in `internal/transport/httpapi/mappers.go`.

## Date handling

- Input: `araddon/dateparse` accepts ISO, RFC3339, and regional formats.
- Timezone: date-only values are interpreted through `time.LoadLocation`.
- Storage: UTC everywhere (`TIMESTAMPTZ`, BSON dates).
- Math: `StartOfDay`, `EndOfDay`, `DaysUntil`, `AddBusinessDays`.
- Output: `go-humanize` relative phrases and ISO week labels.

Computed fields are never stored, so they cannot become stale.

## Retry and connection visibility

- Startup: PostgreSQL, MongoDB, and Redis connections use `retryx.Do` with
  `DB_CONNECT_ATTEMPTS` / `DB_CONNECT_RETRY_DELAY`. Every failed attempt logs the
  driver error.
- Redis commands: `retryx.DoIf` retries network errors but not `redis.Nil`.
- PostgreSQL: pgx is configured with the simple query protocol because the
  project's pooled endpoint (Prisma/pgbouncer) cannot guarantee named prepared
  statements across backends.
- Readiness: pings all three dependencies concurrently with a 3-second timeout
  and returns concrete errors and latencies.
- Shutdown: closes Redis, MongoDB, then PostgreSQL, logging each result.

## Migrations

`migrations/0001_create_tasks.sql` is embedded with `go:embed` and applied by
goose at startup when `AUTO_MIGRATE=true`. Embedded files mean the container does
not depend on a migrations directory at runtime. `migrations/0002_updated_at_trigger.sql`
follows it and changes the `updated_at` trigger so the application's own
timestamp survives an update.

The migration uses `-- +goose StatementBegin` around the PL/pgSQL trigger
function because goose otherwise splits on the semicolons inside the
dollar-quoted body.

Objects are schema-qualified (`taskflow.tasks`) because the project's
transaction-mode pooler rejects a `search_path` startup parameter. The schema
name is therefore not configurable: it is a constant in the `postgres` package
(`postgres.Schema`) and it has to change together with the migration.

## API documentation generation

- Handlers and `cmd/api/main.go` carry `swag` annotations.
- `make docs` runs `swag init` into `docs/swagger/`.
- The router serves the generated document at `/swagger/index.html`.
- `api/openapi.yaml` remains a reviewable OpenAPI 3 contract for clients.

## Extending the service

| Feature | Location |
| --- | --- |
| New PostgreSQL table | new goose migration + `internal/storage/postgres` |
| New document collection | `internal/storage/mongodb` + domain port |
| New cache concern | `internal/storage/redis` + a domain port |
| Auth middleware | `internal/transport/httpapi` |
| New use case | `internal/domain/<area>` |
| Metrics/tracing | middleware or `internal/platform` |
| Background jobs | `cmd/worker` + an `internal/jobs` package |

Keep the dependency rule intact and the project stays easy to reason about.
