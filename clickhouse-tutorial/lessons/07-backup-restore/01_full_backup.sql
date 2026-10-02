-- Lesson 07 / step 2: full backup of the ShopStream database. Prereq: lesson 06 data in shop.events_local.
-- All nodes mount the same 'backups' docker volume at /backups (stand-in for S3/NFS).

-- Full backup of the whole database, every shard, coordinated through Keeper.
BACKUP DATABASE shop ON CLUSTER prod TO Disk('backups', 'shop-full-1');

-- See backup history and status
SELECT name, status, formatReadableSize(total_size) AS size, num_files, start_time, end_time, error
FROM system.backups ORDER BY start_time DESC;

