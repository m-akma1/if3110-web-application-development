# IF3110 Web Application Development Project 

## Overview

### Milestone 1

In this milestone, we implemented a database web platform where a team can work with several databases from one place, without handing out raw database credentials.

- A **Platform Admin** registers target PostgreSQL databases once. Their credentials are stored encrypted and are never shown to anyone again.
- Users get access through **groups**. Each group has an access level per database: no access, **View Only**, or **View & Edit**.
- **View Only** users browse tables, schemas and data, run read-only queries, and read the migration history.
- **View & Edit** users can also write migration scripts, test them with a dry run (including query plans), and execute them. Every execution runs in a single transaction and is recorded in an immutable history.

## Build and Run

### Requirements

- Docker with Docker Compose v2
- `openssl` (to generate secrets)
- For local development only: Go 1.26 and Node.js 22

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

Run `npm --prefix frontend install` once before using the frontend targets. The Go server serves the built SPA from `frontend/dist`, so run `make frontend-build` at least once before `make dev`.

`make dev` reads its configuration from the environment, so export the `.env` variables first. The Compose databases do not publish host ports, so point `POSTGRES_HOST` and `TARGET_POSTGRES_HOST` at PostgreSQL servers reachable from your machine. The session and CSRF cookies are `Secure` and accepted by browsers on `http://localhost`.

### Environment Variables

All variables are **required** unless marked optional. Copy `.env.example` to `.env` and replace every `change-me-*` value before deploying. For required variables, the value column shows the `.env.example` value. For optional variables, it shows the default used when the variable is unset.

#### Application Internal Database

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

#### Data Viewer Query Limits

| Variable | Default Value | Description |
|---|---|---|
| `QUERY_TIMEOUT_MS` | `5000` | (Optional) Maximum execution time in milliseconds for each read-only statement: Query Explorer queries and table browsing (row count, page of rows, schema lookups) |
| `QUERY_ROW_LIMIT` | `500` | (Optional) Maximum number of rows returned by a read-only query |

#### Migration Execution Limits

| Variable | Default Value | Description |
|---|---|---|
| `MIGRATION_LOCK_TIMEOUT_MS` | `5000` | (Optional) Maximum time in milliseconds to wait for a database lock |
| `MIGRATION_STATEMENT_TIMEOUT_MS` | `60000` | (Optional) Maximum execution time in milliseconds for a single migration statement |
| `MIGRATION_QUEUE_TIMEOUT_MS` | `15000` | (Optional) Maximum time in milliseconds a worker waits for a database's migration lock. When it expires the job goes back to the queue without using an attempt |

The resume/abort buttons of batch migrations can be hidden with the frontend build argument `VITE_ENABLE_BATCH_ACTIONS=false` (default `true`).

The worker pool itself uses fixed settings: 4 workers, a 30 s lease renewed every 10 s, at most 3 attempts per job, retry delay 2 s doubling up to 1 min, and a lease check every 10 s (`migration.DefaultWorkerConfig`).

#### Logging

| Variable | Default Value | Description |
|---|---|---|
| `LOG_LEVEL` | `info` | (Optional) Minimum log level: `debug`, `info`, `warn` or `error` |

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
| Logging | `log/slog` structured JSON logs on stdout (one logger, request correlation ID, redaction) |
| Background jobs | Job queue in a PostgreSQL table (`FOR UPDATE SKIP LOCKED`), worker pool inside the app process |
| History and audit | Append-only `migration_executions` table (enforced by a trigger) and `audit.*` log events |
| Reverse proxy and TLS | Caddy 2 |
| Deployment | Docker multi-stage build (Node -> Go -> Alpine), Docker Compose |
| CI and security | GitHub Actions (build, vet, test), CodeQL SAST |

### Architectural Diagram

