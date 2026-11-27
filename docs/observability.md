# Observability: Metrics and Correlation

Details behind [Observability](../README.md#observability) in the README: every metric the app exposes, how logs, traces and metrics link to each other in Grafana, and queries to start from.

## Metrics

| Metric | Labels | What it shows |
|---|---|---|
| `dbplatform_jobs` | `status` | Jobs in `migration_jobs` by status, read at scrape time |
| `dbplatform_jobs_enqueued_total`, `dbplatform_jobs_finished_total` | `job_type`, `status` | Queue input and output (throughput) |
| `dbplatform_job_retries_total` | `job_type`, `reason` | `busy` (database locked, no attempt used), `transient` (target unreachable), `interrupted` (shutdown between batches), `recovered` (lease expired) |
| `dbplatform_job_queue_wait_seconds` | `job_type` | From a job being due to a worker claiming it |
| `dbplatform_migration_lock_wait_seconds` | `outcome` | Waiting for the database's advisory lock (`acquired`, `busy`, `error`) |
| `dbplatform_migration_run_seconds` | `job_type`, `outcome` | One attempt in the engine (`success`, `script_failed`, `partial`, `busy`, `revalidation_failed`, ...) |
| `dbplatform_migration_batches_total`, `dbplatform_migration_batch_rows_total`, `dbplatform_migration_batch_seconds` | `direction` | Batch mode: committed batches, rows, transaction time |
| `dbplatform_http_requests_total`, `dbplatform_http_request_duration_seconds` | `route`, `method`, `code` | API requests by route pattern (`POST /api/migration/{id}/execute`), status class |
| `dbplatform_db_pool_*` | `pool` | `sql.DB.Stats()` of the platform pool and of every target pool by target name: open, in use, idle, max, waits |

Labels never contain user IDs, job or migration IDs, raw paths or SQL: those would create a series per value. They are in the logs and traces instead, one click away.

## Correlation in Grafana

- A log line's `trace_id` has a **Buka trace di Tempo** link (Loki derived field).
- A span in Tempo has **Logs for this span**, which opens the Loki records of the same trace (`{service_name="dbplatform"} | trace_id="..."`).
- The dashboard's last row shows warnings/errors from Loki and the newest `job.process` traces from Tempo next to the metrics.

## Useful queries

```promql
# job throughput and queue wait (p95)
sum by (status) (rate(dbplatform_jobs_finished_total[1m]))
histogram_quantile(0.95, sum by (le) (rate(dbplatform_job_queue_wait_seconds_bucket[1m])))
# is a database pool saturated?
rate(dbplatform_db_pool_wait_seconds_total[1m])
```

```logql
# every record of one trace, or of one job
{service_name="dbplatform"} | trace_id="<trace id>"
{service_name="dbplatform"} | job_id="<job id>"
# failed SQL
{service_name="dbplatform"} |= "sql.result" | status="FAILED"
```
