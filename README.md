# IF3110 Web Application Development Project 

## Overview

### Milestone 1

In this milestone, we implemented a database web platform where a team can works with several databases from one place, without handing out raw database credentials.

## Build and Run

### Steps

1. Generate secrets to fill in environment variables.
  ```sh
  openssl rand -base64 32
  ```
2. Create a `.env` file from the template and copy  the secret to environment variables.
  ```sh
  cp .env.example .env
  # then open the file and fill `change-me` parts with actual secrets 
  ```
3. Build the container and start the server.
  ```sh
  docker compose up --build
  ```
4. Open **[https://localhost](https://localhost)**. Ignore browser warning and proceed. Caddy uses a self-signed internal certificate, so accept the browser warning, or trust Caddy's root CA. The app container is not published on a host port. All traffic goes through Caddy on 80/443.

### Environment Variables

All variables are **required** unless marked optional. Copy `.env.example` to `.env` and replace every `change-me-*` value before deploying.

#### Application Internal Database

| Variable | Default Value | Description |
|---|---|---|
| `POSTGRES_USER` | `if3110` | Username for the application's internal PostgreSQL database |
| `POSTGRES_PASSWORD` | `change-me-postgres-password` | Password for the internal database user |
| `POSTGRES_DB` | `dbplatform_core` | Name of the application's internal database |
| `POSTGRES_HOST` | `postgres` | (Optional) Hostname of the internal database |
| `POSTGRES_PORT` | `5432` | (Optional) Port of the internal database |
| `POSTGRES_SSLMODE` | `disable` | (Optional) SSL mode used to connect to the internal database |

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
| `TARGET_POSTGRES_SSLMODE` | `disable` | (Optional) SSL mode used to connect to the target database |

#### Security

| Variable | Default Value | Description |
|---|---|---|
| `TARGET_CREDENTIALS_KEY` | `change-me-credentials-key` | Encryption key for target database passwords. Generate with `openssl rand -base64 32`. **Do not change once injected**, or existing stored credentials can no longer be decrypted |

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

#### Demo Data

Demo seeding is insert-only. If `SEED_DEMO_DATA=true`, `SEED_DEMO_USERS` and `TARGET_APP_*` are required.

| Variable | Default Value | Description |
|---|---|---|
| `SEED_DEMO_DATA` | `true` | (Optional) Enables seeding of demo data |
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

#### Testing

| Variable | Default Value | Description |
|---|---|---|
| `TEST_DATABASE_URL` | `postgres://test:test@localhost:5433/dbplatform_test?sslmode=disable` | Connection string for the database used by `make test-integration` |

## Project Structure

### Architectural Diagram

### Repository Structure

## Features and Behavior

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