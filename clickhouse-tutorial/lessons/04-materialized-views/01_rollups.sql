-- Lesson 04 / 1: incremental rollups with a materialized view.
-- An MV is an INSERT trigger: each block inserted into the source is transformed and
-- inserted into the target table. It does NOT see data that was already there.

DROP VIEW IF EXISTS shop.mv_events_per_minute;
DROP TABLE IF EXISTS shop.events_per_minute;

-- Target: one row per (tenant, minute, event_type) holding partial aggregate states.
CREATE TABLE shop.events_per_minute
(
    tenant_id  UInt32,
    minute     DateTime('UTC'),
    event_type LowCardinality(String),
    events     AggregateFunction(count),
    users      AggregateFunction(uniq, UInt64),
    revenue    AggregateFunction(sum, Decimal(12, 2)),
    p95_dur    AggregateFunction(quantile(0.95), UInt32)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(minute)
ORDER BY (tenant_id, event_type, minute);

CREATE MATERIALIZED VIEW shop.mv_events_per_minute TO shop.events_per_minute AS
SELECT
    tenant_id,
    toStartOfMinute(event_time) AS minute,
    event_type,
    countState()                 AS events,
    uniqState(user_id)           AS users,
    sumState(revenue)            AS revenue,
    quantileState(0.95)(duration_ms) AS p95_dur
FROM shop.events
GROUP BY tenant_id, minute, event_type;

-- Backfill existing history ONCE (new inserts flow through the MV automatically).
-- On a live system: create MV first with a WHERE event_time >= <cutoff>, then backfill < cutoff.
INSERT INTO shop.events_per_minute
SELECT tenant_id, toStartOfMinute(event_time) AS minute, event_type,
       countState(), uniqState(user_id), sumState(revenue), quantileState(0.95)(duration_ms)
FROM shop.events
GROUP BY tenant_id, minute, event_type;

-- Query with -Merge combinators. Rows for the same key may not be merged yet: always GROUP BY.
SELECT
    toStartOfHour(minute) AS hour,
    countMerge(events)    AS events,
    uniqMerge(users)      AS users,
    sumMerge(revenue)     AS revenue,
    quantileMerge(0.95)(p95_dur) AS p95_ms
FROM shop.events_per_minute
WHERE tenant_id = 1 AND minute >= '2026-07-01' AND minute < '2026-07-02'
GROUP BY hour ORDER BY hour;

-- Compare sizes: the rollup is a fraction of the raw table.
SELECT table, formatReadableSize(sum(data_compressed_bytes)) AS size, sum(rows) AS rows
FROM system.parts WHERE database = 'shop' AND table IN ('events', 'events_per_minute') AND active
GROUP BY table;
