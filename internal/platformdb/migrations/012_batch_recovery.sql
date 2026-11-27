-- 012_batch_recovery.sql
-- Recovery data for aborting a batch migration. Before each UP batch of an UPDATE, the engine
-- stores the before-image of the rows it changes in _dbplatform_batch_rows on the target, in the
-- batch's transaction. recovery_rows is how many rows were stored; NULL means none were kept
-- (an irreversible migration, a DOWN batch, or a batch committed before this column existed).
-- An abort with rollback is refused while a committed batch that is not undone has NULL here.

ALTER TABLE migration_batches ADD COLUMN recovery_rows BIGINT CHECK (recovery_rows >= 0);
