-- Lesson 02 / experiment 3: data-skipping indexes - when they help and when they don't.

-- "Find everything that happened in session X" - session_id is random and not in ORDER BY,
-- so without help this is a full scan.
SELECT count() FROM shop.events WHERE session_id = (SELECT session_id FROM shop.events ORDER BY session_id LIMIT 1 OFFSET 5000000) SETTINGS log_comment = 'l02-skip-before';
-- A bloom filter per block of granules can prove "X is definitely not in here".
-- Indexes are only built for new parts. MATERIALIZE builds it for existing data (a mutation).
-- ADD INDEX only changes the table definition (instant). New inserts and merged parts get the index.
ALTER TABLE shop.events ADD INDEX idx_session session_id TYPE bloom_filter(0.01) GRANULARITY 1;

-- MATERIALIZE INDEX is needed only when the table already has data that the index should cover now.
ALTER TABLE shop.events MATERIALIZE INDEX idx_session SETTINGS mutations_sync = 2;
SELECT count() FROM shop.events WHERE session_id = (SELECT session_id FROM shop.events ORDER BY session_id LIMIT 1 OFFSET 5000000) SETTINGS log_comment = 'l02-skip-after';

-- A minmax index on a column whose values are random within each granule is useless:
ALTER TABLE shop.events ADD INDEX idx_dur duration_ms TYPE minmax GRANULARITY 1;
ALTER TABLE shop.events MATERIALIZE INDEX idx_dur SETTINGS mutations_sync = 2;
EXPLAIN indexes = 1 SELECT count() FROM shop.events WHERE duration_ms > 29990;

SYSTEM FLUSH LOGS;
SELECT log_comment, read_rows, query_duration_ms AS ms
FROM system.query_log
WHERE type = 'QueryFinish' AND log_comment LIKE 'l02-skip%' AND event_time > now() - INTERVAL 5 MINUTE
ORDER BY event_time;

-- Clean up the useless one.
ALTER TABLE shop.events DROP INDEX idx_dur;
