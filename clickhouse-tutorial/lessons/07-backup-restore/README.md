# Lesson 07 · Backup and restore

⏱ 45 minutes · 🧰 Uses `cluster/` with Lesson 06 data · ⬅️ [Lesson 06](../06-sharding/README.md) · ➡️ [Lesson 08](../08-monitoring/README.md)

## Objective
Be able to bring ShopStream's data back after a mistake that replication copies everywhere, and know your restore time.

## ShopStream in this lesson
You back up the `shop` database, add a new day of traffic, take an incremental backup, then recover from an engineer accidentally dropping the events tables. (Background: [the ShopStream dataset](../../data/README.md).)

## What you'll learn
- Why replication is not a backup
- Full and incremental backups with `BACKUP … ON CLUSTER`
- Restoring a dropped table, restoring under another name, restoring one partition
- What a production backup policy looks like (S3)

## Files in this lesson
| File | Purpose |
|---|---|
| `01_full_backup.sql` | full backup of the `shop` database |
| `02_incremental_backup.sql` | incremental backup on top of the full one |
| `03_restore.sql` | drop the tables, restore, restore under a new name |
| `../../data/generate_events.sql` | adds "a new day" of ShopStream traffic between backups |
| `../../go-client/cmd/backup` | the Go backup runner (exercise 2 solution) |

## How the lab stores backups
`cluster.xml` defines a disk named `backups` at `/backups/`. All four nodes mount the same Docker volume there,
standing in for S3 or NFS. That lets `ON CLUSTER` backups write to one shared location.

## Before you start
```bash
cd cluster && docker compose up -d --wait
alias ch1='docker compose exec ch-s1r1 clickhouse-client --password learn'
ch1 -q "SELECT count() FROM shop.events_all"      # expect ~5M from Lesson 06
```

---

## Step 1 — Replication is not a backup
Think about it before running anything: if someone runs `DROP TABLE … ON CLUSTER`, or a bad `ALTER DELETE`,
every replica does it faithfully. You need a copy the cluster can't touch.

## Step 2 — Take a full backup
```bash
ch1 --queries-file /lessons/07-backup-restore/01_full_backup.sql --format PrettyCompact
```
`BACKUP DATABASE shop ON CLUSTER prod TO Disk('backups', 'shop-full-1')`: every shard writes its data, coordinated
through Keeper. The result shows `system.backups` with status `BACKUP_CREATED`, size and file count.

## Step 2b — New traffic arrives, then an incremental backup
```bash
ch1 --param_db=shop --param_table=events_all --param_rows=100000 --param_seed=3 \
    --distributed_foreground_insert=1 --queries-file /data/generate_events.sql
ch1 --queries-file /lessons/07-backup-restore/02_incremental_backup.sql --format PrettyCompact
```
`shop-incr-2` uses `base_backup = shop-full-1`, so only new parts are copied. Compare the two sizes in the result:
the incremental one is much smaller.

## Step 3 — Look at the files
```bash
docker compose exec ch-s1r1 ls /backups
docker compose exec ch-s1r1 ls /backups/shop-full-1
```
A backup is plain files plus a `.backup` metadata file. Anything that can store files can hold it.

## Step 4 — Disaster: drop the table
```bash
ch1 -q "SELECT count() FROM shop.events_all"      # write this number down
```
Now open `03_restore.sql` and read it. It drops both tables on the whole cluster, then restores them.

## Step 5 — Restore
```bash
ch1 --queries-file /lessons/07-backup-restore/03_restore.sql --format PrettyCompact
```
What happens:
1. `DROP TABLE … ON CLUSTER`: data gone on every replica.
2. `RESTORE TABLE shop.events_local ON CLUSTER prod FROM … 'shop-incr-2'`: pulls base parts from `shop-full-1`
   automatically. Then the same for `events_all`.
3. `count()` matches the number from Step 4.
4. A restore **under a new name** (`events_local_restored`) to inspect old data without touching production, then dropped.

