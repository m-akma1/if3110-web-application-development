# Logging and Tracing Examples

Commands for reading the logs and traces, and real output from the platform. The fields, events and span tree are described in the README under [Activity Logging](../README.md#activity-logging) and [Tracing](../README.md#tracing).

## Logs

### Reading the logs

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

### Examples

One block per request; long IDs shortened. A read-only query:

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

## Traces

### Reading a trace

```sh
# trace ID of one request (X-Request-ID from the browser's DevTools)
docker compose logs --no-log-prefix app | jq -R -r 'fromjson? | select(.correlation_id=="<id>" and .event=="http.request") | .trace_id'
# every log record of that trace
docker compose logs --no-log-prefix app | jq -R -c 'fromjson? | select(.trace_id=="<trace id>")'
# its spans as a list: name, span, parent
docker compose logs --no-log-prefix app | jq -R -c 'fromjson? | select(.SpanContext.TraceID=="<trace id>") | [.Name, .SpanContext.SpanID, .Parent.SpanID, .Status.Code]'
```

### Example

A migration approved by both group leaders, then executed (`X-Request-ID: readme-trace-2`). The spans of the trace (name, span ID, parent span ID, status):

```json
["POST /api/migration/{id}/execute","dd0a201e800a2260","0000000000000000","Unset"]
["job.enqueue","f1b426a30e579215","dd0a201e800a2260","Unset"]
["job.process","6733661cfcf76d95","f1b426a30e579215","Unset"]
["migration.lock_wait","3c6d2770cfe3a832","6733661cfcf76d95","Unset"]
["migration.run","c0ad35b2b3b2d411","6733661cfcf76d95","Unset"]
["sql migration","cb073128265e78cd","c0ad35b2b3b2d411","Unset"]
```

The `job.process` span as the console exporter writes it (`Resource` and scope fields left out, attributes flattened for reading). `Parent.Remote: true` is the context read back from the job row:

```json
{"Name":"job.process","SpanContext":{"TraceID":"1f5d052389932c3d11896055d2763300","SpanID":"6733661cfcf76d95"},"Parent":{"TraceID":"1f5d052389932c3d11896055d2763300","SpanID":"f1b426a30e579215","Remote":true},"SpanKind":5,"StartTime":"2026-11-27T09:30:03.157861455+07:00","EndTime":"2026-11-27T09:30:03.168851125+07:00","Attributes":{"job_id":"6b55242d-16cb-40f7-97aa-279fa5494be1","job_type":"EXECUTE","migration_id":"7f96e713-8b88-498e-879e-b7ba980d41bc","database_id":"15a8a362-0dae-4299-8bc3-0770df206823","attempt":1,"worker_id":"worker-3-bbd17602","job.queue_wait_ms":376,"job.status":"SUCCESS"},"Status":{"Code":"Unset","Description":""}}
```

The log records of the same trace (`time` left out, long IDs shortened). The HTTP records share the request span, the worker records share `job.process`, and the SQL records share the SQL span:

```json
{"level":"INFO","event":"audit.enqueue","component":"http","action":"enqueue","outcome":"success","migration_id":"7f96e713-…","database_id":"15a8a362-…","status":"QUEUED","request":"execute","job_id":"6b55242d-…","requires_approval":false,"trace_id":"1f5d052389932c3d11896055d2763300","span_id":"dd0a201e800a2260","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
{"level":"INFO","event":"http.request","component":"http","method":"POST","path":"/api/migration/7f96e713-8b88-498e-879e-b7ba980d41bc/execute","status":202,"duration_ms":4,"trace_id":"1f5d052389932c3d11896055d2763300","span_id":"dd0a201e800a2260","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
{"level":"INFO","event":"job.claimed","component":"worker","job_id":"6b55242d-…","migration_id":"7f96e713-…","database_id":"15a8a362-…","job_type":"EXECUTE","attempt":1,"worker_id":"worker-3-bbd17602","trace_id":"1f5d052389932c3d11896055d2763300","span_id":"6733661cfcf76d95","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
{"level":"INFO","event":"sql.start","component":"migration","database_id":"15a8a362-…","sql_kind":"migration","query":"CREATE TABLE shipments (id bigint PRIMARY KEY, status text NOT NULL);\nINSERT INTO shipments SELECT g, 'NEW' FROM generate_series(1, 3) g;","migration_id":"7f96e713-…","execution_type":"UP","trace_id":"1f5d052389932c3d11896055d2763300","span_id":"cb073128265e78cd","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
{"level":"INFO","event":"sql.result","component":"migration","database_id":"15a8a362-…","sql_kind":"migration","query":"CREATE TABLE shipments (id bigint PRIMARY KEY, status text NOT NULL);\nINSERT INTO shipments SELECT g, 'NEW' FROM generate_series(1, 3) g;","migration_id":"7f96e713-…","execution_type":"UP","duration_ms":4,"status":"SUCCESS","trace_id":"1f5d052389932c3d11896055d2763300","span_id":"cb073128265e78cd","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
{"level":"INFO","event":"tx.commit","component":"migration","database_id":"15a8a362-…","migration_id":"7f96e713-…","execution_type":"UP","trace_id":"1f5d052389932c3d11896055d2763300","span_id":"c0ad35b2b3b2d411","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
{"level":"INFO","event":"migration.attempt","component":"migration","execution_type":"UP","migration_id":"7f96e713-…","execution_id":"541a76dc-…","database_id":"15a8a362-…","version":1,"status":"SUCCESS","duration_ms":5,"query":"CREATE TABLE shipments (id bigint PRIMARY KEY, status text NOT NULL);\nINSERT INTO shipments SELECT g, 'NEW' FROM generate_series(1, 3) g;","trace_id":"1f5d052389932c3d11896055d2763300","span_id":"6733661cfcf76d95","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
{"level":"INFO","event":"audit.execute","component":"migration","action":"execute","outcome":"success","execution_id":"541a76dc-…","migration_id":"7f96e713-…","database_id":"15a8a362-…","attempt":1,"sql_sent":true,"job_id":"6b55242d-…","trace_id":"1f5d052389932c3d11896055d2763300","span_id":"6733661cfcf76d95","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
{"level":"INFO","event":"job.finished","component":"worker","job_id":"6b55242d-…","migration_id":"7f96e713-…","database_id":"15a8a362-…","job_type":"EXECUTE","attempt":1,"worker_id":"worker-3-bbd17602","status":"SUCCESS","trace_id":"1f5d052389932c3d11896055d2763300","span_id":"6733661cfcf76d95","correlation_id":"readme-trace-2","actor":"06f2c024-…"}
```

A script that fails (`CREATE TABLE orders` when `orders` already exists) gives the same tree, with the error from the SQL span up to the job:

```json
["sql migration","Error","ERROR: relation \"orders\" already exists (SQLSTATE 42P07)",["exception"]]
["migration.run","Error","ERROR: relation \"orders\" already exists (SQLSTATE 42P07)",["tx.rollback","exception"]]
["job.process","Error","ERROR 42P07: relation \"orders\" already exists",[]]
```

A batch migration (`UPDATE shipments SET status = 'SHIPPED'`, cursor `id`, batch size 2, 3 rows): every batch is a child of `job.process` with its own statements and commit. The last batch finds no keys left and ends the run (name, attributes, events):

```json
["migration.batch","batch_no=1 rows=2 cursor_to=2",["tx.commit"]]
["migration.batch","batch_no=2 rows=1 cursor_to=3",["tx.commit"]]
["migration.batch","batch_no=3 rows=0",[]]
```
