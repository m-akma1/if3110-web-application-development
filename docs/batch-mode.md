# Per-Batch Execution: Recovery Data and Measurements

Details behind [Per-Batch Execution](../README.md#per-batch-execution) in the README: how a batch keeps the data needed to undo it, when an abort is refused as unsafe, and how batch mode performed on 2,000,000 rows.

## Recovery data and the abort safety rule

- **Recovery data**: before an UP batch of an `UPDATE` changes its rows, it copies them into `_dbplatform_batch_rows` on the target, as `to_jsonb` of the whole row (the *before-image*), and locks them with `FOR UPDATE`, in the batch's own transaction. How many it stored is kept with the checkpoint (`recovery_rows`).
  - The copy uses the statement's `WHERE`; with `UPDATE ... FROM`, whose `WHERE` joins other tables, it takes every row of the key range.
  - The statement then reports the keys it changed (`RETURNING`). If it changed a row that was not copied (another transaction made it match in between), the batch is rolled back with "baris pada rentang batch berubah ..." and the run stops; running it again is safe.
  - Irreversible migrations store nothing, since they cannot be aborted with rollback.
- **The abort safety rule**: an abort with rollback is refused before it starts when a committed batch has no recovery data (it was committed by an older version of the platform); *Tandai Dihentikan* still works. Each DOWN batch runs SQL Down over the batch's key range, then, **in the same transaction**, compares the stored before-images with the rows now in the range. It commits only when
  1. every stored row is back exactly as it was before the migration, and
  2. SQL Down changed no more rows than the batch had changed.

  Otherwise that batch is rolled back and the abort stops as **unsafe** (still `PARTIAL`, or `PARTIALLY_REVERTED` when later batches were already undone), with the batch number, key range and row counts in the error. The usual causes are a row changed again after the migration, or a SQL Down that does not exactly undo SQL Up, for example `UPDATE ... SET status = 'NEW' WHERE status = 'ARCHIVED'` when some rows were already `ARCHIVED` before. A batch is never left half-reverted.
- **Cleanup**: a DOWN batch deletes its own recovery rows when it commits; the rest are purged when the migration becomes final (`SUCCESS` or `ABORTED`). `_dbplatform_batch_rows` is hidden from the Database Viewer like the checkpoint table.

## Large dataset

`make seed-large` creates `events` (2,000,000 rows, 173 MB) on the demo target, owned by the app role. Measured on 2026-11-27 on one laptop against the Docker test database (PostgreSQL 16, data on tmpfs), each run on a freshly seeded table, so treat the numbers as rough and compare them with each other rather than with another machine:

| Run of `UPDATE events SET status = 'ARCHIVED'` | Total | Longest single transaction (average) |
|---|---|---|
| One statement (standard mode) | 6.9 s | the whole statement, 6.9 s, with row locks on all 2,000,000 rows |
| Batch mode, `batch_size` 10,000 (200 batches), with recovery data | 20.5 s | 145 ms (97 ms) |
| Batch mode, `batch_size` 10,000, irreversible (no recovery data) | 7.7 s | 46 ms (33 ms) |
| Batch mode, `batch_size` 50,000 (40 batches), with recovery data | 20.6 s | 512 ms (462 ms) |
| Batch mode, `batch_size` 50,000, irreversible (no recovery data) | 7.6 s | 237 ms (175 ms) |
| Abort with rollback of 199 batches of 10,000 (SQL Down + check per batch) | 19.7 s | 125 ms (94 ms) |

- Batch mode alone costs little next to the single statement. Storing the before-images makes each batch about three times slower, because every changed row is also written as JSON; the longest transaction still stays well under a second.
- The recovery data of the whole table took **459 MB** (the table itself is 173 MB). It is deleted when the migration is final, but PostgreSQL keeps the space for reuse by the table (autovacuum) instead of returning it to the disk; `VACUUM FULL _dbplatform_batch_rows` gives it back.
- Marking a batch migration irreversible skips the recovery data (and the abort with rollback) when that cost is not worth it.
- An earlier measurement on a slower laptop, before recovery data existed, gave 30–49 s for the single statement and 40.0 s / 25.9 s for batch sizes 10,000 / 50,000.
