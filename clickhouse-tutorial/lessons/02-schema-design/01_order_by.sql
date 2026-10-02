-- Lesson 02 / experiment 1: the same 10M rows under three different sort keys.
-- Prereq: lesson 01 loaded shop.events.

DROP TABLE IF EXISTS shop.ev_by_time;
DROP TABLE IF EXISTS shop.ev_by_tenant;
DROP TABLE IF EXISTS shop.ev_lowcard_first;

-- A: "time-series reflex" - sort by time only.
CREATE TABLE shop.ev_by_time AS shop.events
ENGINE = MergeTree PARTITION BY toYYYYMM(event_time) ORDER BY event_time;

-- B: what lesson 01 used - the column you always filter on first.
CREATE TABLE shop.ev_by_tenant AS shop.events
ENGINE = MergeTree PARTITION BY toYYYYMM(event_time) ORDER BY (tenant_id, event_time);

-- C: low-cardinality columns first -> long runs of identical values -> better compression.
CREATE TABLE shop.ev_lowcard_first AS shop.events
ENGINE = MergeTree PARTITION BY toYYYYMM(event_time) ORDER BY (event_type, country, device, tenant_id, event_time);

INSERT INTO shop.ev_by_time       SELECT * FROM shop.events;
INSERT INTO shop.ev_by_tenant     SELECT * FROM shop.events;
INSERT INTO shop.ev_lowcard_first SELECT * FROM shop.events;
OPTIMIZE TABLE shop.ev_by_time FINAL;
OPTIMIZE TABLE shop.ev_by_tenant FINAL;
OPTIMIZE TABLE shop.ev_lowcard_first FINAL;

-- Size on disk per layout
SELECT table, formatReadableSize(sum(data_compressed_bytes)) AS on_disk, sum(rows) AS rows
FROM system.parts
WHERE database = 'shop' AND table LIKE 'ev\_%' AND active
GROUP BY table ORDER BY sum(data_compressed_bytes);

-- The same "one tenant, one week" query against each layout.
-- log_comment tags the queries so we can find them in query_log.
SELECT count() FROM shop.ev_by_time       WHERE tenant_id = 42 AND event_time >= '2026-07-01' AND event_time < '2026-07-08' SETTINGS log_comment = 'l02-order';
SELECT count() FROM shop.ev_by_tenant     WHERE tenant_id = 42 AND event_time >= '2026-07-01' AND event_time < '2026-07-08' SETTINGS log_comment = 'l02-order';
SELECT count() FROM shop.ev_lowcard_first WHERE tenant_id = 42 AND event_time >= '2026-07-01' AND event_time < '2026-07-08' SETTINGS log_comment = 'l02-order';

SYSTEM FLUSH LOGS;
SELECT
    extract(query, 'FROM shop\\.(\\w+)') AS tbl,
    read_rows,
    formatReadableSize(read_bytes) AS read,
    query_duration_ms AS ms
FROM system.query_log
WHERE type = 'QueryFinish' AND log_comment = 'l02-order'
  AND event_time > now() - INTERVAL 5 MINUTE
ORDER BY event_time DESC
LIMIT 3;
