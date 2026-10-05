-- Lesson 02 / experiment 4: data lifecycle with partitions and TTL.

-- Dropping a whole partition is a metadata operation: instant, no rewrite.
-- Build a copy of shop.events partitioned by month so we can manage data per month.
DROP TABLE IF EXISTS shop.ev_lifecycle;
CREATE TABLE shop.ev_lifecycle AS shop.events   -- same columns as shop.events
ENGINE = MergeTree
PARTITION BY toYYYYMM(event_time)   -- one partition per month, e.g. 202606
ORDER BY (tenant_id, event_time)    -- sort key inside each part (also the primary index)
SETTINGS ttl_only_drop_parts = 1;   -- when TTL expires, drop whole parts instead of rewriting them
                                    -- (only works when every row in a part has expired, which
                                    -- monthly partitions make likely)

-- Copy all rows in. Parts are split by partition, so each part belongs to exactly one month.
INSERT INTO shop.ev_lifecycle SELECT * FROM shop.events;

-- Rows per month. `active` skips old parts that were merged away but not yet deleted.
SELECT partition, sum(rows) AS rows FROM system.parts
WHERE database = 'shop' AND table = 'ev_lifecycle' AND active GROUP BY partition ORDER BY partition;

-- Detach / attach / drop by partition
ALTER TABLE shop.ev_lifecycle DETACH PARTITION 202606;   -- files move to detached/, invisible to queries
SELECT count() FROM shop.ev_lifecycle;                    -- count drops by June's rows
ALTER TABLE shop.ev_lifecycle ATTACH PARTITION 202606;   -- files move back from detached/, visible again
SELECT count() FROM shop.ev_lifecycle;                    -- count is back to the original
-- (ALTER TABLE ... DROP PARTITION 202606 would delete the month for good.)

-- Add retention: rows older than 60 days (relative to now) get deleted during merges
--   toDateTime(event_time)  TTL needs a Date/DateTime; this converts event_time (e.g. DateTime64)
--   + INTERVAL 60 DAY       a row expires 60 days after its event_time
--   DELETE                  expired rows are removed (other actions: TO DISK / TO VOLUME, GROUP BY)
-- The TTL is checked against now(), so what expires depends on when you run this.
ALTER TABLE shop.ev_lifecycle MODIFY TTL toDateTime(event_time) + INTERVAL 60 DAY DELETE
SETTINGS materialize_ttl_after_modify = 0;   -- only save the rule; don't rewrite existing parts yet

-- Force the TTL to be applied now (normally it happens during background merges)
-- MATERIALIZE TTL runs as a mutation; mutations_sync = 2 waits until it finishes (on all replicas).
-- Thanks to ttl_only_drop_parts, fully expired monthly parts are simply dropped.
ALTER TABLE shop.ev_lifecycle MATERIALIZE TTL SETTINGS mutations_sync = 2;
-- min(event_time) should now be no older than about 60 days ago (or whole-month boundaries,
-- since a part is only dropped once all of its rows have expired).
SELECT min(event_time), max(event_time), count() FROM shop.ev_lifecycle;

-- Change your mind about retention later:
-- This only changes the rule. Rows already deleted under the 60-day TTL don't come back.
ALTER TABLE shop.ev_lifecycle MODIFY TTL toDateTime(event_time) + INTERVAL 1 YEAR DELETE;
