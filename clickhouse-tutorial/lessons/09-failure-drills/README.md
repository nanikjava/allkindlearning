# Lesson 09 · Failure drills

⏱ 2 hours · 🧰 Uses `cluster/` + `go-client/` · ⬅️ [Lesson 08](../08-monitoring/README.md) · ➡️ [Lesson 10](../10-production-checklist/README.md)

## Objective
Build confidence that ShopStream keeps working through real failures by causing them on purpose, and turn what you see into a runbook.

## ShopStream in this lesson
The Go ingester keeps sending shop events while you kill replicas, Keepers, a shard, a disk and the network. You check what the dashboard and the ingester experience each time. (Background: [the ShopStream dataset](../../data/README.md).)

## What you'll learn
- What the client and the cluster each see when a replica, Keeper, a shard, a disk or the network fails
- How to recover from each failure
- How to turn what you saw into a runbook

## Files in this lesson
| File | Purpose |
|---|---|
| `scripts/watch.sh` | terminal 2: replica health on all nodes + ShopStream event total, every 2 s |
| `scripts/keeper-status.sh` | which Keeper is leader / follower / down |
| `scripts/drill.sh` | `./scripts/drill.sh <1-8> break` and `… heal` for every drill below |
| `../../go-client/cmd/ingest` | terminal 1: live ShopStream traffic |
| `../../go-client/cmd/health` | the health check from Lesson 08 |

## Before you start
You need the cluster from Lessons 05–07 with `shop.events_local`, `shop.events_all` and the `shop-incr-2` backup.
```bash
cd cluster && docker compose up -d --wait && cd ..
chmod +x lessons/09-failure-drills/scripts/*.sh
```

## How to run every drill
For each drill: **1.** start the load, **2.** open the watch terminal, **3.** break something, **4.** observe,
**5.** recover, **6.** write down what happened.

### Terminal 1 — load (leave running)
```bash
cd go-client
go run ./cmd/ingest -addrs localhost:9001,localhost:9002,localhost:9003,localhost:9004 -table events_all
```

### Terminal 2 — watch the cluster
```bash
./lessons/09-failure-drills/scripts/watch.sh
```

### Terminal 3 — break things
Each step shows the raw commands (run from `cluster/`) so you see what's happening. The same thing is wrapped in
`scripts/drill.sh`, e.g. `./lessons/09-failure-drills/scripts/drill.sh 1 break` and `… 1 heal`.

### Your notes (copy this table)
| Drill | Client saw | Cluster saw | Time to recover | Data lost? |
|---|---|---|---|---|

Do the drills in order; each one ends with the cluster healthy again. If a drill leaves things broken, reset with
`docker compose down -v && docker compose up -d --wait` and rerun Lessons 05–07's SQL.

---

## Step 1 — One replica dies

> Shortcut: `scripts/drill.sh 1 break`, observe, then `scripts/drill.sh 1 heal`.
```bash
docker compose kill ch-s1r2
```
Expect: writes continue (other replica takes them; Distributed routes around), reads continue,
`active_replicas` = 1 for shard 01. Then:
```bash
docker compose start ch-s1r2
```
Expect: ch-s1r2 fetches missed parts; `queue_size` spikes then drains; `absolute_delay` → 0. **No data lost.**

## Step 2 — The Keeper leader dies

> Shortcut: `scripts/drill.sh 2 break`, observe, then `scripts/drill.sh 2 heal`.
```bash
../lessons/09-failure-drills/scripts/keeper-status.sh
docker compose kill <leader>
```
Expect: a few seconds of Keeper errors/retries in the ingester while a new leader is elected, then normal.
Start it again; it rejoins as follower.

## Step 3 — Keeper loses quorum

> Shortcut: `scripts/drill.sh 3 break`, observe, then `scripts/drill.sh 3 heal`.
```bash
docker compose kill keeper2 keeper3
```
Expect: **reads keep working**. Inserts fail with `TABLE_IS_READ_ONLY` (242) / Keeper errors after the session
times out; `is_readonly = 1`. ON CLUSTER DDL hangs. The ingester retries and then drops batches — this is why
a durable queue (Kafka) upstream matters.
```bash
docker compose start keeper2 keeper3
```
Expect: replicas reconnect and become writable within ~30s. Lesson: Keeper availability = write availability.