```mermaid
flowchart LR
    subgraph browser["Browser"]
        spa["React SPA<br/>(React Router, fetch API client)"]
    end

    subgraph compose["Docker Compose network (app_net)"]
        caddy["Caddy<br/>TLS on :443 and :80"]

        subgraph app["Go app (:8080)"]
            static["SPA host<br/>frontend/dist + index.html fallback"]
            http["internal/http<br/>REST /api/*, CSRF guard, sanitized logger"]
            authn["authn<br/>users, sessions"]
            rbac["rbac<br/>access decisions"]
            admin["admin<br/>console services"]
            groups["groups<br/>groups, leaders, invite codes"]
            viewer["dbmanager<br/>pools, read-only viewer"]
            migr["migration<br/>drafts, dry run, approvals,<br/>engine, rollback/forward,<br/>execution history"]
            worker["migration worker pool<br/>queue, lease, retry"]
            audit["audit<br/>audit.* events"]
        end

        platform[("postgres<br/>platform DB")]
        target[("target-postgres<br/>demo target DB")]
    end

    other[("Other registered<br/>PostgreSQL targets")]

    spa -- "HTTPS (HTML/JS assets)" --> caddy
    spa -- "HTTPS JSON /api/*" --> caddy
    caddy -- HTTP --> static
    caddy -- HTTP --> http
    http --> authn & rbac & admin & groups & viewer & migr
    http -- "enqueue job (202)" --> platform
    worker -- "claim job" --> platform
    worker --> migr
    http & migr & worker --> audit
    authn & rbac & admin & groups & migr --> platform
    viewer & migr --> target
    viewer & migr --> other
```

