# SAST with CodeQL

## Overview

This document records the static analysis of the repository with GitHub CodeQL: what is scanned, every finding with its decision (fixed, false positive or accepted risk) and the reason, and what SAST cannot catch. In the latest scan nothing is left to fix: the frontend is clean, and the 9 Go alerts are 8 false positives and 1 accepted risk.

### Setup

[`.github/workflows/codeql.yml`](../.github/workflows/codeql.yml) runs CodeQL with the `security-extended` query suite on every push and pull request to `main` and `dev`. Each run:

- uploads its results to **Security → Code scanning** on GitHub;
- writes a findings table to the Actions job summary;
- keeps the SARIF files as artifacts (`codeql-sarif-go`, `codeql-sarif-javascript-typescript`) for 30 days.

| Language | Build mode | Files analysed (from the SARIF) | Rules |
|---|---|---|---|
| `go` | `autobuild` | 115: all 63 application files (`main.go`, `domain/`, `internal/`) and 52 test files | 35 |
| `javascript-typescript` | `none` | 53: the 36 TS/TSX files in `frontend/src/`, frontend config, `loadtest/stress.js`, and the workflow, Compose and observability YAML | 103 |

### Limitation

A clean or explained scan does not prove the application is secure. CodeQL follows data flow for the patterns its rules know, and misses, for example:

- **Migration SQL.** It runs on the target as written, but CodeQL never flags it: the SQL is stored in the platform database before the worker reads it, and CodeQL's default `remote` threat model does not count database reads as user input. The protection here is business logic: leader approval, revalidation under the lock, dry runs, and LIFO rollback.
- **Authorization rules.** CodeQL cannot tell who may do what. The integration tests check this (`internal/http/authz_matrix_test.go`).
- **Deployment configuration**, such as TLS and Caddy, the target database role's privileges, and secrets in `.env`.

## Scan history

