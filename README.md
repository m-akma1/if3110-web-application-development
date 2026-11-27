# IF3110 Web Application Development Project 

## Overview

### Milestone 1

In this milestone, we implemented a database web platform where a team can work with several databases from one place, without handing out raw database credentials.

- A **Platform Admin** registers target PostgreSQL databases once. Their credentials are stored encrypted and are never shown to anyone again.
- Users get access through **groups**. Each group has an access level per database: no access, **View Only**, or **View & Edit**.
- **View Only** users browse tables, schemas and data, run read-only queries, and read the migration history.
- **View & Edit** users can also write migration scripts, test them with a dry run (including query plans), and execute them. Every execution runs in a single transaction and is recorded in an immutable history.

### Milestone 2

In this milestone, the platform was extended so that database changes are reviewed before they run, large migrations run in the background, and every run can be traced.

- The whole interface is a **responsive single-page application** backed by a JSON API.
- Every change to a database, including rollbacks and forwards, needs **approval from the leader of every group** that can access it.
- Migrations can be **rolled back and forwarded** in order, and every attempt is kept in an **immutable execution history**.
- Migrations run **asynchronously** in a worker pool, one at a time per database. Large data changes can run **per batch**, and a stopped run can be resumed or undone.
- **Structured logs** and **OpenTelemetry traces** follow each run from the request to every SQL statement, with **metrics and dashboards** in Grafana.
- The platform is covered by **unit and integration tests** in CI, **stress-tested with k6**, and scanned with **CodeQL SAST**.

## Build and Run

### Requirements

- Docker with Docker Compose v2
- `openssl` (to generate secrets)
- For local development only: Go 1.26 and Node.js 22
- Optional: `jq` to filter the JSON logs. k6 runs in Docker, so it needs no install

### Steps

1. Generate secrets to fill in environment variables.
   ```sh
   openssl rand -base64 32
   ```
2. Create a `.env` file from the template and copy the secrets into it.
   ```sh
   cp .env.example .env
   # then open the file and replace every `change-me` value with an actual secret
   ```
   `TARGET_CREDENTIALS_KEY` must be the output of step 1. Any other value is rejected at startup.
3. Build the containers and start the stack.
   ```sh
   docker compose up --build
   ```
   On startup the app applies the platform schema migrations, creates the bootstrap admins, seeds the demo data (when enabled), and connects every registered target database.