- The browser loads the SPA once (`index.html` + `/assets/*`). Every page change after that is handled by React Router on the client, and data comes from the JSON API under `/api/*`.
- The **platform database** stores users, sessions, groups, grants, invite codes, registered targets (with sealed passwords), migrations, the **job queue** (`migration_jobs`) and the **execution history** (`migration_executions`). It is never exposed as a viewer target.
- Execute, rollback and forward requests only **enqueue a job** and answer `202 Accepted`. A worker in the same process runs the job later; the SPA polls until it is done.
- **Target databases** are only reached through the connection registry, which holds one small pool (up to 5 connections) per connected target. The Data Viewer, the target manager and the migration engine share it.
- Every request that touches a target first identifies the user (`authn`), then asks `rbac.Authorizer` whether the action is allowed. Handlers never compare access levels themselves.

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
│   ├── logging/                 # Structured JSON logger (slog), correlation ID and actor in context, SQL log helpers
│   ├── ids/                     # UUID helpers
│   ├── accesstest/, testutil/   # Test fixtures and test database helpers
├── frontend/                    # React + TypeScript SPA (Vite)
│   ├── src/api/                 # fetch client (session cookie + CSRF header)
│   ├── src/context/             # Auth context (current user, CSRF token)
│   ├── src/pages/               # Login, signup, dashboard (viewer), migration, reviews, admin
│   ├── src/components/          # Layout, sidebar, data grid, dry-run report, migration and admin parts
│   ├── src/types/, src/utils/   # Shared TypeScript types and helpers
│   └── public/static/           # Images copied into the build
├── static/                      # Images served under /static
├── docs/                        # Extra documentation (CodeQL SAST report)
├── docker/target-init/          # Init scripts for the demo target database
├── docker/seed/                 # Optional large dataset for batch mode (make seed-large)
├── Dockerfile                   # SPA build (Node) -> Go build -> Alpine runtime
├── docker-compose.yml           # caddy, app, postgres, target-postgres, postgres-test (profile "test")
├── Caddyfile                    # TLS, reverse proxy, log filtering
├── Makefile                     # Developer shortcuts
├── .github/workflows/codeql.yml # CodeQL SAST
└── .github/workflows/ci.yml     # CI pipeline
```

### Routes

#### SPA pages (client-side routes)

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
| `GET /api/migration/pending-reviews` | Group Leader | Lists migrations waiting for review |
| `POST /api/migration/{id}/approve`, `POST /api/migration/{id}/reject` | Group Leader | Approves or rejects for the leader's group |
| `POST /api/migration/{id}/execute` | View & Edit | Queues an approved migration: `202` with the job, `409` when it is already queued |
| `POST /api/migration/{id}/rollback` | View & Edit | Rollback of the latest active migration: a request for approval (`200`), or a queued job (`202`) when no group needs to approve |
| `POST /api/migration/{id}/forward` | View & Edit | Forward of a rolled-back migration: a request for approval (`200`), or a queued job (`202`) |
| `GET /api/migration/{id}/status` | View Only | Migration with its newest job, for polling |
| `GET /api/migration/{id}/executions` | View Only | Every execution attempt of the migration, oldest first |
| `GET /api/jobs/{id}` | View Only | One job, for polling after a `202` |
| `GET /api/migration/{id}/batches` | View Only | Committed batches of a batch migration (`UP`, and `DOWN` for undone ones) |
| `POST /api/migration/{id}/resume` | View & Edit | Continues a `PARTIAL` batch migration from its checkpoint (`202`) |
| `POST /api/migration/{id}/abort` | View & Edit | `{"rollback": true}` undoes the committed batches (`202`); `{"rollback": false}` marks it `ABORTED` (`200`) |
| `POST /api/migration/{id}/duplicate` | View & Edit | Copies a migration into a new draft |
| `GET /api/admin/overview` and `/api/admin/*` | Platform Admin | Admin console data and actions |

Database pages and API calls take the selected database as `?db_id=`.

## Features and Behavior

### Access Control

#### Authentication

- **Sign up** creates a `USER` account. Fields: username (3-32 characters of `a-z`, `0-9`, `.`, `_`, `-`, case-insensitive), optional email, password (8-128 characters), and an optional invite code. A valid, active invite code also adds the new user to that code's group. The user is logged in right after signing up.
- **Log in** accepts a username or an email (an identifier containing `@` is matched against emails). Deactivated accounts cannot log in. Wrong usernames and wrong passwords get the same error message.
- Passwords are hashed with **Argon2id** (64 MiB memory, 1 iteration, 4 threads, 16-byte salt). A dummy hash is verified when the account does not exist, so response time does not reveal which usernames exist.
- Login and signup reject requests that put the password in the URL query string.
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
- **Logging**: the access log records method, path, status and duration only. Query parameters whose names look like passwords, sessions or tokens are redacted. Caddy removes `Cookie`, `Authorization` and `Set-Cookie` headers from its access logs. See [Activity Logging](#activity-logging).

#### Admin Platform

The admin console (`/admin`) is only available to Platform Admins and has four tabs:

- **Users**: create users (with role and groups), change roles, deactivate or reactivate accounts, and reset passwords. An admin cannot demote or deactivate their own account.
- **Groups**: create groups, edit name and description, add and remove members, delete groups, and set each group's access level per database (`NONE`, `VIEW_ONLY`, `VIEW_EDIT`). Deleting a group also removes its memberships, grants and invite codes.
- **Databases**: register target PostgreSQL databases (display name, host, port, database name, username, password, SSL mode `disable`/`require`/`verify-full`).
  - The connection is tested on save. A target that fails the test is still saved with status `ERROR` and the reason, so the admin can fix it.
  - Editing with an empty password keeps the stored one. **Test connection** reconnects and updates the status.
  - Deleting a target also removes its grants and migration history.
  - Passwords are encrypted with **AES-256-GCM** using `TARGET_CREDENTIALS_KEY`, with the target's id bound as additional data. They are never displayed, and connection errors are shown with the password redacted.
- **Invite codes**: create a code for a group (custom, or a random one like `JOIN-7K2Q-MX4P`), and activate or deactivate it. Codes are 4-64 characters of `A-Z`, `0-9`, `_` or `-`, and case-insensitive.

#### Access Management

- **Public** is a built-in system group. Every user joins it automatically when the account is created, and cannot leave it. Public cannot be renamed or deleted, and it cannot have invite codes. It has no database access unless an admin grants it.
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
- **Script checks**: the platform wraps every script in one transaction, so statements that cannot run inside it are rejected before anything runs: transaction control (`BEGIN`, `COMMIT`, `ROLLBACK`, ...), `VACUUM`, `DISCARD`, `CREATE`/`DROP DATABASE` or `TABLESPACE`, `ALTER SYSTEM`, the `CONCURRENTLY` forms of index commands, and `COPY ... FROM STDIN / TO STDOUT`. `SAVEPOINT`, `RELEASE` and `ROLLBACK TO SAVEPOINT` are allowed.
- **Execute**:
  - The whole script runs in one transaction: **COMMIT** when every statement succeeds, **ROLLBACK** otherwise.
  - Each statement is bounded by `MIGRATION_LOCK_TIMEOUT_MS` and `MIGRATION_STATEMENT_TIMEOUT_MS`.
  - Executions on the same database run one at a time, in the order they were queued; different databases run in parallel (see **Asynchronous Execution**).
  - A successful run gets the next **version number** of its database (1, 2, 3, ...). A failed run is kept in the history without a version, with the error message and the line, column and statement where it failed.
  - Session settings changed by a committed script (`SET search_path`, `SET ROLE`, ...) are reset before the connection goes back to the pool.
- **Rollback and forward**: rollback runs the SQL Down of the **latest active** migration only (LIFO); forward re-applies the earliest rolled-back migration as a new run with a new version (linear). Irreversible migrations cannot be rolled back. When groups have access to the database, both are requests that need the same leader approvals as a migration.
- **History**: every run with version, status, executor, time and duration. A database trigger blocks any change to an executed migration. The migration page can hide rollback pairs.

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

#### Per-Batch Execution

A migration can run in **batch mode** (*Mode Eksekusi → Per-batch* in the editor) with a cursor column (`batch_attribute`, an integer key such as `id`) and a batch size (1–100,000 rows, default 1,000). This is for changes to large tables: one transaction per batch keeps every transaction and its locks short, and a run that stops can continue where it stopped.

- **Script**: exactly one `UPDATE` or `DELETE` on one table, written as for a normal run (no `WITH`, `RETURNING`, `ORDER BY`, `LIMIT`). SQL Down, if any, follows the same rule. Both are checked when the draft is saved.
- **How a batch runs**: the platform adds the key range to the statement itself. For `UPDATE orders SET status = 'ARCHIVED' WHERE status = 'CANCELLED'` with cursor `id`, every batch runs
  ```sql
  UPDATE orders SET status = 'ARCHIVED' WHERE (status = 'CANCELLED')
    AND ($1::bigint IS NULL OR orders.id > $1) AND orders.id <= $2::bigint
  ```
  where `$1` is the previous batch's upper bound and `$2` the largest `id` among the next `batch_size` keys (`WHERE id > $1 ORDER BY id LIMIT n`).
- **Checkpoint in the same transaction**: each batch writes its checkpoint (batch number, key range, rows changed) to `_dbplatform_batch_checkpoints` **on the target, in the same transaction as its data change**, then commits. A batch is either committed with its checkpoint or not at all. The platform keeps a copy in `migration_batches` and re-reads the target's checkpoints at the start of every run, so a batch that committed just before a crash is never run again. The checkpoint table is hidden from the Database Viewer.
- **The database's lock is held for the whole run**, across all batches, and the worker's heartbeat keeps the lease. Every batch has its own lock and statement timeouts.
- **When a batch fails** (an SQL error), its transaction is rolled back and the run stops: the migration is **`PARTIAL`** (or `FAILED` if no batch was ever committed). The monitor on the detail page shows committed/total batches, the checkpoint cursor and the error. A `PARTIAL` migration **holds every other migration of its database** until it is finished or aborted. Then:
  - **Resume** (*Lanjutkan dari Checkpoint*) runs the remaining batches, starting after the last checkpoint; no new approval is needed;
  - **Abort with rollback** (*Batalkan & Rollback Batch*) runs SQL Down over the same key ranges, **newest batch first**. When every batch is undone the migration is `ABORTED`; if it stops early it reads as **`PARTIALLY_REVERTED`** and can only be aborted again;
  - **Mark as stopped** (*Tandai Dihentikan*) sets `ABORTED` and leaves the committed batches in place.
- **Restarts are safe**: on `SIGTERM` a batch run stops at the next batch boundary and its job goes back to the queue without using an attempt; when a worker dies mid-run, the job is put back in the queue instead of becoming `UNCERTAIN`. In both cases the next run continues after the last checkpoint.
- Every run (first run, resume, abort) is one execution in the history (`PARTIAL` when it stopped with committed batches); the batches it committed point at that execution.

**Large dataset.** `make seed-large` creates `events` (2,000,000 rows, 173 MB) on the demo target, owned by the app role. Measured once on a laptop against the Docker test database (data on tmpfs), so treat the numbers as rough:

| Run | Total | Longest single transaction |
|---|---|---|
| `UPDATE events SET status = 'ARCHIVED'` in one statement | 30–49 s | the whole statement (30–49 s) |
| Batch mode, `batch_size` 10,000 (200 batches) | 40.0 s | 621 ms (average 146 ms) |
| Batch mode, `batch_size` 50,000 (40 batches) | 25.9 s | 1.6 s (average 583 ms) |

The single statement is already close to the default `MIGRATION_STATEMENT_TIMEOUT_MS` (60 s), and it holds row locks on all 2,000,000 rows until it commits. In batch mode the total time is similar, but no transaction lasts more than a couple of seconds, and progress is never lost.

#### Execution History and Audit

- Every attempt to run a migration is one row in **`migration_executions`**: execution ID, attempt, job ID, the exact SQL sent, executor, status (`SUCCESS`, `FAILED`, `UNCERTAIN`), error and SQLSTATE, and `reverts_execution_id`. The migration detail page lists them under **Riwayat Eksekusi**.
- Attempts that stopped **before any SQL reached the target** are recorded too, with `sql_sent = false`. For example, three attempts while the target is down give three rows.
- The table is **append-only**: a trigger rejects every `UPDATE` and `DELETE`. The only exceptions are the executor becoming `NULL` when a user is deleted, and rows removed together with their target database. A draft that already has attempts cannot be deleted.
- **Rollback does not rewrite history.** It adds a `ROLLBACK` execution whose `reverts_execution_id` points at the execution it undid; a forward adds a `FORWARD` execution that reverts that rollback. A migration is shown as `ROLLED_BACK` when such a rollback exists (view `migration_chain`), so the original row stays exactly as it was.
- The migration row and its execution row are written in **one transaction**, so the history never has one without the other.
- **Audit events** record who did what and with which outcome (`success`, `failure`, `denied`, `uncertain`), as `audit.<action>` log records with `actor` and `correlation_id`:

  | Event | Written when |
  |---|---|
  | `audit.submit`, `audit.approve`, `audit.reject` | A draft is sent for review, approved or rejected |
  | `audit.enqueue` | Execute, rollback or forward is requested (`request`, plus `job_id` or `requires_approval`) |
  | `audit.grant` | A group's access level on a database changes (`group_id`, `database_id`, `access_level`) |
  | `audit.execute`, `audit.rollback`, `audit.forward` | An execution is recorded (`execution_id`, `attempt`, `sql_sent`, `reverts_execution_id`); outcome `partial` for a batch run that stopped |
  | `audit.abort` | A `PARTIAL` batch migration is marked `ABORTED` without rollback (resume and abort with rollback are `audit.enqueue` with `request: resume / abort`) |

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

#### Dry Run and Query Plans

- A dry run executes the draft on the target inside a transaction that is **always rolled back**, so the data and schema are left unchanged.
- Statements run one at a time. DML statements (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, ...) run through `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`, so each one shows its plan tree with node types, estimated and actual rows, cost, actual time, loops and buffer hits/reads. Each plan sees the schema built by the statements before it, for example an index created two statements earlier.
- A failing dry run reports which statement failed and why.
- Non-transactional effects, such as sequence values consumed by `nextval`, are not undone by the rollback.

#### Limitations

- Only PostgreSQL targets are supported.
- If the app process dies after a migration commits on the target but before its history is saved, the job becomes `UNCERTAIN` and holds that database. There is no UI to resolve it yet: check the target, then update the job (and, for a run that did commit, the migration) in the platform database.
- Worker settings (number of workers, lease, attempts, backoff) are fixed in code and cannot be set through environment variables yet.
- Batch mode supports one `UPDATE` or `DELETE` with an integer cursor column. Rows inserted beyond the last key while a run is in progress are included; keys below the checkpoint that appear later are not. Rolling back a **completed** batch migration (`SUCCESS`) runs SQL Down in one transaction, like a standard migration.
- Batch mode creates `_dbplatform_batch_checkpoints` in the target's default schema; the platform's database role must be allowed to create tables there.

### Activity Logging

Every log record is one JSON object on stdout, written by one `log/slog` logger (`internal/logging`).

- **Common fields**: `time`, `level`, `event` (a stable name such as `sql.result`), `component` (`http`, `dbmanager`, `migration`, `worker`), `correlation_id`, `actor` (user ID), and `database_id` and `status` where they apply.
- **Correlation ID**: every request gets an ID, returned in the `X-Request-ID` response header. A well-formed `X-Request-ID` from the client (`[A-Za-z0-9._-]`, at most 64 characters) is reused. Every record written while handling the request carries it.
- **Every SQL execution is logged** as a `sql.start` and `sql.result` pair: Query Explorer queries (`sql_kind: read_only`), table browsing (`table_browse`), schema lookups (`catalog`) and migration scripts (`migration`). `sql.result` has `duration_ms`, `status`, `rows` when known, and on failure `error` and the PostgreSQL `sqlstate`.
- **Transactions that change data** also log `tx.commit` or `tx.rollback`. A run of a migration, rollback or forward ends with one `migration.attempt` record (`execution_type`, `execution_id`, `version`, `status`).
- **Jobs** log `job.claimed`, `job.finished`, `job.retry_scheduled` (with `delay_ms` and `counts_as_attempt`), `job.uncertain` and `worker.reaped`. A job keeps the `correlation_id` and `actor` of the request that queued it, so the worker's records join the HTTP request's.
- **Batches** log `migration.batch` (direction, `batch_no`, `cursor_from`, `cursor_to`, `rows`, `duration_ms`), `migration.batch_failed`, and `migration.batch_recovered` when checkpoints are copied from the target after a crash.
- **Audit** events are `audit.*` (see **Execution History and Audit**).
- **Other events**: `http.request` (access log; `WARN` for 4xx, `ERROR` for 5xx), `authz.denied`, `authz.error`, `viewer.error`, `migration.error`, and startup events such as `server.started`.
- **Redaction**: attributes whose name contains `password`, `secret`, `token`, `session`, `cookie`, `authorization`, `dsn` or `credential` are written as `[REDACTED]`. Bind parameter values are never logged, only their types. Query text is cut at 2000 characters.

Reading the logs:

```sh
# every record of one request (X-Request-ID from the browser's DevTools)
docker compose logs --no-log-prefix app | jq -R -c 'fromjson? | select(.correlation_id=="<id>")'
# failed SQL
docker compose logs --no-log-prefix app | jq -R -c 'fromjson? | select(.event=="sql.result" and .status=="FAILED")'
# audit trail
docker compose logs --no-log-prefix app | jq -R -c 'fromjson? | select(.event | startswith("audit."))'
# one job from claim to finish
docker compose logs --no-log-prefix app | jq -R -c 'fromjson? | select(.job_id=="<job id>")'
```

Example output (one block per request; long IDs shortened). A read-only query:

```json
{"time":"2026-11-26T06:55:10.438974839Z","level":"INFO","event":"sql.start","component":"dbmanager","database_id":"4ea8b6a0-…","sql_kind":"read_only","query":"SELECT 1","correlation_id":"readme-q1","actor":"0e966767-…"}
{"time":"2026-11-26T06:55:10.439979881Z","level":"INFO","event":"sql.result","component":"dbmanager","database_id":"4ea8b6a0-…","sql_kind":"read_only","query":"SELECT 1","duration_ms":0,"status":"SUCCESS","rows":1,"correlation_id":"readme-q1","actor":"0e966767-…"}
{"time":"2026-11-26T06:55:10.440056923Z","level":"INFO","event":"http.request","component":"http","method":"POST","path":"/api/viewer/query","status":200,"duration_ms":5,"correlation_id":"readme-q1","actor":"0e966767-…"}
```

A failing query:

```json
{"time":"2026-11-26T06:55:10.476767964Z","level":"ERROR","event":"sql.result","component":"dbmanager","database_id":"4ea8b6a0-…","sql_kind":"read_only","query":"SELECT * FROM tidak_ada","duration_ms":3,"status":"FAILED","error":"ERROR: relation \"tidak_ada\" does not exist (SQLSTATE 42P01)","sqlstate":"42P01","correlation_id":"readme-q2","actor":"0e966767-…"}
{"time":"2026-11-26T06:55:10.476863589Z","level":"WARN","event":"http.request","component":"http","method":"POST","path":"/api/viewer/query","status":422,"duration_ms":6,"correlation_id":"readme-q2","actor":"0e966767-…"}
```

Access to a database the user has no grant on:

```json
{"time":"2026-11-26T06:55:10.533319339Z","level":"WARN","event":"authz.denied","component":"http","database_id":"00000000-0000-0000-0000-000000000000","action":"database:view_schema","status":"DENIED","correlation_id":"readme-deny","actor":"0e966767-…"}
{"time":"2026-11-26T06:55:10.533437673Z","level":"WARN","event":"http.request","component":"http","method":"GET","path":"/api/viewer?db_id=00000000-0000-0000-0000-000000000000","status":403,"duration_ms":6,"correlation_id":"readme-deny","actor":"0e966767-…"}
```

The migration examples below are records from an integration test run (`time` left out; in the test the HTTP records have no `component`, in the app they carry `"component":"http"`).

A group gets edit access:

```json
{"level":"INFO","event":"audit.grant","action":"grant","outcome":"success","group_id":"ebda6aac-…","database_id":"37491ac9-…","access_level":"VIEW_EDIT","correlation_id":"afb88795-…","actor":"894a4741-…"}
```

A migration is queued over HTTP, then run by a worker. Note the same `correlation_id` from the request to `job.finished`, and the execution ID shared by `migration.attempt` and `audit.execute`:

```json
{"level":"INFO","event":"audit.enqueue","action":"enqueue","outcome":"success","migration_id":"e3777042-…","database_id":"37491ac9-…","status":"QUEUED","request":"execute","job_id":"985f0362-…","requires_approval":false,"correlation_id":"fc63554d-…","actor":"492d74cb-…"}
{"level":"INFO","event":"job.claimed","component":"worker","job_id":"985f0362-…","migration_id":"e3777042-…","database_id":"37491ac9-…","job_type":"EXECUTE","attempt":1,"worker_id":"w1","correlation_id":"fc63554d-…","actor":"492d74cb-…"}
{"level":"INFO","event":"sql.start","component":"migration","database_id":"37491ac9-…","sql_kind":"migration","query":"CREATE TABLE orders (id int PRIMARY KEY)","migration_id":"e3777042-…","execution_type":"UP","correlation_id":"fc63554d-…","actor":"492d74cb-…"}
{"level":"INFO","event":"sql.result","component":"migration","database_id":"37491ac9-…","sql_kind":"migration","query":"CREATE TABLE orders (id int PRIMARY KEY)","migration_id":"e3777042-…","execution_type":"UP","duration_ms":1,"status":"SUCCESS","correlation_id":"fc63554d-…","actor":"492d74cb-…"}
{"level":"INFO","event":"tx.commit","component":"migration","database_id":"37491ac9-…","migration_id":"e3777042-…","execution_type":"UP","correlation_id":"fc63554d-…","actor":"492d74cb-…"}
{"level":"INFO","event":"migration.attempt","component":"migration","execution_type":"UP","migration_id":"e3777042-…","execution_id":"16f18003-…","database_id":"37491ac9-…","version":1,"status":"SUCCESS","duration_ms":8,"query":"CREATE TABLE orders (id int PRIMARY KEY)","correlation_id":"fc63554d-…","actor":"492d74cb-…"}
{"level":"INFO","event":"audit.execute","component":"migration","action":"execute","outcome":"success","execution_id":"16f18003-…","migration_id":"e3777042-…","database_id":"37491ac9-…","attempt":1,"sql_sent":true,"job_id":"985f0362-…","correlation_id":"fc63554d-…","actor":"492d74cb-…"}
{"level":"INFO","event":"job.finished","component":"worker","job_id":"985f0362-…","migration_id":"e3777042-…","database_id":"37491ac9-…","job_type":"EXECUTE","attempt":1,"worker_id":"w1","status":"SUCCESS","correlation_id":"fc63554d-…","actor":"492d74cb-…"}
```

A migration that fails and is rolled back:

```json
{"level":"ERROR","event":"sql.result","component":"migration","database_id":"37491ac9-…","sql_kind":"migration","query":"INSERT INTO missing VALUES (1)","migration_id":"2b6fd488-…","execution_type":"UP","duration_ms":0,"status":"FAILED","error":"ERROR: relation \"missing\" does not exist (SQLSTATE 42P01)","sqlstate":"42P01","correlation_id":"ae6e465e-…","actor":"492d74cb-…"}
{"level":"WARN","event":"tx.rollback","component":"migration","database_id":"37491ac9-…","migration_id":"2b6fd488-…","execution_type":"UP","error":"ERROR: relation \"missing\" does not exist (SQLSTATE 42P01)","correlation_id":"ae6e465e-…","actor":"492d74cb-…"}
{"level":"ERROR","event":"migration.attempt","component":"migration","execution_type":"UP","migration_id":"2b6fd488-…","execution_id":"abca4e2e-…","database_id":"37491ac9-…","version":0,"status":"FAILED","duration_ms":1,"query":"INSERT INTO missing VALUES (1)","error":"ERROR 42P01: relation \"missing\" does not exist (statement 1, baris 1, kolom 13)","sqlstate":"42P01","correlation_id":"ae6e465e-…","actor":"492d74cb-…"}
{"level":"WARN","event":"audit.execute","component":"migration","action":"execute","outcome":"failure","execution_id":"abca4e2e-…","migration_id":"2b6fd488-…","database_id":"37491ac9-…","attempt":1,"sql_sent":true,"job_id":"cf784c11-…","error":"ERROR 42P01: relation \"missing\" does not exist (statement 1, baris 1, kolom 13)","correlation_id":"ae6e465e-…","actor":"492d74cb-…"}
{"level":"WARN","event":"job.finished","component":"worker","job_id":"cf784c11-…","migration_id":"2b6fd488-…","database_id":"37491ac9-…","job_type":"EXECUTE","attempt":1,"worker_id":"w1","status":"FAILED","error":"ERROR 42P01: relation \"missing\" does not exist (statement 1, baris 1, kolom 13)","correlation_id":"ae6e465e-…","actor":"492d74cb-…"}
```

A rollback: `audit.rollback` points at the rollback row (`migration_id`) and at the execution it undid (`reverts_execution_id`, the `execution_id` of the run above):

```json
{"level":"INFO","event":"migration.attempt","component":"migration","execution_type":"ROLLBACK","migration_id":"e3777042-…","execution_id":"cab7f42c-…","database_id":"37491ac9-…","version":1,"status":"SUCCESS","duration_ms":3,"query":"DROP TABLE orders","correlation_id":"ee0558e1-…","actor":"492d74cb-…"}
{"level":"INFO","event":"audit.rollback","component":"migration","action":"rollback","outcome":"success","execution_id":"cab7f42c-…","migration_id":"29011a19-…","database_id":"37491ac9-…","attempt":1,"sql_sent":true,"job_id":"876b21a1-…","reverts_execution_id":"16f18003-…","correlation_id":"ee0558e1-…","actor":"492d74cb-…"}
{"level":"INFO","event":"job.finished","component":"worker","job_id":"876b21a1-…","migration_id":"e3777042-…","database_id":"37491ac9-…","job_type":"ROLLBACK","attempt":1,"worker_id":"w1","status":"SUCCESS","correlation_id":"ee0558e1-…","actor":"492d74cb-…"}
```

The target is down: every attempt is recorded with `sql_sent: false` and retried with backoff (the test used a 10 ms base delay; the app uses 2 s), and the job fails after the third attempt:

```json
{"level":"WARN","event":"audit.execute","component":"worker","action":"execute","outcome":"failure","execution_id":"459628dd-…","migration_id":"4110053e-…","database_id":"37491ac9-…","attempt":1,"sql_sent":false,"job_id":"9bf8298a-…","error":"koneksi ke database target tidak tersedia","correlation_id":"69a08b0e-…","actor":"492d74cb-…"}
{"level":"WARN","event":"job.retry_scheduled","component":"worker","job_id":"9bf8298a-…","migration_id":"4110053e-…","database_id":"37491ac9-…","job_type":"EXECUTE","attempt":1,"worker_id":"w1","delay_ms":10,"counts_as_attempt":true,"error":"koneksi ke database target tidak tersedia","correlation_id":"69a08b0e-…","actor":"492d74cb-…"}
{"level":"WARN","event":"audit.execute","component":"worker","action":"execute","outcome":"failure","execution_id":"bd92f643-…","migration_id":"4110053e-…","database_id":"37491ac9-…","attempt":2,"sql_sent":false,"job_id":"9bf8298a-…","error":"koneksi ke database target tidak tersedia","correlation_id":"69a08b0e-…","actor":"492d74cb-…"}
{"level":"WARN","event":"job.retry_scheduled","component":"worker","job_id":"9bf8298a-…","migration_id":"4110053e-…","database_id":"37491ac9-…","job_type":"EXECUTE","attempt":2,"worker_id":"w1","delay_ms":20,"counts_as_attempt":true,"error":"koneksi ke database target tidak tersedia","correlation_id":"69a08b0e-…","actor":"492d74cb-…"}
{"level":"WARN","event":"audit.execute","component":"worker","action":"execute","outcome":"failure","execution_id":"99982564-…","migration_id":"4110053e-…","database_id":"37491ac9-…","attempt":3,"sql_sent":false,"job_id":"9bf8298a-…","error":"koneksi ke database target tidak tersedia","correlation_id":"69a08b0e-…","actor":"492d74cb-…"}
{"level":"WARN","event":"job.finished","component":"worker","job_id":"9bf8298a-…","migration_id":"4110053e-…","database_id":"37491ac9-…","job_type":"EXECUTE","attempt":3,"worker_id":"w1","status":"FAILED","error":"koneksi ke database target tidak tersedia","correlation_id":"69a08b0e-…","actor":"492d74cb-…"}
```

### Testing and CI

- Unit tests run without a database: `make test`.
- Integration tests use a disposable PostgreSQL from the `postgres-test` Compose profile (port 5433 on localhost, data on tmpfs): `make test-integration`.
- GitHub Actions ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs on pushes to `main` and `dev` and on pull requests. It installs and builds the frontend SPA, then runs `gofmt`, `go vet`, `go build`, and every test with the race detector against a PostgreSQL service. A second job builds the Docker image.

### Security: SAST dengan CodeQL

- Static Application Security Testing (SAST) diaktifkan menggunakan **GitHub CodeQL** pada [.github/workflows/codeql.yml](.github/workflows/codeql.yml) untuk menganalisis bahasa `go` (`autobuild`) dan `javascript-typescript` (`build-mode: none`) dengan query suite `security-extended`.
- Hasil analisis diunggah ke **GitHub Code Scanning**, ditampilkan sebagai ringkasan pada **GitHub Actions Step Summary**, serta disimpan sebagai **SARIF artifact** (`codeql-sarif-go`, `codeql-sarif-javascript-typescript`).
- Dokumentasi lengkap cakupan analisis, audit temuan, mitigasi/perbaikan, dan batasan SAST tersedia pada [docs/SAST-CodeQL.md](docs/SAST-CodeQL.md).

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
