-- Lesson 05 / step 8: consistency knobs, with ShopStream purchase events.

-- Default: the INSERT returns once THIS replica wrote the part. Other replicas fetch it
-- asynchronously (usually ms). If this node dies before they fetch -> data can be lost.

-- insert_quorum: wait until N replicas have the part. 'auto' = majority.
INSERT INTO shop.events_local SETTINGS insert_quorum = 2, insert_quorum_timeout = 10000
SELECT now64(3), 42, number, generateUUIDv4(number), 'purchase', 'DE', 'desktop', '/product/77', 1, 49.90
FROM numbers(10);

-- Read-your-writes on another replica:
--   select_sequential_consistency = 1 only returns data confirmed by quorum inserts.
--   Simpler in practice: pin a session/client to one replica (load_balancing = 'first_or_random'
--   / 'in_order'), or run SYSTEM SYNC REPLICA before reading.
SYSTEM SYNC REPLICA shop.events_local;   -- blocks until this replica's queue is empty

-- Deduplication: the same block inserted twice is stored once (safe retries).
-- Scenario: the ShopStream ingester sends a purchase, the ack is lost, it retries.
INSERT INTO shop.events_local VALUES ('2026-07-01 12:00:00', 7, 7, '00000000-0000-0000-0000-000000000007', 'purchase', 'US', 'mobile', '/product/7', 1200, 19.99);
INSERT INTO shop.events_local VALUES ('2026-07-01 12:00:00', 7, 7, '00000000-0000-0000-0000-000000000007', 'purchase', 'US', 'mobile', '/product/7', 1200, 19.99);
SELECT count() FROM shop.events_local WHERE tenant_id = 7;   -- 1, not 2

-- Look at what Keeper stores for this table
SELECT name, numChildren FROM system.zookeeper
WHERE path = '/clickhouse/tables/01/shop/events_local';
SELECT name FROM system.zookeeper
WHERE path = '/clickhouse/tables/01/shop/events_local/replicas';