4. Open **[https://localhost](https://localhost)**. Caddy uses a self-signed internal certificate, so accept the browser warning or trust Caddy's root CA. The app container has no published host port since all traffic goes through Caddy on ports 80 and 443.
5. Log in with an account from `BOOTSTRAP_ADMINS` or `SEED_DEMO_USERS`, or sign up a new account.

The app refuses to start when a required variable is missing or invalid. The `postgres` and `target-postgres` containers also refuse to start when one of their variables is missing.

To reset all data (platform database and demo database), remove the volumes:

```sh
docker compose down -v
```

### Target Databases

Any PostgreSQL server the app container can reach can be a target. To run one in Docker next to the stack:

1. Start it on the platform's network. Compose names that network `<project>_app_net`, where the project is the repository folder name by default (check with `docker network ls`):
   ```yaml
   # my-db/docker-compose.yml
   services:
     my-db:
       image: postgres:16-alpine
       environment:
         POSTGRES_USER: app
         POSTGRES_PASSWORD: change-me
         POSTGRES_DB: mydb
       networks: [platform]
   networks:
     platform:
       external: true
       name: if3110-web-application-development_app_net
   ```
   ```sh
   docker compose -f my-db/docker-compose.yml up -d   # after the platform stack is up
   ```
2. In **Admin → Databases**, register it with host `my-db` (the service name), port `5432` and the credentials above.
3. In **Admin → Groups**, grant groups access to it. A group needs an active leader first.

To remove it, delete it in **Admin → Databases** (this also removes its grants and migration history), then `docker compose -f my-db/docker-compose.yml down -v`.


### Local Development

The `Makefile` holds the developer shortcuts. Run them from the repository root.

| Command | Description |
|---|---|
| `make frontend-dev` | Run the Vite dev server for the React SPA (hot reload) |
| `make frontend-build` | Build the SPA into `frontend/dist` (served by the Go app) |
| `make build` | Build the SPA, then build the Go binary |
| `make dev` | Run the Go API server with `go run .` |
| `make vet` | Run `go vet` |
| `make test` | Run unit tests (integration tests are skipped without `TEST_DATABASE_URL`) |
| `make test-integration` | Start the disposable test PostgreSQL and run every test against it |
| `make test-db-down` | Stop the test PostgreSQL |
| `make observability` | Start the stack plus the OpenTelemetry Collector, Tempo, Loki, Prometheus and Grafana (<http://localhost:3000>), with traces and logs exported over OTLP; `make observability-down` stops it |
| `make loadtest` | Run the k6 stress test against the running stack, with databases created from this repository (see [Stress Test (k6)](#stress-test-k6)) |
| `make loadtest-local` | The same with our private local databases (not in the repository) |
| `make seed-large` | Create the 2,000,000-row `events` table on the demo target, for trying batch mode; needs the Compose stack running |

Run `npm --prefix frontend install` once before using the frontend targets. The Go server serves the built SPA from `frontend/dist`, so run `make frontend-build` at least once before `make dev`.

`make dev` reads its configuration from the environment, so export the `.env` variables first. The Compose databases do not publish host ports, so point `POSTGRES_HOST` and `TARGET_POSTGRES_HOST` at PostgreSQL servers reachable from your machine. The session and CSRF cookies are `Secure` and accepted by browsers on `http://localhost`.

### Environment Variables

All variables are **required** unless marked optional. Copy `.env.example` to `.env` and replace every `change-me-*` value before deploying. For required variables, the value column shows the `.env.example` value. For optional variables, it shows the default used when the variable is unset.

#### Platform Database

| Variable | Default Value | Description |
|---|---|---|
| `POSTGRES_USER` | `if3110` | Username for the application's internal PostgreSQL database |
| `POSTGRES_PASSWORD` | `change-me-postgres-password` | Password for the internal database user |
| `POSTGRES_DB` | `dbplatform_core` | Name of the application's internal database |
| `POSTGRES_HOST` | `postgres` | (Optional) Hostname of the internal database |
| `POSTGRES_PORT` | `5432` | (Optional) Port of the internal database |
| `POSTGRES_SSLMODE` | `disable` | (Optional) SSL mode used to connect to the internal database: `disable`, `allow`, `prefer`, `require`, `verify-ca`, or `verify-full` |

#### Public Target Database

| Variable | Default Value | Description |
|---|---|---|
| `TARGET_POSTGRES_USER` | `target_admin` | Superuser username, only used by the `target-postgres` container |
| `TARGET_POSTGRES_PASSWORD` | `change-me-target-admin` | Superuser password, only used by the `target-postgres` container |
| `TARGET_POSTGRES_DB` | `demo_target` | Name of the target database |
| `TARGET_APP_USER` | `app_target` | Regular user role the platform uses to connect to the target. The demo database is registered from these values when `SEED_DEMO_DATA=true` |
| `TARGET_APP_PASSWORD` | `change-me-app-target` | Password for the regular user role |
| `TARGET_POSTGRES_HOST` | `target-postgres` | (Optional) Hostname of the target database |
| `TARGET_POSTGRES_PORT` | `5432` | (Optional) Port of the target database |
| `TARGET_POSTGRES_SSLMODE` | `disable` | (Optional) SSL mode used to connect to the target database: `disable`, `require`, or `verify-full` |

The `target-postgres` init scripts ([docker/target-init/](docker/target-init/)) run only on the first start of its volume, and the demo database is registered only once. Changing these values later does not update an existing setup: edit the connection in **Admin → Databases**, or reset the volumes with `docker compose down -v`.

#### Security

| Variable | Default Value | Description |
|---|---|---|
| `TARGET_CREDENTIALS_KEY` | `change-me-credentials-key` | Encryption key for target database passwords: 32 random bytes encoded as base64. Generate with `openssl rand -base64 32`. **Do not change once injected**, or existing stored credentials can no longer be decrypted |

#### Query Limits

| Variable | Default Value | Description |
|---|---|---|
| `QUERY_TIMEOUT_MS` | `5000` | (Optional) Maximum execution time in milliseconds for each read-only statement: Query Explorer queries and table browsing (row count, page of rows, schema lookups) |
| `QUERY_ROW_LIMIT` | `500` | (Optional) Maximum number of rows returned by a read-only query |

#### Migration Limits

| Variable | Default Value | Description |
|---|---|---|
| `MIGRATION_LOCK_TIMEOUT_MS` | `5000` | (Optional) Maximum time in milliseconds to wait for a database lock |
| `MIGRATION_STATEMENT_TIMEOUT_MS` | `60000` | (Optional) Maximum execution time in milliseconds for a single migration statement |
| `MIGRATION_QUEUE_TIMEOUT_MS` | `15000` | (Optional) Maximum time in milliseconds a worker waits for a database's migration lock. When it expires the job goes back to the queue without using an attempt |

The resume/abort buttons of batch migrations can be hidden with the frontend build argument `VITE_ENABLE_BATCH_ACTIONS=false` (default `true`).

The worker pool itself uses fixed settings: 4 workers, a 30 s lease renewed every 10 s, at most 3 attempts per job, retry delay 2 s doubling up to 1 min, and a lease check every 10 s (`migration.DefaultWorkerConfig`).

#### Server and Logging

| Variable | Default Value | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | (Optional) Address the app listens on. Caddy proxies to `app:8080`, so change both together |
| `LOG_LEVEL` | `info` | (Optional) Minimum log level: `debug`, `info`, `warn` or `error` |

#### Tracing

| Variable | Default Value | Description |
|---|---|---|
| `OTEL_TRACES_EXPORTER` | `console` | (Optional) `console` writes every finished span as one JSON line on stdout, next to the logs. `none` still creates spans (so logs keep their `trace_id`/`span_id`) but exports nothing |
| `OTEL_SERVICE_NAME` | `dbplatform` | (Optional) `service.name` on every span and exported log record |
| `OTEL_LOGS_EXPORTER` | `none` | (Optional) `otlp` also sends every log record over OTLP (stdout stays as it is). See [Observability](#observability) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://otel-collector:4318` in Compose | (Optional) Where `otlp` traces and logs go (OTLP/HTTP). The other standard `OTEL_EXPORTER_OTLP_*` variables work too |
| `METRICS_ADDR` | `:9091` | (Optional) Address of the Prometheus endpoint `/metrics`, separate from the app port and not routed by Caddy. `off` disables it |

#### Session Handling

| Variable | Default Value | Description |
|---|---|---|
| `SESSION_IDLE_TIMEOUT_MINUTES` | `30` | (Optional) Minutes of inactivity before a session expires |
| `SESSION_ABSOLUTE_EXPIRATION_HOURS` | `12` | (Optional) Maximum session lifetime in hours, regardless of activity |
| `SESSION_ROTATION_INTERVAL_MINUTES` | `15` | (Optional) Interval in minutes at which the session token is rotated |
| `SESSION_GRACE_PERIOD_SECONDS` | `15` | (Optional) Seconds the previous session token remains valid after rotation |

All numeric limits must be positive whole numbers.

#### Initial Platform Admin

| Variable | Default Value | Description |
|---|---|---|
| `BOOTSTRAP_ADMINS` | JSON array (see below) | Initial Platform Admin accounts. Required as long as there is no active Platform Admin; may be left empty afterwards. Created only once and never overwrites an existing account |

**`BOOTSTRAP_ADMINS` fields**

| Field | Required | Description |
|---|---|---|
| `username` | Yes | Admin username |
| `password` | Yes | Admin password |
| `email` | No | Admin email address |

Example:

```dotenv
BOOTSTRAP_ADMINS='[{"username":"admin","password":"change-me-admin-password","email":"admin@example.com"}]'
```

Usernames must be between 3 to 32 characters of `a-z`, `0-9`, `.`, `_` or `-` and passwords must be between 8 to 128 characters. Unknown JSON fields and duplicate usernames or emails are rejected.

#### Demo Data

Demo seeding is insert-only. If `SEED_DEMO_DATA=true`, `SEED_DEMO_USERS` and `TARGET_APP_*` are required.

| Variable | Default Value | Description |
|---|---|---|
| `SEED_DEMO_DATA` | `false` | (Optional) Enables seeding of demo data. `.env.example` sets it to `true` |
| `SEED_DEMO_USERS` | JSON array (see below) | Demo users to create. Required when `SEED_DEMO_DATA=true` |
| `SEED_DEMO_GROUPS` | JSON array (see below) | (Optional) Demo groups to create |

**`SEED_DEMO_USERS` fields**

| Field | Required | Description |
|---|---|---|
| `username` | Yes | User's username |
| `password` | Yes | User's password |
| `email` | No | User's email address |
| `role` | No | `USER` or `PLATFORM_ADMIN` (default: `USER`) |

**`SEED_DEMO_GROUPS` fields**

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Group name |
| `description` | Yes | Short description of the group |
| `invite_code` | Yes | Code used to join the group. Should be hard to guess |
| `grant` | Yes | Access level on the demo database: `NONE`, `VIEW_ONLY`, or `VIEW_EDIT` |
| `members` | Yes | Usernames from `SEED_DEMO_USERS` |

When enabled, the app creates the demo users and groups that do not exist yet, registers the demo database as **PostgreSQL Demo** (only if no database with that name exists), and gives each demo group its `grant` on it.

#### Testing

| Variable | Default Value | Description |
|---|---|---|
| `TEST_DATABASE_URL` | `postgres://test:test@localhost:5433/dbplatform_test?sslmode=disable` | Connection string for the database used by `make test-integration` |

## Project Structure

### Technology Stack

| Layer | Technology |
|---|---|
| Backend language and server | Go 1.26, standard library `net/http` router (method + path patterns) |
| API | REST JSON under `/api/*`, cookie session + double-submit CSRF token |
| Frontend | React 18 + TypeScript 5 Single Page Application, React Router 6 |
| Frontend build | Vite 6 (output `frontend/dist`, served by the Go app with an `index.html` fallback) |
| Styling | Tailwind CSS 3.4 (PostCSS + Autoprefixer) |
| Databases | PostgreSQL 16, driver `jackc/pgx/v5` |
| Password hashing | Argon2id (`golang.org/x/crypto`) |
| Logging | `log/slog` structured JSON logs on stdout (one logger, request correlation ID, trace/span IDs, redaction) |
| Tracing | OpenTelemetry Go SDK, `otelhttp`, W3C Trace Context carried through the job queue; console (default) or OTLP exporter, logs over OTLP through the `otelslog` bridge |
| Metrics | Prometheus Go client on a separate `/metrics` port |
| Observability stack (bonus) | OpenTelemetry Collector, Grafana Tempo, Loki, Prometheus and Grafana (Compose profile `observability`) |
| Load testing (bonus) | k6 (`grafana/k6` Docker image) |
| Background jobs | Job queue in a PostgreSQL table (`FOR UPDATE SKIP LOCKED`), worker pool inside the app process |
| History and audit | Append-only `migration_executions` table (enforced by a trigger) and `audit.*` log events |
| Reverse proxy and TLS | Caddy 2 |
| Deployment | Docker multi-stage build (Node -> Go -> Alpine), Docker Compose |
| CI and security | GitHub Actions (frontend build, gofmt, vet, build, tests with `-race`, Docker build), CodeQL SAST (`security-extended`) |

### Architecture

```mermaid
flowchart LR
    subgraph browser["Browser"]
        spa["React SPA<br/>(React Router, fetch API client)"]
    end

    subgraph compose["Docker Compose network (app_net)"]
        caddy["Caddy<br/>TLS on :443 and :80"]

        subgraph app["Go app (:8080)"]
            static["SPA host<br/>frontend/dist + index.html fallback"]
            http["internal/http<br/>REST /api/*, CSRF guard,<br/>access log, request spans"]
            worker["migration worker pool<br/>claim, lease, retry"]

            authn["authn<br/>users, sessions"]
            rbac["rbac<br/>access decisions"]
            groups["groups<br/>groups, leaders, invite codes"]
            admin["admin<br/>console services"]
            migr["migration<br/>drafts, dry run, approvals,<br/>job queue, engine,<br/>rollback/forward, history"]
            obs["logging · audit · tracing · metrics<br/>used by every component:<br/>JSON logs, audit.* events, OTel spans<br/>(stdout or OTLP), /metrics on :9091"]

            viewer["dbmanager<br/>target pools, read-only viewer"]
        end

        platform[("postgres<br/>platform DB")]
        target[("target-postgres<br/>demo target DB")]
    end

    other[("Other registered<br/>PostgreSQL targets")]

    spa -- HTTPS --> caddy
    caddy -- HTTP --> static
    caddy -- HTTP --> http
    http --> authn & rbac & groups & admin & migr & viewer
    worker -- "claims and runs jobs" --> migr
    authn & rbac & groups & admin & migr --> platform
    admin -- "register targets" --> viewer
    migr -- "target pools" --> viewer
    viewer -- SQL --> target & other

    %% layout only: keep the databases in their own column right of the app
    worker ~~~ obs
    viewer ~~~ platform
```

- The browser loads the SPA once (`index.html` + `/assets/*`). Every page change after that is handled by React Router on the client, and data comes from the JSON API under `/api/*`.
- The **platform database** stores users, sessions, groups, grants, invite codes, registered targets (with sealed passwords), migrations, the **job queue** (`migration_jobs`) and the **execution history** (`migration_executions`). It is never exposed as a viewer target.
- **Target databases** are only reached through the connection registry, which holds one small pool (up to 5 connections) per connected target. The Data Viewer, the target manager and the migration engine share it.
- Every request that touches a target first identifies the user (`authn`), then asks `rbac.Authorizer` whether the action is allowed. Handlers never compare access levels themselves.
- Logging, audit events, tracing and metrics are used by every component, so the diagram shows them as one box.

### Repository Structure

```text
.
├── main.go                      # Entry point: config, platform migrations, bootstrap, server
├── domain/                      # JSON view models (DTOs) returned by the API
├── internal/
│   ├── config/                  # Reads and validates every environment variable
│   ├── platformdb/              # Embedded platform schema migrations (migrations/NNN_*.sql)
│   ├── authn/                   # Users, Argon2id passwords, session lifecycle
│   ├── rbac/                    # Access levels, actions, effective-access rule, grant store
│   ├── groups/                  # Groups, memberships, group leaders, invite codes
│   ├── admin/                   # Platform Admin console services
│   ├── dbmanager/               # Target registry, credential encryption, read-only guard, viewer queries
│   ├── migration/               # Splitter, script checks, engine, rollback/forward, approvals, EXPLAIN,
│   │                            #   job queue + worker pool, append-only execution history
│   ├── audit/                   # audit.<action> events: who did what, with which outcome
│   ├── bootstrap/               # Bootstrap admins and demo data seeding
│   ├── http/                    # REST API handlers, SPA host, middleware, cookies
│   ├── logging/                 # Structured JSON logger (slog), correlation ID and actor in context, SQL log + span helper
│   ├── tracing/                 # OpenTelemetry setup (console/otlp traces, otlp logs), traceparent for the job queue
│   ├── metrics/                 # Prometheus metrics (jobs, lock wait, batches, HTTP routes, pools) on METRICS_ADDR
│   ├── ids/                     # UUID helpers
│   ├── accesstest/, testutil/   # Test fixtures and test database helpers
├── frontend/                    # React + TypeScript SPA (Vite)
│   ├── src/api/                 # fetch client (session cookie + CSRF header)
│   ├── src/context/             # Auth context (current user, CSRF token)
│   ├── src/pages/               # Login, signup, dashboard (viewer), migration, reviews, admin
│   ├── src/components/          # Layout, sidebar, data grid, dry-run report, migration and admin parts
│   ├── src/types/, src/utils/   # Shared TypeScript types and helpers
│   └── public/static/           # Images; copied into frontend/dist/static, served under /static
├── docs/                        # Details linked from this README: batch mode, logging and tracing examples,
│                                #   observability, CodeQL SAST report
├── docker/target-init/          # Init scripts for the demo target database
├── docker/seed/                 # Optional large dataset for batch mode (make seed-large)
├── docker/observability/        # Collector, Tempo, Loki, Prometheus, Grafana config + dashboard (make observability)
├── loadtest/                    # k6 stress test (stress.js, run.sh, local-run.sh); runs write results/*.json (git-ignored)
├── Dockerfile                   # SPA build (Node) -> Go build -> Alpine runtime
├── docker-compose.yml           # caddy, app, postgres, target-postgres; profiles "test" and "observability"
├── Caddyfile                    # TLS, reverse proxy, log filtering
├── Makefile                     # Developer shortcuts
├── .github/workflows/codeql.yml # CodeQL SAST
└── .github/workflows/ci.yml     # CI pipeline
```

### Routes

#### SPA Pages

The Go server answers every non-API `GET` with `index.html`; React Router renders the page.

| Path | Access | Page |
|---|---|---|
| `/login`, `/signup` | Public | Login and signup |
| `/dashboard` | View Only | Database Viewer and read-only query explorer |
| `/migration` | View Only | Migration history (and drafts/editor for View & Edit) |
| `/migration/reviews` | Group Leader | Migrations waiting for the leader's review |
| `/migration/:id` | View Only | Migration detail, approvals, actions |
| `/admin` | Platform Admin | Admin console (users, groups, databases, invite codes) |
| `/forbidden` | Logged in | Access denied page |

#### JSON API

| Method and path | Access | Description |
|---|---|---|
| `GET /api/health` | Public | Liveness check |
| `GET /api/auth/me` | Public | Current user and CSRF token (401 when logged out) |
| `POST /api/auth/login`, `POST /api/auth/signup` | Public | Log in, sign up |
| `POST /api/auth/logout` | Logged in | Revokes the session |
| `GET /api/viewer` | View Only | Databases, schemas and tables for the selected database |
| `GET /api/viewer/table` | View Only | One page of a table with its schema |
| `POST /api/viewer/query` | View Only | Runs a read-only query |
| `GET /api/migration` | View Only | History and drafts (`?hide_rollback=true` hides rollback pairs) |
| `GET /api/migration/{id}` | View Only | Migration detail with approvals |
| `POST /api/migration/drafts` | View & Edit | Creates a draft (SQL up/down, irreversible flag) |
| `POST /api/migration/{id}`, `DELETE /api/migration/{id}` | View & Edit | Saves or deletes a draft |
| `POST /api/migration/{id}/dry-run` | View & Edit | Dry-runs a draft |
| `POST /api/migration/{id}/submit-review` | View & Edit | Sends a draft to group leaders for review |
| `GET /api/migration/pending-reviews` | Group Leader | Lists migrations (`reviews`) and batch action requests (`batch_requests`) waiting for review |
| `POST /api/migration/{id}/approve`, `POST /api/migration/{id}/reject` | Group Leader | Approves or rejects for the leader's group |
| `POST /api/migration/{id}/execute` | View & Edit | Queues an approved migration: `202` with the job, `409` when it is already queued |
| `POST /api/migration/{id}/rollback` | View & Edit | Rollback of the latest active migration: a request for approval (`200`), or a queued job (`202`) when no group needs to approve |
| `POST /api/migration/{id}/forward` | View & Edit | Forward of a rolled-back migration: a request for approval (`200`), or a queued job (`202`) |
| `GET /api/migration/{id}/status` | View Only | Migration with its newest job, for polling |
| `GET /api/migration/{id}/executions` | View Only | Every execution attempt of the migration, oldest first |
| `GET /api/jobs/{id}` | View Only | One job, for polling after a `202` |
| `GET /api/migration/{id}/batches` | View Only | Committed batches of a batch migration (`UP`, and `DOWN` for undone ones) |
| `POST /api/migration/{id}/resume` | View & Edit | Resumes a `PARTIAL` batch migration from its checkpoint: a request for approval (`200`, `requires_approval`), or a queued job (`202`) once the request is approved or when no group needs to approve. `409` while a request waits |
| `POST /api/migration/{id}/abort` | View & Edit | `{"rollback": true}` undoes the committed batches (job, `202`); `{"rollback": false}` marks it `ABORTED` (`200`). Same approval flow as resume |
| `POST /api/batch-requests/{id}/approve`, `POST /api/batch-requests/{id}/reject` | Group Leader | Approves or rejects a batch action request for the leader's group (`{"note"}` / `{"reason"}`, reason required) |
| `POST /api/migration/{id}/duplicate` | View & Edit | Copies a migration into a new draft |
| `GET /api/admin/overview` and `/api/admin/*` | Platform Admin | Admin console data and actions |

Database pages and API calls take the selected database as `?db_id=`.

## Features and Behavior

### Access Control

#### Authentication

- **Sign up** creates a `USER` account. Fields: username (3-32 characters of `a-z`, `0-9`, `.`, `_`, `-`, case-insensitive), optional email, password (8-128 characters), and an optional invite code. A valid, active invite code also adds the new user to that code's group. The user is logged in right after signing up.
- **Log in** accepts a username or an email (an identifier containing `@` is matched against emails). Deactivated accounts cannot log in. Wrong usernames and wrong passwords get the same error message.
- Passwords are hashed with **Argon2id** (64 MiB memory, 1 iteration, 4 threads, 16-byte salt). A dummy hash is verified when the account does not exist, so response time does not reveal which usernames exist.
- Login and signup refuse a request that puts the password in the URL query string (`400`), and read credentials only from the request body.
- **Log out** revokes the session on the server and clears the cookie.

#### Authorization

- There are two platform roles: `USER` and `PLATFORM_ADMIN`. The role only controls access to the admin console. **Platform Admin is not a database access level**: admins reach databases only through their groups, like everyone else.
- Database access comes from **group grants**. Each grant gives one group one level on one database:

  | Action | No access | View Only | View & Edit |
  |---|:-:|:-:|:-:|
  | See tables, schemas and data | | Yes | Yes |
  | Run read-only queries | | Yes | Yes |
  | See executed migrations (history and detail) | | Yes | Yes |
  | See, create, edit, delete and duplicate drafts | | | Yes |
  | Dry-run and execute migrations | | | Yes |

- A user in several groups gets the **most permissive** grant (View & Edit > View Only > none). There are no deny rules.
- Access is checked on the server for every request. A lowered grant or a removed membership takes effect on the user's next request.
- Unknown databases and migrations get the same "access denied" answer as forbidden ones, so ids cannot be probed.
- Users only see the databases they can access in the database switcher. A user without any access sees an empty state.

#### Session Handling

- A session is a random 256-bit opaque token. The browser keeps it in the `__Host-sid` cookie (`HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, no `Domain`). The server only stores its SHA-256 hash.
- **Idle timeout** (default 30 minutes): every request moves the idle deadline forward.
- **Absolute expiration** (default 12 hours): fixed at login and never extended.
- **Rotation** (default every 15 minutes): the token is replaced and the cookie updated. The previous token stays valid for a short **grace period** (default 15 seconds) so parallel requests are not logged out.
- Logging in revokes the session the browser had before, so a planted session cannot be reused.
- Privilege changes:
  - Promotion to Platform Admin, joining a group, or a raised grant: the user's sessions rotate on their next request.
  - Demotion, deactivation, or a password reset: all of the user's sessions are revoked, so they must log in again.
- Expired, revoked or unknown sessions are cleared. The API answers `401` and the SPA redirects to `/login`.
- **CSRF protection** uses a double-submit cookie: every `POST`, `PUT`, `PATCH` and `DELETE` must send an `X-CSRF-Token` header that matches the `__Host-csrf` cookie. The API returns the token in the `csrf_token` field (login, signup, `/api/auth/me`) and the SPA's fetch client sends it on every state-changing request.
- **Logging**: the access log records method, path, status and duration only, with sensitive query parameters redacted. Caddy removes `Cookie`, `Authorization` and `Set-Cookie` headers from its own access log.

#### Admin Platform

The admin console (`/admin`) is only available to Platform Admins and has four tabs:

- **Users**: create users (with role and groups), change roles, deactivate or reactivate accounts, and reset passwords. An admin cannot demote or deactivate their own account.
- **Groups**: create groups, edit name and description, add and remove members, choose each group's **leader** (Public included), delete groups, and set each group's access level per database (`NONE`, `VIEW_ONLY`, `VIEW_EDIT`). A group needs an active leader before it can get access to a database (see [Review and Approval](#review-and-approval)); a group that has access but lost its leader is flagged in red. Deleting a group also removes its memberships, grants and invite codes.
- **Databases**: register target PostgreSQL databases (display name, host, port, database name, username, password, SSL mode `disable`/`require`/`verify-full`).
  - The connection is tested on save. A target that fails the test is still saved with status `ERROR` and the reason, so the admin can fix it.
  - Editing with an empty password keeps the stored one. **Test connection** reconnects and updates the status.
  - Deleting a target also removes its grants and migration history.
  - Passwords are encrypted with **AES-256-GCM** using `TARGET_CREDENTIALS_KEY`, with the target's id bound as additional data. They are never displayed, and connection errors are shown with the password redacted.
- **Invite codes**: create a code for a group (custom, or a random one like `JOIN-7K2Q-MX4P`), and activate or deactivate it. Codes are 4-64 characters of `A-Z`, `0-9`, `_` or `-`, and case-insensitive.

#### Access Management

- **Public** is a built-in system group. Every user joins it automatically when the account is created, and cannot leave it. Public cannot be renamed or deleted, and it cannot have invite codes. It has no database access unless an admin grants it.
- Public's leader is the **first account in `BOOTSTRAP_ADMINS`**, set at startup whenever Public has no active leader. If that account does not exist or is deactivated, the earliest active Platform Admin leads Public. An admin can choose any active user instead.
- Users join other groups through an admin, or by signing up with an invite code.
- On first start, the accounts in `BOOTSTRAP_ADMINS` are created if there is no active Platform Admin. If there is none and the variable is empty, the app refuses to start. Existing accounts are never overwritten or promoted.

### Database Management

#### Database Viewer

- The navbar **database switcher** lists every database the user can access and keeps the current page (Viewer or Migration).
- The sidebar lists the tables (plain and partitioned) of every user schema, grouped by schema. System schemas are hidden.
- Selecting a table shows:
  - **Data**: 25 rows per page, ordered by the primary key when the table has one, so pages stay stable.
  - **Schema**: columns (type, nullable, default, primary key), constraints, and indexes (with their access method).
- When a database cannot be read (not connected, or a query error), the page shows the reason and a retry button instead of data.

#### SQL Query Explorer

- Runs one read-only statement starting with `SELECT`, `WITH` or `SHOW`. Multiple statements are rejected.
- Statements containing data or schema changes (`INSERT`, `UPDATE`, `DELETE`, `DROP`, `ALTER`, `CREATE`, `TRUNCATE`, `GRANT`, `COPY`, and similar) are rejected, as are dangerous server functions such as `pg_terminate_backend`, `pg_read_file`, `pg_advisory_lock`, `lo_import` and `dblink`.
- Every viewer query runs inside a `READ ONLY` transaction that is always rolled back, with `statement_timeout` set to `QUERY_TIMEOUT_MS`.
- Results are capped at `QUERY_ROW_LIMIT` rows and marked as truncated when more rows exist.
- For a plain single-table `SELECT`, the result also shows that table's schema.
- The **Copy** button copies the displayed rows as tab-separated text, ready to paste into a spreadsheet.

#### Migrations

A migration is a named SQL script (SQL Up, and SQL Down for rollback) for one target database. Its status moves through
`DRAFT → PENDING_REVIEW → APPROVED → QUEUED → RUNNING → SUCCESS | FAILED`, plus `PARTIAL` and `ABORTED` for batch migrations. Once it is `SUCCESS`, `FAILED` or `ABORTED` the row never changes again; `ROLLED_BACK` and `PARTIALLY_REVERTED` are not stored but derived from the history (see below).

- **Drafts** (View & Edit): create, edit, delete, and duplicate. Names are up to 200 characters and scripts are up to 1 MiB. Duplicating copies any migration into a new draft.
- **Reversibility**: a draft can be saved without SQL Down, but it can only be **submitted for review or executed** when it has a SQL Down, or is marked irreversible with a written reason. The editor shows what is missing under the field and keeps both buttons disabled until then; the server checks again (`422`). Rollback and forward requests are exempt, since their scripts are copied from the executed migration. In batch mode a `DELETE` must be irreversible: deleted rows cannot be brought back by an `UPDATE`/`DELETE` SQL Down.
- **Script checks**: the platform wraps every script in one transaction, so statements that cannot run inside it are rejected before anything runs: transaction control (`BEGIN`, `COMMIT`, `ROLLBACK`, ...), `VACUUM`, `DISCARD`, `CREATE`/`DROP DATABASE` or `TABLESPACE`, `ALTER SYSTEM`, the `CONCURRENTLY` forms of index commands, and `COPY ... FROM STDIN / TO STDOUT`. `SAVEPOINT`, `RELEASE` and `ROLLBACK TO SAVEPOINT` are allowed.
- **Execute**:
  - The whole script runs in one transaction: **COMMIT** when every statement succeeds, **ROLLBACK** otherwise.
  - Each statement is bounded by `MIGRATION_LOCK_TIMEOUT_MS` and `MIGRATION_STATEMENT_TIMEOUT_MS`.
  - A successful run gets the next **version number** of its database (1, 2, 3, ...). A failed run is kept in the history without a version, with the error message and the line, column and statement where it failed.
  - Session settings changed by a committed script (`SET search_path`, `SET ROLE`, ...) are reset before the connection goes back to the pool.
- **Rollback and forward**: rollback runs the SQL Down of the **latest active** migration only (LIFO); forward re-applies the earliest rolled-back migration as a new run with a new version (linear). Irreversible migrations cannot be rolled back. When groups have access to the database, both are requests that need the same leader approvals as a migration (see below).
- **Hide rollback**: the migration page can hide each rolled-back execution together with the rollback that undid it. Nothing is deleted (see [Execution History and Audit](#history-and-audit)).

#### Review and Approval

Every change to a database must be approved by the **leader of every group that has access to it** (View Only or View & Edit), because every team using the database is affected. This covers migrations, rollbacks, forwards, and the resume and abort actions of batch migrations.

- **Leaders**: every group has one leader, who must be an active member. The first member of a new group becomes its leader; an admin can change it. When a leader leaves the group or is deactivated, the earliest active member takes over (for Public, the earliest active Platform Admin).
- **Leaderless groups**: an admin cannot give database access to a group that has no active leader, and cannot remove the leader of a group that has access. If a group still ends up without one (for example, its last active member was deactivated), the startup log warns `groups.leaderless_with_grants`, the admin console flags the group, and new reviews on its databases are refused with the group's name until a leader is assigned. Nothing gets stuck waiting for an approval nobody can give.
- **Review**: the author submits a draft (`PENDING_REVIEW`). One approval per group with access is opened. Each leader approves or rejects (a reason is required) for their group. When every group approved, the migration is `APPROVED` and an editor can execute it; one rejection makes it `REJECTED`. When no group has access, a submitted draft is approved at once.
- **Rollback and forward** create a request (a rollback or forward draft with the original, unchangeable scripts) that goes through the same review.
- **Batch actions**: resume, abort with rollback, and mark as stopped of a `PARTIAL` batch migration each open a **batch action request** with one approval per group. The monitor on the detail page shows the request and its approvals; leaders approve it there or on **Persetujuan Migrasi** (`/migration/reviews`). Once it is approved, an editor runs it with the same button. One request per migration can be open at a time; a rejected request is final and a new one can be made. An approved request that was not run is cancelled when a request for another action replaces it.
- **Fail closed**: if the groups with access cannot be read (for example, the platform database is unavailable), nothing is executed.
- **Grant changes**: when an admin gives a group access, takes it away, or deletes a group, open reviews follow at once (the same sync also runs at startup):
  - a group that **lost** access no longer blocks: its pending approval is dropped, and a review that has nothing pending left becomes `APPROVED`;
  - a group that **got** access must approve too: it gets a pending approval, and an `APPROVED` migration or batch request that has not run yet goes back to review.
- **Revalidation**: a job can wait in the queue while grants change, so the worker checks again right after it takes the database lock, before any SQL is sent:
  - the executor must still have View & Edit on the database;
  - every group that has access **now** must have approved this run: the migration's own review, or for a resume/abort the batch request the job runs. A direct rollback/forward was queued without approval because no group had access then, so it is refused if one has now.

  If a check fails, nothing reaches the target: the attempt is recorded with `sql_sent = false`, the job is `FAILED` with the reason, and the log has `migration.revalidation_failed`. A migration that a new group has to review goes back to `PENDING_REVIEW` with a pending approval for that group (the approvals already given are kept). Otherwise it returns to the status it had before it was queued (`APPROVED`, `PARTIAL`), so an editor who still has access can run it, or a new request can be made.
- Platform Admins see every pending review but approve only for groups they lead.

#### Asynchronous Execution

- `POST .../execute` (and a direct rollback/forward) stores a job in `migration_jobs` in the same transaction that moves the migration to `QUEUED`, then answers `202 Accepted` with the job. The SPA polls `GET /api/jobs/{id}` or the migration status until the job is done.
- A pool of workers in the app claims jobs with `FOR UPDATE SKIP LOCKED`. Only the oldest queued job of a database can start, and only while no other job of that database is running, so one database runs one migration at a time in queue order while different databases run in parallel. An advisory lock taken while claiming closes the race between two workers.
- A worker holds a **lease** on its job and renews it while the job runs (heartbeat). If the process dies, the lease expires and the job is recovered on the next start:
  - no SQL was sent yet: the job goes back to the queue;
  - the outcome is already in the history: the job is closed as `SUCCESS` without running again;
  - SQL may have been sent but no outcome was recorded: the job becomes **`UNCERTAIN`** and holds every later job of that database until someone checks the target.
- **Retry** only happens for errors before any SQL reaches the target (target unavailable, broken connection), up to 3 attempts with exponential backoff. A failing script is a final `FAILED` result and is never retried. A busy database puts the job back without using an attempt.
- **No double commit**: before running, the worker checks whether an execution of the same job already sent SQL and finished. A job is never run twice, even when it is delivered again after a crash.
- On `SIGINT`/`SIGTERM` (Ctrl+C, `docker stop`) the server stops taking requests and jobs, waits for running jobs to finish, then exits.
- **Lock recovery**: the lock of a database is a PostgreSQL **session** advisory lock (`pg_try_advisory_lock`), taken on a connection to the platform database that the run keeps for its whole duration. It is not a row in a table, so it cannot be left behind: PostgreSQL releases it when that session ends.
  - The worker process exits or crashes: the OS closes its sockets, the session ends and the lock is free at once.
  - The worker's host disappears without closing the connection (power loss, network partition): the server only notices through TCP keepalive. The Compose file sets `tcp_keepalives_idle=60`, `tcp_keepalives_interval=10` and `tcp_keepalives_count=6` on both databases, so that takes about 2 minutes instead of the Linux default of about 2 h 11 min. The same settings make the target roll back the dead worker's open transaction and release its row locks.
  - Meanwhile the lease (30 s) has already expired and the reaper has settled the job as described above. A job of that database that runs before the lock is free cannot get it within `MIGRATION_QUEUE_TIMEOUT_MS`; it goes back to the queue without using an attempt and is tried again until the lock is released. Nothing runs on the database while the old session could still be alive.

#### Per-Batch Execution

A migration can run in **batch mode** (*Mode Eksekusi → Per-batch* in the editor) with a cursor column (`batch_attribute`, an integer key such as `id`) and a batch size (1–100,000 rows, default 1,000). This is for changes to large tables: one transaction per batch keeps every transaction and its locks short, and a run that stops can continue where it stopped.

- **Script**: exactly one `UPDATE` or `DELETE` on one table, written as for a normal run (no `WITH`, `RETURNING`, `ORDER BY`, `LIMIT`). SQL Down, if any, follows the same rule. Both are checked when the draft is saved. A `DELETE` must be marked irreversible (see [Migrations](#migrations)).
- **How a batch runs**: the platform adds the key range to the statement itself. For `UPDATE orders SET status = 'ARCHIVED' WHERE status = 'CANCELLED'` with cursor `id`, every batch runs
  ```sql
  UPDATE orders SET status = 'ARCHIVED' WHERE (status = 'CANCELLED')
    AND ($1::bigint IS NULL OR orders.id > $1) AND orders.id <= $2::bigint
  ```
  where `$1` is the previous batch's upper bound and `$2` the largest `id` among the next `batch_size` keys (`WHERE id > $1 ORDER BY id LIMIT n`).
- **Checkpoints**: each batch writes its checkpoint (batch number, key range, rows changed) to `_dbplatform_batch_checkpoints` **on the target, in the same transaction as its data change**, then commits. A batch is either committed with its checkpoint or not at all. The platform keeps a copy in `migration_batches` and re-reads the target's checkpoints at the start of every run, so a batch that committed just before a crash is never run again. The checkpoint table is hidden from the Database Viewer.
- **Lock**: the database's lock is held for the whole run, across all batches, and the worker's heartbeat keeps the lease. Every batch has its own lock and statement timeouts.
- **When a batch fails** (an SQL error), its transaction is rolled back and the run stops: the migration is **`PARTIAL`** (or `FAILED` if no batch was ever committed). The monitor on the detail page shows committed/total batches, the checkpoint cursor and the error. A `PARTIAL` migration **holds every other migration of its database** until it is finished or aborted. With the approval of every group's leader, an editor can then:
  - **Resume** (*Lanjutkan dari Checkpoint*) runs the remaining batches, starting after the last checkpoint;
  - **Abort with rollback** (*Batalkan & Rollback Batch*) runs SQL Down over the same key ranges, **newest batch first**. When every batch is undone the migration is `ABORTED`; if it stops early it reads as **`PARTIALLY_REVERTED`** and can only be aborted again;
  - **Mark as stopped** (*Tandai Dihentikan*) sets `ABORTED` and leaves the committed batches in place.
- **Recovery data**: before an UP batch of an `UPDATE` changes its rows, it stores a copy of them (the *before-image*) in `_dbplatform_batch_rows` on the target, in the batch's own transaction. Irreversible migrations store nothing.
- **Abort safety**: an abort with rollback is refused when a committed batch has no recovery data. Each DOWN batch commits only when the rows are back exactly as they were before the migration; otherwise that batch is rolled back and the abort stops as **unsafe**. A batch is never left half-reverted. Recovery rows are deleted when the migration is final.

  How the before-images are captured, checked and cleaned up is in [docs/batch-mode.md](docs/batch-mode.md).
- **Restarts are safe**: on `SIGTERM` a batch run stops at the next batch boundary and its job goes back to the queue without using an attempt; when a worker dies mid-run, the job is put back in the queue instead of becoming `UNCERTAIN`. In both cases the next run continues after the last checkpoint.
- Every run (first run, resume, abort) is one execution in the history (`PARTIAL` when it stopped with committed batches); the batches it committed point at that execution.

**Large dataset.** `make seed-large` creates `events` (2,000,000 rows, 173 MB) on the demo target. On one laptop, `UPDATE events SET status = 'ARCHIVED'` took **6.9 s** as one statement, holding row locks on every row. In batch mode with `batch_size` 10,000 it took 20.5 s with recovery data (7.7 s without), and no single transaction ran longer than 145 ms. The full measurements and their analysis are in [docs/batch-mode.md](docs/batch-mode.md#large-dataset).

#### History and Audit

- Every attempt to run a migration is one row in **`migration_executions`**: execution ID, attempt, job ID, the exact SQL sent, executor, status (`SUCCESS`, `FAILED`, `UNCERTAIN`), error and SQLSTATE, and `reverts_execution_id`. The migration detail page lists them under **Riwayat Eksekusi**.
- Attempts that stopped **before any SQL reached the target** are recorded too, with `sql_sent = false`. For example, three attempts while the target is down give three rows.
- The table is **append-only**: a trigger rejects every `UPDATE` and `DELETE`. The only exceptions are the executor becoming `NULL` when a user is deleted, and rows removed together with their target database. A draft that already has attempts cannot be deleted.
- **Rollbacks** do not rewrite history. A rollback adds a `ROLLBACK` execution whose `reverts_execution_id` points at the execution it undid; a forward adds a `FORWARD` execution that reverts that rollback. A migration is shown as `ROLLED_BACK` when such a rollback exists (view `migration_chain`), so the original row stays exactly as it was.
- The migration row and its execution row are written in **one transaction**, so the history never has one without the other.
- **Audit events** record who did what and with which outcome (`success`, `failure`, `denied`, `uncertain`), as `audit.<action>` log records with `actor` and `correlation_id`:

  | Event | Written when |
  |---|---|
  | `audit.submit`, `audit.approve`, `audit.reject` | A draft is sent for review, approved or rejected; `audit.approve`/`audit.reject` with `batch_request_id` for a batch action request |
  | `audit.enqueue` | Execute, rollback, forward, resume or abort is requested (`request`, plus `job_id` or `requires_approval`; `batch_request_id` for batch actions) |
  | `audit.grant` | A group's access level on a database changes (`group_id`, `database_id`, `access_level`) |
  | `audit.execute`, `audit.rollback`, `audit.forward` | An execution is recorded (`execution_id`, `attempt`, `sql_sent`, `reverts_execution_id`); outcome `partial` for a batch run that stopped |
  | `audit.abort` | A `PARTIAL` batch migration is marked `ABORTED` without rollback. Requesting resume, abort or mark-stopped is `audit.enqueue` (`request: resume`, `abort`, `abort_mark`) |

  Audit events never contain SQL; it is in the execution history, found through `execution_id`.

Inspecting the history in the platform database:

```sql
-- every attempt on one database, oldest first
SELECT left(e.id::text, 8) AS exec, m.name, e.execution_type, e.attempt, e.status, e.sql_sent,
       left(e.reverts_execution_id::text, 8) AS reverts, left(e.error_message, 40) AS error
FROM migration_executions e JOIN migrations m ON m.id = e.migration_id
WHERE e.database_id = '<database id>'
ORDER BY e.finished_at;
```

#### Dry Run

- A dry run executes the draft on the target inside a transaction that is **always rolled back**, so the data and schema are left unchanged.
- Statements run one at a time. DML statements (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, ...) run through `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`, so each one shows its plan tree with node types, estimated and actual rows, cost, actual time, loops and buffer hits/reads. Each plan sees the schema built by the statements before it, for example an index created two statements earlier.
- A failing dry run reports which statement failed and why.
- Non-transactional effects, such as sequence values consumed by `nextval`, are not undone by the rollback.

#### Limitations

- Only PostgreSQL targets are supported.
- If the app process dies after a migration commits on the target but before its history is saved, the job becomes `UNCERTAIN` and holds that database. There is no UI to resolve it yet: check the target, then update the job (and, for a run that did commit, the migration) in the platform database.
- Batch mode supports one `UPDATE` or `DELETE` with an integer cursor column. Rows inserted beyond the last key while a run is in progress are included; keys below the checkpoint that appear later are not. Rolling back a **completed** batch migration (`SUCCESS`) runs SQL Down in one transaction, like a standard migration.
- Batch mode creates `_dbplatform_batch_checkpoints` and `_dbplatform_batch_rows` in the target's default schema; the platform's database role must be allowed to create tables there.
- The recovery data takes about as much space as the rows the batches changed (as JSON), until the migration is final; see the measurements in [docs/batch-mode.md](docs/batch-mode.md#large-dataset).
- The abort check compares whole rows. A column that the application changes on its own (an `updated_at` set by a trigger, a counter) after the migration makes that batch unsafe to undo, even if SQL Down is correct; such a migration can only be marked as stopped.

### Activity Logging

Every log record is one JSON object on stdout, written by one `log/slog` logger (`internal/logging`).

- **Common fields**: `time`, `level`, `event` (a stable name such as `sql.result`), `component` (`http`, `dbmanager`, `migration`, `worker`), `correlation_id`, `actor` (user ID), `trace_id` and `span_id` (see [Tracing](#tracing)), and `database_id` and `status` where they apply.
- **Correlation ID**: every request gets an ID, returned in the `X-Request-ID` response header. A well-formed `X-Request-ID` from the client (`[A-Za-z0-9._-]`, at most 64 characters) is reused. Every record written while handling the request carries it.
- **SQL**: every execution on a target is logged as a `sql.start` and `sql.result` pair, with `sql_kind`:
  - Query Explorer queries (`read_only`), table browsing (`table_browse`), schema lookups (`catalog`);
  - migration scripts (`migration`) and dry-run statements (`dry_run`, one pair per statement);
  - in batch mode: the key range of the next batch (`batch_bounds`), the batch statement itself (`migration_batch`), the estimate of the batches left (`batch_estimate`), checkpoint reads and writes (`checkpoint`), and the recovery data: storing the before-images (`recovery_capture`), the check after SQL Down (`recovery_verify`), and deleting them (`recovery_release` per batch, `recovery_purge` when the migration is final). Each has `batch_no` when it belongs to a batch.

  `sql.result` has `duration_ms`, `status`, `rows` when known, and on failure `error` and the PostgreSQL `sqlstate`. Bind parameters are listed by type only (`params`), never by value.
- **Transactions** on a target log `tx.commit` or `tx.rollback`: once per migration run, once per batch in batch mode, and a `tx.rollback` with `reason: dry_run` at the end of every dry run. A run of a migration, rollback or forward ends with one `migration.attempt` record (`execution_type`, `execution_id`, `version`, `status`).
- **Jobs** log `job.claimed`, `job.finished`, `job.retry_scheduled` (with `delay_ms` and `counts_as_attempt`), `job.uncertain`, `worker.reaped`, and `migration.revalidation_failed` when a run is refused under the lock (see [Review and Approval](#review-and-approval)). A job keeps the `correlation_id` and `actor` of the request that queued it, so the worker's records join the HTTP request's.
- **Batches** log `migration.batch` (direction, `batch_no`, `cursor_from`, `cursor_to`, `rows`, `duration_ms`), `migration.batch_failed`, and `migration.batch_recovered` when checkpoints are copied from the target after a crash.
- **Audit** events are `audit.*` (see **Execution History and Audit**).
- **Other events**: `http.request` (access log; `WARN` for 4xx, `ERROR` for 5xx), `authz.denied`, `authz.error`, `viewer.error`, `migration.error`, and startup events such as `server.started`.
- **Redaction**: attributes whose name contains `password`, `passwd`, `secret`, `token`, `session`, `cookie`, `authorization`, `dsn` or `credential` are written as `[REDACTED]`. Bind parameter values are never logged, only their types. Query text is cut at 2000 characters.
- **Masked parameters**: migration scripts never use bind parameters; they are logged in full and stored in the history, so every run can be reproduced. Bind parameters carry either the platform's own values (IDs, batch bounds), which are already separate fields in the same records, or Database Viewer input, which can be personal or secret. Logging only their types loses nothing needed for debugging.

Example: a read-only query (long IDs shortened). The `jq` commands for reading the logs, and examples of a failing query, a denied access, a migration run, a failed run, a rollback and a retry while the target is down, are in [docs/logging-and-tracing.md](docs/logging-and-tracing.md#logs).

```json
{"time":"2026-11-26T06:55:10.438974839Z","level":"INFO","event":"sql.start","component":"dbmanager","database_id":"4ea8b6a0-…","sql_kind":"read_only","query":"SELECT 1","correlation_id":"readme-q1","actor":"0e966767-…"}
{"time":"2026-11-26T06:55:10.439979881Z","level":"INFO","event":"sql.result","component":"dbmanager","database_id":"4ea8b6a0-…","sql_kind":"read_only","query":"SELECT 1","duration_ms":0,"status":"SUCCESS","rows":1,"correlation_id":"readme-q1","actor":"0e966767-…"}
{"time":"2026-11-26T06:55:10.440056923Z","level":"INFO","event":"http.request","component":"http","method":"POST","path":"/api/viewer/query","status":200,"duration_ms":5,"correlation_id":"readme-q1","actor":"0e966767-…"}
```

### Tracing

Every API request and every migration job is traced with **OpenTelemetry** (`internal/tracing`). One trace follows a migration from the request that queues it, through the queue and the worker, to the lock, every batch, and every SQL statement on the target.

```text
POST /api/migration/{id}/execute     server span (otelhttp), named by route
└─ job.enqueue                       producer: INSERT into migration_jobs; its traceparent is stored on the job
   └─ job.process                    consumer: the worker's attempt (one span per attempt, retries included)
      ├─ migration.lock_wait         wait for the database's advisory lock (lock.tries, lock.acquired)
      ├─ migration.run               the script's transaction (events tx.commit / tx.rollback)
      │  └─ sql migration            the statement on the target
      └─ migration.batch × N         batch mode: one span per batch (batch_no, cursor_from/to, rows, tx.commit)
         ├─ sql batch_bounds
         ├─ sql migration_batch
         └─ sql checkpoint
```

- **Through the queue**: the job row keeps the W3C `traceparent` of its `job.enqueue` span (`migration_jobs.trace_parent`). The worker that claims the job, possibly much later and after a restart, starts `job.process` as its child. A retry is another `job.process` span in the same trace, and a job recovered after its lease expired gets a `job.recover` span there too.
- **Other spans**: dry runs (`migration.dry_run` with one `sql dry_run` per statement), viewer queries (`sql read_only`, `sql table_browse`, `sql catalog`) under their HTTP span.
- **Attributes**: route, status and `correlation_id` on the server span; `job_id`, `job_type`, `migration_id`, `database_id`, `attempt`, `worker_id`, `job.queue_wait_ms` and `job.status` on `job.process`; `db.system`, `db.operation`, `db.statement` (cut at 2000 characters, no parameter values), `sql_kind`, rows and `db.response.status_code` (SQLSTATE) on SQL spans.
- **Errors**: a failing statement marks its SQL span, `migration.run` and `job.process` as `Error`, with the message and an `exception` event.
- **Logs**: every record written inside a span carries its `trace_id` and `span_id`, so `sql.start`/`sql.result` share the SQL span's ID and `job.claimed`/`job.finished` the `job.process` span's.
- Only `/api/*` requests are traced. The API is public, so a `traceparent` header sent by a client is recorded as a link, not trusted as the parent. Every trace is sampled.
- **Output**: with `OTEL_TRACES_EXPORTER=console` (the default) each finished span is one JSON line on stdout, next to the logs. Spans are written in batches every few seconds, and the rest are flushed on shutdown.

The spans of one executed migration (name, span ID, parent span ID, status). The `jq` commands, the raw span, the log records of the same trace, and a failing and a batch run are in [docs/logging-and-tracing.md](docs/logging-and-tracing.md#traces).

```json
["POST /api/migration/{id}/execute","dd0a201e800a2260","0000000000000000","Unset"]
["job.enqueue","f1b426a30e579215","dd0a201e800a2260","Unset"]
["job.process","6733661cfcf76d95","f1b426a30e579215","Unset"]
["migration.lock_wait","3c6d2770cfe3a832","6733661cfcf76d95","Unset"]
["migration.run","c0ad35b2b3b2d411","6733661cfcf76d95","Unset"]
["sql migration","cb073128265e78cd","c0ad35b2b3b2d411","Unset"]
```

### Observability

`make observability` starts the normal stack plus the `observability` Compose profile:

```mermaid
flowchart LR
    app["Go app"] -- "traces and logs<br/>(OTLP/HTTP)" --> collector["OpenTelemetry<br/>Collector"]
    collector -- traces --> tempo[("Tempo")]
    collector -- logs --> loki[("Loki")]
    prometheus[("Prometheus")] -- "scrapes :9091/metrics<br/>every 5 s" --> app
    grafana["Grafana<br/>:3000"] --> prometheus & tempo & loki
```

- **Grafana**: <http://localhost:3000> (anonymous visitors can view; sign in as `admin`/`admin` to edit). The home dashboard is **DB Platform**. Prometheus is also on <http://localhost:9090> for raw queries. Both are published on `127.0.0.1` only; the collector, Tempo, Loki and the app's `:9091` are only reachable inside the Compose network.
- **Traces**: `OTEL_TRACES_EXPORTER=otlp` sends the spans described in [Tracing](#tracing) to the collector, which forwards them to Tempo.
- **Logs**: `OTEL_LOGS_EXPORTER=otlp` also sends every log record, already redacted, to Loki through the `otelslog` bridge; stdout is unchanged. In Loki the event name is the log line, the attributes and `trace_id`/`span_id` are structured metadata, and the only stream label is `service_name`.
- **Metrics** (`internal/metrics`, Prometheus client on `METRICS_ADDR`): jobs by status, queue throughput, retries, queue and lock wait, migration and batch duration, batches and rows, HTTP requests by route, and connection pool stats per pool. Labels never contain user, job or migration IDs, raw paths or SQL.
- **Correlation in Grafana**: a log line links to its trace in Tempo, a span links to its logs in Loki, and the dashboard shows recent errors and `job.process` traces next to the metrics.
- **Without the profile** nothing changes: `OTEL_TRACES_EXPORTER` stays `console`, `OTEL_LOGS_EXPORTER` stays `none`, and `/metrics` is still served on `:9091` inside the network (set `METRICS_ADDR=off` to turn it off).

Every metric, the correlation setup and example PromQL/LogQL queries are in [docs/observability.md](docs/observability.md).

### Stress Test (k6)

`loadtest/stress.js` stress-tests the running stack with [k6](https://k6.io). It goes through Caddy (`https://localhost`), like a browser. Two runners start it:

| Runner | Databases | For |
|---|---|---|
| `make loadtest` (`loadtest/run.sh`) | the demo database plus `loadtest_a`, `loadtest_b`, created on `target-postgres` | anyone: it needs nothing but this repository |
| `make loadtest-local` (`loadtest/local-run.sh`) | the demo database plus our private local databases `airport`, `bioskop` and `sales` | the team only: they are not in the repository |

Both take the same settings (`MIGRATIONS_PER_DB`, `JOB_VU_RATIO`, `INCLUDE_DEMO`, `RUN_NAME`; `LOCAL_DBS` for the local runner).

**Setup.** `setup()` works only through the public API, as an admin and the group leaders would:
- creates the user `k6runner` and the group **k6 Load Test**, led by `k6runner`;
- registers the databases as targets and grants the group View & Edit on each;
- prepares a pool of **approved** migrations per database. Each is submitted and approved by the leader of every group with access; on the demo database that includes `akmal` and `dzaki`.

Every migration is a one-row `UPDATE` on a real table, and its SQL Down is the exact inverse:

| Database | API browses | A job changes |
|---|---|---|
| PostgreSQL Demo | `orders` | `products.stock ± 1` |
| `airport` | `booking` (72,000 rows) | `booking.price ± 1` |
| `bioskop` | `_reservasi` (102,628 rows) | `_film.durasi ± 1` |
| `sales` | `orders` (50,000 rows, partitioned by month) | `customers.updated_at ± 1 s` |
| `loadtest_*` | `k6_counters` | `k6_counters.n ± 1` |

**Scenarios.** Both run at the same time with the same VU shape: ramp for 30 s, then hold for 30 s at 10, 20, 40 and 80 VUs (80 is held for 60 s), then down to 10 and to 0. That is 6 minutes in total.

| Scenario | VUs | One iteration |
|---|---|---|
| `api` | 10 → 20 → 40 → 80 → 10 → 0 | What a user does on the pages: `GET /api/auth/me`, the viewer (table list, then a random page of the database's table), the migration list, one migration's detail. Then 0.5–1.5 s of think time |
| `jobs` | 25 % of that: 3 → 5 → 10 → 20 → 3 → 0 | Takes the next approved migration of the pool and sends `POST .../execute`. Then it polls `GET /api/jobs/{id}` every 200 ms until the job is done and pauses 1 s. A job counts as a success only when it ends `SUCCESS`; the `202` of the execute request does not count |

**Normal load.** We set the platform's normal load at **10–20 VUs**: about the whole team plus reviewers working at once, with 3–5 migrations running at the same time. 40 and 80 VUs are the stress levels above it, and the last 10-VU hold shows whether the system recovers.

**Thresholds.** Every metric is tagged with the stage and the database, so the summary has a line per stage and per database.

| Metric | Threshold | Meaning |
|---|---|---|
| `http_req_duration{scenario:api}` p95 | < 500 ms, overall and per stage | API response time (p50/p95/p99 are reported) |
| `http_req_failed` | < 1 % | error rate of every request, jobs scenario included |
| `job_success` | > 99 % | jobs that ended `SUCCESS` |
| `http_reqs` | (reported) | request throughput, overall and per stage |
| `job_completion_ms` | (reported) | from the execute request until the job is seen done |
| `job_queue_wait_ms` | (reported) | the job's `started_at - created_at`, from the server's own timestamps: queue plus lock wait |

**Results** (2026-11-27, `make loadtest-local` on one laptop; k6, Caddy, the app, every PostgreSQL server and the observability stack shared the same CPUs):

| | `stress-01`: demo + airport + bioskop + sales | `stress-02`: airport only |
|---|---|---|
| Migrations prepared | 700 per database | 1,800 |
| HTTP requests | 61,817 (151.4/s), **0.00 % failed** | 49,104 (130.1/s), **0.00 % failed** |
| API p50 / p95 / p99 | 32 / **223** / 348 ms | 59 / **585** / 892 ms |
| Jobs | **1,973 SUCCESS, 0 failed** | **1,139 SUCCESS, 0 failed** |
| Job completion p50 / p95 | 482 ms / 1.0 s | 448 ms / 9.1 s |
| Job queue wait p50 / p95 | 321 ms / 630 ms | 325 ms / 8.5 s |
| Thresholds | **all passed** | API p95 crossed from the ramp to 80 VUs on (so also overall) |

Per load level (API p95, jobs completed in the stage, job completion p95, job queue wait p95):

| Stage | 01: API p95 | 01: jobs | 01: job p95 | 01: queue p95 | 02: API p95 | 02: jobs | 02: job p95 | 02: queue p95 |
|---|---|---|---|---|---|---|---|---|
| hold 10 VUs (normal) | 29 ms | 63 | 0.64 s | 0.49 s | 76 ms | 62 | 0.65 s | 0.48 s |
| hold 20 VUs (normal) | 32 ms | 104 | 0.65 s | 0.50 s | 96 ms | 113 | 0.65 s | 0.44 s |
| hold 40 VUs | 63 ms | 219 | 0.67 s | 0.46 s | 163 ms | 212 | 0.73 s | 0.53 s |
| ramp to 80 VUs | 182 ms | 287 | 1.10 s | 0.70 s | **586 ms** | 135 | 9.9 s | 9.3 s |
| hold 80 VUs (60 s) | 303 ms | **703** | 1.12 s | 0.76 s | **812 ms** | **123** | 11.4 s | 10.9 s |
| down to 10 VUs | 167 ms | 245 | 0.80 s | 0.51 s | 487 ms | 138 | 5.5 s | 5.0 s |
| hold 10 VUs again | 41 ms | 59 | 0.64 s | 0.50 s | 86 ms | 60 | 0.65 s | 0.50 s |

In `stress-01` the four databases came out even: 493–494 jobs each, job p95 1.0 s and queue wait p95 0.61–0.65 s on every one.

**Server side at the 80-VU peak** (Prometheus):

| | `stress-01` | `stress-02` |
|---|---|---|
| Requests/s | 288 | 221 |
| Jobs finished/s | 11.5 | 2.1 |
| Migration run p95 (engine, lock wait included) | 0.25 s | 0.48 s |
| App CPU | 2.1 cores | 2.4 cores |
| Platform pool in use (no waits) | 39 | 60 |
| Target pool waits | none to speak of | `airport`: 39 ms per second |
| Slowest route p95 | `GET /api/migration` 0.47 s | `GET /api/migration` 1.1 s |

**Analysis.**
- **Normal load is comfortable.** At 10–20 VUs the API p95 stays at 29–96 ms, and a job completes in about 0.65 s (p95), most of it the worker's poll interval and the queue.
- **The system holds up and recovers.** Neither run had a failed request or a failed job: 3,112 jobs in total, none `UNCERTAIN`, and no retries (the Grafana retry panel stays empty). Thirty seconds after the load drops back to 10 VUs, every number is back to its normal-load value, including the queue that built up in `stress-02`.
- **Jobs scale with databases, not workers.** Up to 40 VUs both runs finish the same number of jobs, because the load is still below capacity. At 80 VUs, `stress-01` finishes 703 jobs in the minute (11.5/s, spread evenly over four databases) and `stress-02` only 123 (2.1/s).

  This is the platform's rule: one database runs one migration at a time, in queue order, under its advisory lock. With four databases, the 4 workers run four migrations in parallel. With one, they take turns on it, so jobs pile up in the queue (queue wait p95 10.9 s) while the throughput stays at the rate of a single database.
- **The API slows where the work concentrates.** In `stress-01` the API stays under its target at every level (p95 303 ms at 80 VUs). In `stress-02` it passes 500 ms from 80 VUs. The slowest route in both runs is `GET /api/migration`: it returns every draft and history row of the database, and the single database of `stress-02` holds 1,800 drafts against 700 per database in `stress-01`.

  `stress-02` also puts every query on one target pool. Its 5 connections are shared by the viewer and the migrations, so they start waiting (39 ms per second), and more platform connections are busy at the same time (60 against 39).
- **What to change first:** page the migration list instead of returning every row, and make the per-target pool size (fixed at 5 connections in `internal/dbmanager/targets.go`) configurable for a database that gets most of the traffic. The one-migration-per-database rule is intended and stays: it is what keeps versions ordered and runs safe.
- These numbers come from one laptop that also ran k6 and the observability stack. Compare the runs with each other and the stages with each other, rather than reading the absolute numbers as production capacity.

To reproduce: `make observability`, then `make loadtest`. `stress-01` was `make loadtest-local` and `stress-02` was `LOCAL_DBS=airport INCLUDE_DEMO=false MIGRATIONS_PER_DB=1800 make loadtest-local`. The raw k6 summaries (`loadtest/results/*.json`) are git-ignored.

### Testing and CI

- **Unit tests** (`make test`) need no database and replace dependencies with stubs or in-memory stores: access rules, sessions, the script splitter and checks, the read-only guard, batch planning, config parsing, and log redaction and escaping.
- **Integration tests** (`make test-integration`) run real components against a disposable PostgreSQL (Compose profile `test`, port 5433, data on tmpfs), through the HTTP handlers or the services. They cover every access level and action (`internal/http/authz_matrix_test.go`), the approval workflow and revalidation, the job queue and worker (retries, leases, uncertain outcomes, no double commit), batch mode (resume, abort, unsafe abort), and the history, logging and tracing.
- Tests cover success, failure and access denial. On 2026-11-27, 327 tests (including subtests) passed with `-race`, none skipped.
- **CI** ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs on pushes to `main` and `dev` and on pull requests: frontend build, `gofmt`, `go vet`, `go build`, every test with the race detector against a PostgreSQL service, and a Docker image build.

### SAST (CodeQL)

- **CodeQL** ([.github/workflows/codeql.yml](.github/workflows/codeql.yml)) scans the Go and TypeScript code with the `security-extended` queries on every push and pull request to `main` and `dev`. Results go to GitHub Code Scanning, the Actions job summary and a SARIF artifact.
- The Milestone 2 rescan found nothing in the frontend. Of the 9 Go findings, 8 are false positives (log injection, but every log is JSON with escaped newlines) and 1 is an accepted risk (the SQL Query Explorer runs user queries by design).
- Every finding with its decision, the scan history and the limits of SAST: [docs/SAST-CodeQL.md](docs/SAST-CodeQL.md).

## Credits

**IF3110 Web Application Development**  
*Semester 1 2026/2027*

Group K03-01: 
| Nama                        | NIM      | Username                                         |
|-----------------------------|----------|--------------------------------------------------|
| Dzaki Ahmad Al Hussainy     | 13524084 | [@HussainDzaki](https://github.com/HussainDzaki) |
| Moreno Syawali Ganda Sugita | 13524096 | [@DeSince29](https://github.com/DeSince29)       |
| Muhammad Akmal              | 13524099 | [@m-akma1](https://github.com/m-akma1)           |

**Program Studi Teknik Informatika**  
**Sekolah Teknik Elektro dan Informatika**  
**Institut Teknologi Bandung**
