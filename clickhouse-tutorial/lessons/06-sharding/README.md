# Lesson 06 · Sharding with Distributed tables

⏱ 60 minutes · 🧰 Uses `cluster/` + `go-client/` · ⬅️ [Lesson 05](../05-replication/README.md) · ➡️ [Lesson 07](../07-backup-restore/README.md)

## Objective
Scale ShopStream past one machine: split events across shards while keeping each shop's queries fast.

## ShopStream in this lesson
You add `shop.events_all` on top of `events_local`, shard by shop (`tenant_id`), load 5M events through it, and point the Go services at the whole cluster. (Background: [the ShopStream dataset](../../data/README.md).)

**`events_local` vs `events_all`:** `shop.events_all` is a `Distributed` table that stores nothing itself. It forwards queries to `events_local` (from [Lesson 05](../05-replication/README.md)) on every shard and merges the results:

| Table | Engine | Holds data? | A query returns |
|---|---|---|---|
| `shop.events_local` | `ReplicatedMergeTree` | yes, one shard's rows on each node | this node's rows |
| `shop.events_all` | `Distributed` | no | all rows from all shards |

## About this lesson

### What this lesson teaches
Replication gives you copies; sharding gives you capacity. This lesson adds `shop.events_all`, a `Distributed` table that holds no data but routes reads and writes to `events_local` on each shard.
- **Load through it:** send 5 million events through `events_all` and check how they split across shards.
- **Query from anywhere:** run a cluster-wide query from any node.
- **Shard pruning:** make a single-shop query touch only one shard.
- **Two ways to write:** through the Distributed table, or straight into the local tables.
- **Go on the cluster:** point the ingester and query service at all four nodes.
- **The hard questions:** how to pick a sharding key, what a hot tenant does to it, and whether you need more than one shard at all.

### Concepts you'll learn
| Concept | In one line |
|---|---|
| Two-table pattern | `_local` tables hold the data; a `Distributed` table on top routes queries |
| `Distributed` engine | sends a query to one replica per shard and merges the results on the initiator |
| Sharding key | the expression that decides which shard a row goes to, e.g. `cityHash64(tenant_id)` |
| `_shard_num` | a virtual column showing which shard a row came from |
| Shard pruning | `optimize_skip_unused_shards` skips shards the `WHERE` rules out |
| Foreground vs background distributed inserts | wait for delivery, or queue on the initiator in `system.distribution_queue` |
| Data skew / hot shard | one big tenant can overload the shard it lives on |
| `GLOBAL IN` / `GLOBAL JOIN` | how subqueries over distributed tables have to be written |
| No automatic rebalancing | adding a shard doesn't move old data |

### Goal of the lesson
The goal is to scale ShopStream beyond one machine without slowing down per-shop queries. By the end you should be able to set up the local + Distributed pattern, choose a sharding key that fits your access pattern, and check with `_shard_num` and `query_log` that a tenant query hits only one shard. Just as important, you should be able to argue whether a project needs sharding yet. The usual advice is to start with one shard and two or three replicas, and add shards only when one node's disk or CPU runs out.

## What you'll learn
- The two-table pattern: `events_local` (data) + `events_all` (`Distributed`, routing)
- How to pick a sharding key
- Shard pruning with `optimize_skip_unused_shards`
- Writing to a sharded cluster from Go

## Files in this lesson
| File | Purpose |
|---|---|
| `01_create_distributed.sql` | create `events_all`, empty `events_local` |
| `02_query_distributed.sql` | where the data went, cluster-wide query, shard pruning |
| `../../data/generate_events.sql` | the ShopStream generator, now writing through `events_all` |
| `../../go-client/cmd/ingest`, `cmd/query` | live traffic and the dashboard API against the cluster |

## Before you start
```bash
cd cluster && docker compose up -d --wait
alias ch1='docker compose exec ch-s1r1 clickhouse-client --password learn'
ch1 -q "EXISTS TABLE shop.events_local"     # expect 1 (from Lesson 05)
```

---

## Step 1 — The pattern
```
             shop.events_all  (Distributed: no data, exists on every node)
                /                         \
   shard 01: shop.events_local       shard 02: shop.events_local
          (ReplicatedMergeTree on ch-s1r1/r2)   (on ch-s2r1/r2)
```
- **Reads** on `events_all`: the node you connect to sends the query to one replica per shard and merges the answers.
- **Writes** to `events_all`: rows are split by the sharding key and forwarded to the right shard.

