-- Lesson 07 / step 6: disaster. A ShopStream engineer runs a cleanup script against production
-- instead of staging and drops the events tables. Recover.

SELECT count() FROM shop.events_all;   -- remember this number

DROP TABLE shop.events_all ON CLUSTER prod SYNC;
DROP TABLE shop.events_local ON CLUSTER prod SYNC;

-- Restore from the incremental backup (it pulls base parts from shop-full-1 automatically).
-- Restore the local table first, then the Distributed table on top of it.
RESTORE TABLE shop.events_local ON CLUSTER prod FROM Disk('backups', 'shop-incr-2');
RESTORE TABLE shop.events_all   ON CLUSTER prod FROM Disk('backups', 'shop-incr-2');

SELECT count() FROM shop.events_all;   -- same number as before

-- Restore under a different name to inspect old data without touching production?
-- Not on this cluster: the backup keeps the CREATE query, so the copy would get the same
-- Keeper path ('/clickhouse/tables/{shard}/shop/events_local') and replica name as the live
-- table and fail with REPLICA_ALREADY_EXISTS. Restore it onto a scratch cluster with its own
-- Keeper instead (see the README, best practice 10).

-- Restore only one partition:
-- RESTORE TABLE shop.events_local PARTITIONS '202607' ON CLUSTER prod FROM Disk('backups','shop-full-1')
--   SETTINGS allow_non_empty_tables = 1;