## Step 6 — Run a backup asynchronously
Real backups take minutes to hours. Run them in the background and poll:
```bash
ch1 -q "BACKUP TABLE shop.events_local ON CLUSTER prod TO Disk('backups', 'async-1') ASYNC"
ch1 -q "SELECT name, status, error FROM system.backups ORDER BY start_time DESC LIMIT 3"
```
Repeat the second command until the status is `BACKUP_CREATED`.

## Step 7 — Production: S3
Same statements, different destination:
```sql
BACKUP DATABASE shop ON CLUSTER prod
TO S3('https://my-bucket.s3.eu-west-1.amazonaws.com/clickhouse/shop-2026-09-29', '<key>', '<secret>')
SETTINGS base_backup = S3('https://…/shop-2026-09-28', '<key>', '<secret>');
```
Put credentials in server config (named collections) or use IAM roles, not SQL literals.

## Best practices for backup and restore (from ClickHouse docs and Altinity)
These come from the official [ClickHouse backup & restore docs](https://clickhouse.com/docs/operations/backup) and
Altinity's [introduction to ClickHouse backups](https://altinity.com/blog/introduction-to-clickhouse-backups-and-clickhouse-backup).
For each one, the "ShopStream" line says how it applies to this tutorial's project.

### 1. Plan the strategy before you need it
The docs say to prepare backup and restore **in advance** and to combine several methods, since each has gaps.
Write down your targets first: how much data you can lose (RPO) and how long a restore may take (RTO).
- **ShopStream:** RPO 1 hour (hourly incrementals, plus Kafka replay upstream), RTO 4 hours for the events tables.

### 2. Replication is not a backup, and neither are local copies
Replicas copy `DROP`, `TRUNCATE` and bad mutations everywhere. Altinity also warns that local storage alone "is usually
insufficient to meet data durability requirements". Keep backups **off the cluster**, in object storage.
- **ShopStream:** S3 bucket in a separate account, object lock (write-once) turned on.

### 3. Mix full and incremental backups
- Full backups suit small databases or the most critical data.
- Incrementals (`SETTINGS base_backup = …`) suit large databases that need frequent backups.
- The docs suggest a hybrid such as **weekly full + daily incremental**. Go more frequent (daily full + hourly
  incremental) if your RPO needs it.
- An incremental needs its base to restore. Never delete a base while any incremental still depends on it, and start
  a new full regularly so the chain stays short.

```sql
BACKUP DATABASE shop ON CLUSTER prod TO S3('https://bucket.s3.amazonaws.com/ch/full-2026-09-28') ASYNC;
BACKUP DATABASE shop ON CLUSTER prod TO S3('https://bucket.s3.amazonaws.com/ch/incr-2026-09-29')
  SETTINGS base_backup = S3('https://bucket.s3.amazonaws.com/ch/full-2026-09-28') ASYNC;
```

### 4. On a cluster, cover every shard
Use `ON CLUSTER` so each shard's data is included; Altinity puts it as "at least one replica from each shard should
be backed up". Backing up every replica of a shard only stores the same data twice.
- **ShopStream:** `ON CLUSTER prod` backs up shard 01 and shard 02 once each.

### 5. Run backups in the background and watch them
Use `ASYNC` for anything large and poll `system.backups`; `system.backup_log` keeps the history. Alert when a backup
fails, and when the newest successful backup is older than your schedule allows.
- **ShopStream:** `go-client/cmd/backup` starts an `ASYNC` backup, polls, and exits non-zero on `BACKUP_FAILED`.
  Add a "no successful backup in 26 hours" alert (Lesson 08, exercise 4).

### 6. Keep resource use and cost under control
- `compression_method` / `compression_level` shrink backups written as archives.
- `allow_concurrent_backups = false` and `allow_concurrent_restores = false` stop two heavy jobs from competing.
- Skip high-volume system logs (`query_log`, `trace_log`, `metric_log`); they are monitoring data, not business data.
  Back up the `shop` database, not everything.

### 7. Protect the backups themselves
- `password` encrypts ZIP archive backups; for S3, also use bucket encryption.
- Give ClickHouse a role or a named collection with write access to the bucket, not keys typed into SQL, and don't let
  the application user delete backups.
- Retention by lifecycle rules. Altinity's `clickhouse-backup` examples keep 7 local and 31 remote backups.
- Don't change file permissions inside a local backup folder: Altinity notes the files are hard links to live data
  parts, so a permission change affects the table too.

### 8. Back up what isn't table data
- `BACKUP DATABASE` includes table schemas. `structure_only = true` gives a schema-only backup, useful before
  migrations.
- Keep server config, users, roles and quotas in git (or back up access entities too).
- Keeper doesn't need backup: `SYSTEM RESTORE REPLICA` rebuilds it from the data (Lesson 09, drill 6).

### 9. Test restores, on a schedule
The docs: if you've never tried to restore a backup, "chances are that restore won't work properly when you actually
need it". Automate restores onto a spare cluster and practise regularly. Altinity: "a backup is worthless if the
restoration process hasn't been tested".
- **ShopStream:** a monthly job restores the latest backup into a scratch cluster, compares `count()` per partition with
  production, and records the restore time against the 4-hour RTO.

### 10. Restore surgically, and carefully
- Restore a single table, or only some partitions (`PARTITIONS '202607'`), instead of the whole database.
- Restore under a new name (`RESTORE TABLE shop.events_local AS shop.events_restored …`) to inspect before touching
  production, then copy back only the rows you need with `INSERT … SELECT`.
- `allow_non_empty_tables = 1` restores into a table that already has data, but can **duplicate rows**. Use it only
  when you know the restored partitions aren't there.

### 11. Guard against the accident in the first place
`max_table_size_to_drop` (default 50 GB) and `max_partition_size_to_drop` block dropping big tables without an explicit
override. Combine that with RBAC so application users can't `DROP` or `TRUNCATE` at all.

### 12. Pick the right tool
| Tool | Good for |
|---|---|
| Native `BACKUP`/`RESTORE` (this lesson) | built in, SQL-driven, `ON CLUSTER`, incremental, S3/GCS/Azure/disk |
| Altinity [`clickhouse-backup`](https://github.com/Altinity/clickhouse-backup) | CLI/REST daemon, retention built in (`backups_to_keep_*`), incrementals with `--diff-from`, popular for self-hosted |
| `ALTER TABLE … FREEZE` | hard-link snapshot of parts; the building block the tools use |
| ClickHouse Cloud | automatic backups managed for you |

### ShopStream backup policy (putting it together)
| What | How |
|---|---|
| Full | weekly (Sunday), `BACKUP DATABASE shop ON CLUSTER prod TO S3(...) ASYNC` |
| Incremental | daily against the latest full (hourly if the 1-hour RPO must come from backups alone) |
| Where | S3 in a separate account, object lock, lifecycle: 7 dailies, 4 weeklies, 12 monthlies |
| Runner | `go-client/cmd/backup` from cron, alert on failure and on age > 26 h |
| Schema and config | `structure_only` backup before each migration; config and users in git |
| Restore test | monthly into a scratch cluster, compare counts, record time vs RTO |
| Guardrails | `max_table_size_to_drop`, no DROP/TRUNCATE for app users |

---

## ✅ Checkpoint
- Why did the incremental restore still need `shop-full-1`?
- Why restore under a different name?
- What would you back up besides table data?

## 🏋️ Exercises
1. Restore only partition `202607`:
   `RESTORE TABLE shop.events_local PARTITIONS '202607' ON CLUSTER prod FROM Disk('backups','shop-full-1') SETTINGS allow_non_empty_tables = 1`.
   What happens to rows already there?
2. Write a Go program that starts `BACKUP … ASYNC`, polls `system.backups` until done, and exits non-zero on `BACKUP_FAILED`.
   Solution: `go-client/cmd/backup` (`go run ./cmd/backup -addrs localhost:9001`).
3. Time the restore. Scale it to your data size. Does it meet your recovery time target?