| Commit | Go | JS/TS | What changed |
|---|---|---|---|
| `0bc25da` (2026-11-22) | 4 | 0 | First scan |
| `e8a2ac0` (2026-11-22) | 1 | 0 | The two log-injection findings fixed |
| `c0aa875` (2026-11-26) | 8 | 0 | Logging moved to `log/slog` JSON (`a36320b`..`0c5cc90`); CodeQL flags each `slog` call that logs user input |
| `1d88e5f` (2026-11-26) | 9 | 0 | Audit events added (`f91beb6`) |
| `31dc68f` (2026-11-27) | 9 | 0 | Rescan on `main` for Milestone 2 ([run 46025336706](https://github.com/m-akma1/if3110-web-application-development/actions/runs/46025336706)) |

### Findings 2026-11-22 (`0bc25da`)

| Rule | Severity | Location (then) | Impact | Decision |
|---|---|---|---|---|
| `go/log-injection` (CWE-117) | Medium | `internal/http/api_viewer.go:216` (`logViewerError`) | `?db_id=` and error text written with `logger.Printf` (plain text, one line per record), so `\r\n` could forge log lines | **Valid, fixed** in `e8a2ac0` with `stripLogNewlines`; gone in the re-scan |
| `go/log-injection` (CWE-117) | Medium | `internal/http/authz.go:144` (`authorizeDB`) | Same, with the `db_id` of a denied request | **Valid, fixed** in `e8a2ac0`; gone in the re-scan |
| `go/sql-injection` (CWE-89) | High | `internal/dbmanager/query_guard.go:185` | User SQL reaches `tx.QueryContext` | **Accepted risk**, still open as alert #1 (below) |

Hardening done before the first scan, clean in every scan since:

| Issue | Location | Fix |
|---|---|---|
| CR/LF in the logged method and path | `internal/http/middleware_logger.go` | `stripLogNewlines`, with a unit test |
| Column name concatenated into SQL | `internal/migration/repository.go` (`hasPendingActionFor`) | Two static queries |
| Slowloris (CWE-400) | `main.go`, `internal/metrics` | `ReadHeaderTimeout` of 10 s and 5 s |

### Findings 2026-11-27 (`31dc68f`)

| Alert | Rule | Severity | Location | Tainted input | Decision |
|---|---|---|---|---|---|
| #1 | `go/sql-injection` (CWE-89) | High | `internal/dbmanager/query_guard.go:185` (`queryRows`) | `sql_query` of `POST /api/viewer/query` | **Accepted risk** |
| #5, #6 | `go/log-injection` (CWE-117) | Medium | `internal/logging/sql.go:143`, `:151` (`sql.result`) | viewer request body, URL and form values | **False positive** |
| #13 | `go/log-injection` (CWE-117) | Medium | `internal/logging/sql.go:72` (`sql.start`) | same | **False positive** |
| #7, #8 | `go/log-injection` (CWE-117) | Medium | `internal/http/authz.go:150`, `:156` (`authz.denied`, `authz.error`) | `db_id` | **False positive** |
| #9, #10 | `go/log-injection` (CWE-117) | Medium | `internal/http/api_viewer.go:218`, `:219` (`viewer.error`) | `db_id`, error text | **False positive** |
| #12 | `go/log-injection` (CWE-117) | Medium | `internal/audit/audit.go:47` (`audit.Log`) | `group_id`, `database_id`, `level` of `POST /api/admin/grants` | **False positive** |

GitHub shows alert #11 (`internal/logging/sql.go:63`) as fixed, but that is not a real fix. Commit `10ac5d0` moved the same log call, and GitHub reported it again as #13.

## Rationales

### Log injection: false positive

Log injection needs a log written as plain text, one record per line. Since Milestone 2 there is no such log:

- **One JSON logger.** `main.go` builds the only logger with `logging.New`, which uses `slog.NewJSONHandler`, and sets it as the default. No application code logs with `log.Printf` or `fmt.Print*`.
- **The JSON encoder escapes control characters.** CR, LF, ESC and the rest are written as `\r`, `\n` and `\u001b` inside the JSON string. A record is always one line, and a value cannot close its string or add a key.
- **The OTLP export is structured.** With `OTEL_LOGS_EXPORTER=otlp`, records go to Loki as protobuf attributes, not as lines of text.

CodeQL flags every `slog` call as a sink, whatever handler is used. It does not recognise the JSON escaping as a sanitizer.

Stripping newlines, as in Milestone 1, would only make the logs worse. Migration SQL has line breaks and must be logged as it was sent, and JSON escaping keeps it intact.

The test `TestUserInputCannotForgeLogLines` ([`internal/logging/logging_test.go`](../internal/logging/logging_test.go)) is the evidence. It logs a `database_id`, an error, an audit attribute and a multi-line SQL statement, all containing `\r\n`, a fake `{"event":"authz.allowed"}` record and ESC. It then checks four things:

- the four records are exactly four valid JSON lines;
- no forged event appears;
- every value, including the SQL line breaks, decodes back unchanged;
- no raw CR or ESC reaches the output.

The eight alerts are dismissed on GitHub as *False positive*, with a comment pointing here. The rule stays on, so a future plain-text log of user input would still be reported.

### SQL injection: accepted risk

The SQL Query Explorer exists to run the user's read-only queries, so the query cannot be a constant. Several layers protect it:

- **Access check.** `authorizeDB(..., rbac.ActionViewData)` runs before the query is checked or sent.
- **Validation.** `ValidateReadOnlySQL` accepts one statement starting with `SELECT`, `WITH` or `SHOW`. It refuses data and schema changes, and dangerous server functions (`blockedFunctions` in [`sql_scan.go`](../internal/dbmanager/sql_scan.go): `pg_read_file`, `dblink`, `pg_terminate_backend`, advisory locks, ...).
- **Read-only transaction.** `withReadOnlyTx` uses `BeginTx(ReadOnly: true)` and always rolls back, so PostgreSQL itself refuses any write.
- **Limits.** `SET LOCAL statement_timeout` (`QUERY_TIMEOUT_MS`), a client-side deadline, and `QUERY_ROW_LIMIT`.

The alert stays open on purpose, as a reminder that this endpoint takes free-form SQL.
