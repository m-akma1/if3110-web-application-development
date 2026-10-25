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
| `make gen` | Generate templ code (`*_templ.go`) and compile Tailwind CSS (`static/css/style.css`) |
| `make dev` | Watch templ and Tailwind, and run the app with `go run .` |
| `make vet` | Run `go vet` |
| `make test` | Run unit tests (integration tests are skipped without `TEST_DATABASE_URL`) |
| `make test-integration` | Start the disposable test PostgreSQL and run every test against it |
| `make test-db-down` | Stop the test PostgreSQL |

Run `npm ci` once before using the Tailwind targets.

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
| `MIGRATION_QUEUE_TIMEOUT_MS` | `15000` | (Optional) Maximum time in milliseconds a migration can wait in the queue |

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
| Language and server | Go 1.26, standard library `net/http` router |
| Templates | [templ](https://templ.guide) v0.3.1070 (server-rendered HTML) |
| Interactivity | htmx 2.0.11, Alpine.js 3.13.5 (served from `static/js`) |
| Styling | Tailwind CSS v4 (CLI build) |
| Databases | PostgreSQL 16, driver `jackc/pgx/v5` |
| Password hashing | Argon2id (`golang.org/x/crypto`) |
| Reverse proxy and TLS | Caddy 2 |
| Deployment | Docker multi-stage build, Docker Compose |
| CI | GitHub Actions |

### Architectural Diagram

```mermaid
flowchart LR
    browser["Browser<br/>(htmx and Alpine.js)"]

    subgraph compose["Docker Compose network (app_net)"]
        caddy["Caddy<br/>TLS on :443 and :80"]

        subgraph app["Go app (:8080)"]
            http["internal/http<br/>routes, CSRF guard, sanitized logger"]
            authn["authn<br/>users, sessions"]
            rbac["rbac<br/>access decisions"]
            admin["admin<br/>console services"]
            viewer["dbmanager<br/>pools, read-only viewer"]
            migr["migration<br/>drafts, dry run, engine"]
        end

        platform[("postgres<br/>platform DB")]
        target[("target-postgres<br/>demo target DB")]
    end

    other[("Other registered<br/>PostgreSQL targets")]

    browser -- HTTPS --> caddy -- HTTP --> http
    http --> authn & rbac & admin & viewer & migr
    authn & rbac & admin & migr --> platform
    viewer & migr --> target
    viewer & migr --> other
```

- The **platform database** stores users, sessions, groups, grants, invite codes, registered targets (with sealed passwords) and migration history. It is never exposed as a viewer target.
- **Target databases** are only reached through the connection registry, which holds one small pool (up to 5 connections) per connected target. The Data Viewer, the target manager and the migration engine share it.
- Every request that touches a target first identifies the user (`authn`), then asks `rbac.Authorizer` whether the action is allowed. Handlers never compare access levels themselves.

### Repository Structure

```text
.
├── main.go                      # Entry point: config, platform migrations, bootstrap, server
├── domain/                      # View models shared by handlers and templates
├── internal/
│   ├── config/                  # Reads and validates every environment variable
│   ├── platformdb/              # Embedded platform schema migrations (migrations/NNN_*.sql)
│   ├── authn/                   # Users, Argon2id passwords, session lifecycle
│   ├── rbac/                    # Access levels, actions, effective-access rule, grant store
│   ├── groups/                  # Groups, memberships, invite codes
│   ├── admin/                   # Platform Admin console services
│   ├── dbmanager/               # Target registry, credential encryption, read-only guard, viewer queries
│   ├── migration/               # Statement splitter, script checks, engine, EXPLAIN plans, history
│   ├── bootstrap/               # Bootstrap admins and demo data seeding
│   ├── http/                    # Routes, handlers, middleware, cookies
│   ├── ids/                     # UUID helpers
│   ├── accesstest/, testutil/   # Test fixtures and test database helpers
├── views/
│   ├── layouts/                 # Auth and dashboard page shells
│   ├── pages/                   # Login/signup, viewer, migration, admin pages
│   └── components/              # Navbar, sidebar, data grid, query plan, empty/error states
├── assets/css/input.css         # Tailwind source and theme tokens
├── static/                      # Compiled CSS, htmx, Alpine.js, images
├── docker/target-init/          # Init scripts for the demo target database
├── Dockerfile                   # CSS build -> templ generate + Go build -> Alpine runtime
├── docker-compose.yml           # caddy, app, postgres, target-postgres, postgres-test (profile "test")
├── Caddyfile                    # TLS, reverse proxy, log filtering
├── Makefile                     # Developer shortcuts
└── .github/workflows/ci.yml     # CI pipeline
```

### Routes

| Method and path | Access | Description |
|---|---|---|
| `GET /` | Public | Redirects to `/login` |
| `GET /login`, `POST /login` | Public | Login page and form |
| `GET /signup`, `POST /signup` | Public | Signup page and form |
| `POST /logout` | Logged in | Revokes the session |
| `GET /dashboard` | View Only | Database Viewer for the selected database |
| `GET /dashboard/table` | View Only | One page of a table with its schema |
| `POST /dashboard/query` | View Only | Runs a read-only query |
| `GET /migration` | View Only | Migration history (and drafts for View & Edit) |
| `GET /migration/{id}` | View Only | Migration detail |
| `POST /migration/drafts` | View & Edit | Creates a draft |
| `POST /migration/{id}` | View & Edit | Saves a draft |
| `POST /migration/{id}/delete` | View & Edit | Deletes a draft |
| `POST /migration/{id}/dry-run` | View & Edit | Dry-runs a draft |
| `POST /migration/{id}/execute` | View & Edit | Executes a draft |
| `POST /migration/{id}/duplicate` | View & Edit | Copies a migration into a new draft |
| `GET /admin` and `/admin/*` | Platform Admin | Admin console and its actions |
| `GET /api/health` | Public | Liveness check (JSON) |

Database pages take the selected database as `?db_id=`

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
- Expired, revoked or unknown sessions are cleared and redirected to `/login` (htmx requests get an `HX-Redirect`).
- **CSRF protection** uses a double-submit cookie: every `POST`, `PUT`, `PATCH` and `DELETE` must send an `X-CSRF-Token` header that matches the `__Host-csrf` cookie. The pages place the token in a meta tag and htmx sends it automatically.
- **Logging**: the app logs method, path, status and duration only. Query parameters whose names look like passwords, sessions or tokens are redacted. Caddy removes `Cookie`, `Authorization` and `Set-Cookie` headers from its access logs.

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

A migration is a named SQL script for one target database. It is either a **DRAFT** (editable) or an executed run, **SUCCESS** or **FAILED** (immutable).

- **Drafts** (View & Edit): create, edit, delete, and duplicate. Names are up to 200 characters and scripts are up to 1 MiB. Duplicating copies any migration into a new draft.
- **Script checks**: the platform wraps every script in one transaction, so statements that cannot run inside it are rejected before anything runs: transaction control (`BEGIN`, `COMMIT`, `ROLLBACK`, ...), `VACUUM`, `DISCARD`, `CREATE`/`DROP DATABASE` or `TABLESPACE`, `ALTER SYSTEM`, the `CONCURRENTLY` forms of index commands, and `COPY ... FROM STDIN / TO STDOUT`. `SAVEPOINT`, `RELEASE` and `ROLLBACK TO SAVEPOINT` are allowed.
- **Execute**:
  - The whole script runs in one transaction: **COMMIT** when every statement succeeds, **ROLLBACK** otherwise.
  - Each statement is bounded by `MIGRATION_LOCK_TIMEOUT_MS` and `MIGRATION_STATEMENT_TIMEOUT_MS`.
  - Executions on the same database run one at a time, using an advisory lock on the platform database. A migration waits up to `MIGRATION_QUEUE_TIMEOUT_MS` and is then rejected as busy.
  - A successful run gets the next **version number** of its database (1, 2, 3, ...). A failed run is kept in the history without a version, with the error message and the line, column and statement where it failed.
  - Session settings changed by a committed script (`SET search_path`, `SET ROLE`, ...) are reset before the connection goes back to the pool.
- **History**: every executed run with version, status, executor, time and duration. A database trigger blocks any change to an executed migration.

#### Dry Run and Query Plans

- A dry run executes the draft on the target inside a transaction that is **always rolled back**, so the data and schema are left unchanged.
- Statements run one at a time. DML statements (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, ...) run through `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`, so each one shows its plan tree with node types, estimated and actual rows, cost, actual time, loops and buffer hits/reads. Each plan sees the schema built by the statements before it, for example an index created two statements earlier.
- A failing dry run reports which statement failed and why.
- Non-transactional effects, such as sequence values consumed by `nextval`, are not undone by the rollback.

#### Limitations

- Only PostgreSQL targets are supported.
- If the app process dies after a migration commits on the target but before its history is saved, that run is missing from the history.

### Testing and CI

- Unit tests run without a database: `make test`.
- Integration tests use a disposable PostgreSQL from the `postgres-test` Compose profile (port 5433 on localhost, data on tmpfs): `make test-integration`.
- GitHub Actions ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs on pushes to `main` and `dev` and on pull requests. It checks that the generated templ code is committed (error) and that the compiled CSS is up to date (warning), then runs `gofmt`, `go vet`, `go build`, and every test with the race detector against a PostgreSQL service. A second job builds the Docker image.

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