## Step 2 — Create the Distributed table and load data
Look at the table definition in `01_create_distributed.sql`:
```sql
ENGINE = Distributed(prod, shop, events_local, cityHash64(tenant_id))
```
Sharding by `tenant_id` keeps every tenant on one shard.

Run it, then load 5M ShopStream events **through** `events_all` with the usual generator:
```bash
ch1 --queries-file /lessons/06-sharding/01_create_distributed.sql
ch1 --param_db=shop --param_table=events_all --param_rows=5000000 --param_seed=0 \
    --distributed_foreground_insert=1 --queries-file /data/generate_events.sql
ch1 --queries-file /lessons/06-sharding/02_query_distributed.sql --format PrettyCompact
```
`02_query_distributed.sql` shows:
1. Rows and tenants per shard (`_shard_num`). Roughly half each.
2. A query over the whole cluster (`GROUP BY event_type`).
3. A tenant-42 query with `optimize_skip_unused_shards = 1`, then which hosts ran a sub-query. Expected: **one** shard.

## Step 3 — Query from any node
Every node has `events_all`, so any of them can answer:
```bash
docker compose exec ch-s2r2 clickhouse-client --password learn -q "SELECT count() FROM shop.events_all"
```
Same total as from ch-s1r1.

## Step 4 — Direct vs distributed writes
Two ways to write:
- Through `events_all`: simple. An extra hop. If a shard is down, rows queue on the initiator's disk (`system.distribution_queue`).
- Directly into `events_local` on a node you choose: fastest, but you do the routing. Common for high-volume pipelines.

The load in Step 2 used `distributed_foreground_insert = 1`, so the insert waited until the data reached the shards.
The default (`0`) returns sooner and forwards in the background.

## Step 5 — Ingest from Go into the cluster
```bash
cd ../go-client
go run ./cmd/ingest -addrs localhost:9001,localhost:9002,localhost:9003,localhost:9004 -table events_all -duration 60s
```
The client round-robins across all four nodes. Then query:
```bash
go run ./cmd/query -addrs localhost:9001,localhost:9003 -table events_all -tenant 1
```

## Step 6 — Choosing a sharding key
| Key | Good for | Risk |
|---|---|---|
| `rand()` | even spread | every query hits every shard |
| `cityHash64(tenant_id)` | per-tenant queries, tenant-local JOINs | one huge tenant = hot shard |
| `cityHash64(tenant_id, user_id)` | spread + per-user co-location | per-tenant queries hit all shards |

Check the skew we have:
```bash
ch1 -q "SELECT _shard_num, count() FROM shop.events_all WHERE tenant_id = 1 GROUP BY _shard_num"
```
Tenant 1 (our huge tenant) lives entirely on one shard.

## Step 7 — Do you need shards at all?
One well-sized node scans billions of rows per second. Start with **1 shard × 2–3 replicas**. Add shards when
disk or CPU of one node runs out. ClickHouse doesn't rebalance old data when you add a shard. Your options are
shard weights (new data prefers the new shard), re-inserting into a new table, or moving partitions by hand.

---

## ✅ Checkpoint
- What does a `Distributed` table store on disk?
- Why did the tenant-42 query touch only one shard?
- When would you write directly to `events_local`?

## ⚠️ Gotchas
- A subquery on a distributed table inside `IN`/`JOIN` needs `GLOBAL IN`/`GLOBAL JOIN`. Without it, the default
  `distributed_product_mode = 'deny'` rejects the query (`DISTRIBUTED_IN_JOIN_SUBQUERY_DENIED`). With `'local'`, each
  shard joins against only its own rows, which is correct only when both sides are co-located by the shard key.
- `uniqExact`, big `GROUP BY`s and `ORDER BY … LIMIT` pull data to the initiator. Watch its memory.

## 🏋️ Exercises
1. Recreate `events_all` with `rand()` as the key, reload, rerun the pruning query. How many shards now?
2. Stop both shard-2 nodes. Query `events_all`. Then add `SETTINGS skip_unavailable_shards = 1`.
3. With shard 2 still down, insert via `events_all` (foreground off). Look at `system.distribution_queue`.
   Start shard 2 and watch the queue drain.
