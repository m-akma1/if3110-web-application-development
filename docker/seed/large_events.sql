-- large_events.sql: a large table for trying per-batch migrations (make seed-large).
-- 2,000,000 rows; an UPDATE of every row in one statement takes well over a second, which is
-- what the batch mode is for. Safe to run again: the table is recreated.
\set ON_ERROR_STOP on
\timing on
DROP TABLE IF EXISTS events;
CREATE TABLE events (
    id          BIGINT PRIMARY KEY,
    customer_id INTEGER       NOT NULL,
    kind        VARCHAR(16)   NOT NULL,
    status      VARCHAR(16)   NOT NULL DEFAULT 'NEW',
    amount      NUMERIC(12,2) NOT NULL,
    created_at  TIMESTAMPTZ   NOT NULL
);
INSERT INTO events (id, customer_id, kind, status, amount, created_at)
SELECT g,
       1 + g % 60,
       (ARRAY['view', 'click', 'order', 'refund'])[1 + g % 4],
       'NEW',
       (g % 1000) * 1.25,
       NOW() - (g % 365) * INTERVAL '1 day'
FROM generate_series(1, 2000000) AS g;
ANALYZE events;
SELECT count(*) AS rows, pg_size_pretty(pg_total_relation_size('events')) AS size FROM events;