## Step 4 — A whole shard is down

> Shortcut: `scripts/drill.sh 4 break`, observe, then `scripts/drill.sh 4 heal`.
```bash
docker compose kill ch-s2r1 ch-s2r2
```
Queries on `events_all` fail (`ALL_CONNECTION_TRIES_FAILED`). Try:
```sql
SELECT count() FROM shop.events_all SETTINGS skip_unavailable_shards = 1;   -- partial answer
SELECT * FROM system.distribution_queue;                                      -- inserts queued for shard 2
```
Start them; the queue drains. Lesson: replicas per shard in **different failure domains** (racks/AZs).

## Step 5 — A replica loses its disk entirely

> Shortcut: `scripts/drill.sh 5 break`, observe, then `scripts/drill.sh 5 heal`.
```bash
docker compose rm -sf ch-s1r2
docker volume rm ch-cluster_s1r2
docker compose up -d ch-s1r2
```
The node comes back empty (no `shop` database). Its old replica metadata is still in Keeper, so recreate the
schema and it will re-fetch everything from ch-s1r1:
```sql
-- on ch-s1r2
CREATE DATABASE shop;
-- copy the CREATE TABLE from another replica: SHOW CREATE TABLE shop.events_local (on ch-s1r1)
-- run it on ch-s1r2 WITHOUT "ON CLUSTER". Same path + same replica name = it re-syncs.
```
If you use a `Replicated` database engine, this step is automatic. If a node is gone for good, remove its
metadata from another replica: `SYSTEM DROP REPLICA 'ch-s1r2' FROM TABLE shop.events_local;`

## Step 6 — Keeper metadata is lost

> Shortcut: `scripts/drill.sh 6 break`, observe, then `scripts/drill.sh 6 heal`.
```bash
docker compose kill keeper1 keeper2 keeper3
docker volume rm ch-cluster_keeper1 ch-cluster_keeper2 ch-cluster_keeper3
docker compose up -d keeper1 keeper2 keeper3
```
All replicated tables are read-only forever (their Keeper paths are gone). Rebuild metadata from the data on disk:
```sql
SYSTEM RESTART REPLICA shop.events_local;          -- on every node
SYSTEM RESTORE REPLICA shop.events_local ON CLUSTER prod;
```
Lesson: Keeper holds no data you can't regenerate, but recovery is manual — run 3–5 Keepers on separate disks.

## Step 7 — Human error

> Shortcut: `scripts/drill.sh 7 break`, observe, then `scripts/drill.sh 7 heal`.
```sql
TRUNCATE TABLE shop.events_local ON CLUSTER prod SYNC;
```
Replication happily replicated it. Recover with lesson 07 (`RESTORE … PARTITIONS` or full). Then consider:
RBAC so the app user can't `TRUNCATE`/`DROP`, `max_table_size_to_drop`, and backups your app user can't delete.

## Step 8 — A network partition

> Shortcut: `scripts/drill.sh 8 break`, observe, then `scripts/drill.sh 8 heal`.
```bash
docker network disconnect ch-cluster_default ch-cluster-ch-s1r1-1
# ... observe for 60s: s1r1 goes read-only (lost Keeper), s1r2 keeps taking writes
docker network connect ch-cluster_default ch-cluster-ch-s1r1-1
```
When healed, s1r1 catches up. No split-brain: writes require a Keeper session, and only the majority side has one.

## Step 9 — Disk fills up (optional, destructive)
Fill the data volume inside a container (`fallocate -l 50G /var/lib/clickhouse/fill`) and watch inserts and
merges fail. Delete the file and see recovery. In production: alert at 85% and keep merge headroom.

---
## Step 10 — Write your runbook
Use `runbook-template.md` in this folder: one copy per drill.
Turn your notes into a one-page runbook per failure: *symptom → which query/alert shows it → action → verify*.
That document is the real deliverable of this lesson.

## ✅ Checkpoint
- Which failures stop **writes** but not **reads**?
- What's the difference between losing one Keeper and losing two?
- After a replica loses its disk, what brings its data back?
- Which failure did replication *not* protect you from?
