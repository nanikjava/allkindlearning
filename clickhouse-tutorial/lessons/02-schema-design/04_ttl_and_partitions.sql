-- Lesson 02 / experiment 4: data lifecycle with partitions and TTL.

-- Dropping a whole partition is a metadata operation: instant, no rewrite.
DROP TABLE IF EXISTS shop.ev_lifecycle;
CREATE TABLE shop.ev_lifecycle AS shop.events
ENGINE = MergeTree
PARTITION BY toYYYYMM(event_time)
ORDER BY (tenant_id, event_time)
SETTINGS ttl_only_drop_parts = 1;   -- when TTL expires, drop whole parts instead of rewriting them

INSERT INTO shop.ev_lifecycle SELECT * FROM shop.events;

SELECT partition, sum(rows) AS rows FROM system.parts
WHERE database = 'shop' AND table = 'ev_lifecycle' AND active GROUP BY partition ORDER BY partition;

-- Detach / attach / drop by partition
ALTER TABLE shop.ev_lifecycle DETACH PARTITION 202606;   -- files move to detached/, invisible to queries
SELECT count() FROM shop.ev_lifecycle;
ALTER TABLE shop.ev_lifecycle ATTACH PARTITION 202606;
SELECT count() FROM shop.ev_lifecycle;

-- Add retention: rows older than 60 days (relative to now) get deleted during merges
ALTER TABLE shop.ev_lifecycle MODIFY TTL toDateTime(event_time) + INTERVAL 60 DAY DELETE
SETTINGS materialize_ttl_after_modify = 0;

-- Force the TTL to be applied now (normally it happens during background merges)
ALTER TABLE shop.ev_lifecycle MATERIALIZE TTL SETTINGS mutations_sync = 2;
SELECT min(event_time), max(event_time), count() FROM shop.ev_lifecycle;

-- Change your mind about retention later:
ALTER TABLE shop.ev_lifecycle MODIFY TTL toDateTime(event_time) + INTERVAL 1 YEAR DELETE;
