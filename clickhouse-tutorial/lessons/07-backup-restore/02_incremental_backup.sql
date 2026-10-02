-- Lesson 07 / step 2b: incremental backup after a new batch of ShopStream traffic arrived.

BACKUP DATABASE shop ON CLUSTER prod TO Disk('backups', 'shop-incr-2')
SETTINGS base_backup = Disk('backups', 'shop-full-1');

-- Long backups: run ASYNC and poll system.backups
-- BACKUP TABLE shop.events_local ON CLUSTER prod TO Disk('backups', 'x') ASYNC;

SELECT name, status, formatReadableSize(total_size) AS size, num_files FROM system.backups ORDER BY start_time DESC;
