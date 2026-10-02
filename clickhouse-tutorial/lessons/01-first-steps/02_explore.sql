-- Lesson 01: look inside. Run these one at a time in clickhouse-client so you can read the output.

-- 1. How many rows, and how big on disk vs in memory?
SELECT
    count()                                     AS parts,
    sum(rows)                                   AS rows,
    formatReadableSize(sum(data_compressed_bytes))   AS on_disk,
    formatReadableSize(sum(data_uncompressed_bytes)) AS uncompressed,
    round(sum(data_uncompressed_bytes) / sum(data_compressed_bytes), 1) AS ratio
FROM system.parts
WHERE database = 'shop' AND table = 'events' AND active;

-- 2. Per-column compression: which columns cost you the most?
SELECT
    name,
    type,
    formatReadableSize(data_compressed_bytes)   AS compressed,
    formatReadableSize(data_uncompressed_bytes) AS uncompressed,
    round(data_uncompressed_bytes / data_compressed_bytes, 1) AS ratio
FROM system.columns
WHERE database = 'shop' AND table = 'events'
ORDER BY data_compressed_bytes DESC;

-- 3. Parts per partition. Every INSERT creates a part; background merges combine them.
SELECT partition, count() AS parts, sum(rows) AS rows, max(level) AS max_merge_level
FROM system.parts
WHERE database = 'shop' AND table = 'events' AND active
GROUP BY partition
ORDER BY partition;

-- 4. ShopStream question 1: "how did shop 42 do in the first week of July?"
--    Uses the primary index well (tenant_id is first in ORDER BY).
SELECT event_type, count() AS events, uniq(user_id) AS users
FROM shop.events
WHERE tenant_id = 42 AND event_time >= '2026-07-01' AND event_time < '2026-07-08'
GROUP BY event_type
ORDER BY events DESC;

-- 5. Ask ClickHouse how it will use the index. Look at "Granules: x/y".
EXPLAIN indexes = 1
SELECT count() FROM shop.events
WHERE tenant_id = 42 AND event_time >= '2026-07-01' AND event_time < '2026-07-08';

-- 6. A query that CANNOT use the primary index (country isn't in ORDER BY) -> full scan.
EXPLAIN indexes = 1
SELECT count() FROM shop.events WHERE country = 'DE';

-- 6b. ...and actually run it, so it shows up in query_log below.
SELECT count() FROM shop.events WHERE country = 'DE';

-- 7. Compare what the two queries actually read (query_log is flushed every ~7s;
--    SYSTEM FLUSH LOGS forces it).
SYSTEM FLUSH LOGS;
SELECT
    event_time,
    query_duration_ms,
    read_rows,
    formatReadableSize(read_bytes) AS read,
    substring(query, 1, 80)        AS q
FROM system.query_log
WHERE type = 'QueryFinish' AND query ILIKE '%FROM shop.events%' AND query NOT ILIKE '%system.query_log%'
ORDER BY event_time DESC
LIMIT 10;
