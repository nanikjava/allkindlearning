# Lesson 05 · Replication with ClickHouse Keeper

⏱ 75 minutes · 🧰 Uses `cluster/` · ⬅️ [Lesson 04](../04-materialized-views/README.md) · ➡️ [Lesson 06](../06-sharding/README.md)

## Objective
Make ShopStream survive the loss of a server: run Keeper, replicate `shop.events`, and understand exactly what consistency you get.

## ShopStream in this lesson
`shop.events` becomes `shop.events_local`, a replicated table, so a crashed node no longer takes the dashboard down. (Background: [the ShopStream dataset](../../data/README.md).)

**Why `_local`?** In Lessons 01–04 there was one server, so `shop.events` held every row. On a cluster, each node has its own copy of the table, and that copy holds only the rows of the node's shard. The `_local` suffix is the usual ClickHouse naming convention for this: the table that stores data on each node. Querying `shop.events_local` returns only the rows on the node you're connected to, not the whole dataset.

## About this lesson

### What this lesson teaches
From one server to a real cluster: three ClickHouse Keeper nodes and four ClickHouse nodes, arranged as two shards of two replicas.
- **Read the config:** macros, the cluster definition and the Keeper addresses, so none of it is a mystery.
- **Create a replicated table:** `shop.events_local` as a `ReplicatedMergeTree` on every node, with one `ON CLUSTER` statement.
- **Watch replication:** write to one replica and see the rows appear on the other.
- **Write anywhere:** write to the second replica to see that any replica accepts writes.
- **Catch up:** pause a replica, load data, and watch it catch up through its replication queue.
- **Consistency settings:** quorum inserts, `SYNC REPLICA`, and the block deduplication that makes Lesson 03's retries safe.
- **Look inside Keeper:** what it stores, and what it doesn't.

### Concepts you'll learn
| Concept | In one line |
|---|---|
| ClickHouse Keeper | a Raft-based coordination service (ZooKeeper-compatible) that needs a majority to work |
| Macros (`{shard}`, `{replica}`) | per-node values that let the same DDL run everywhere |
| `ON CLUSTER` DDL | a schema change that Keeper distributes to every node |
| `ReplicatedMergeTree` | a MergeTree that copies parts between replicas sharing the same Keeper path |
| Keeper path vs replica name | same path = same data; unique replica name = a different copy |
| Multi-leader writes | any replica accepts inserts; the others fetch the new parts |
| Replication queue and delay | `system.replication_queue` and `absolute_delay` show how far behind a replica is |
| `insert_quorum` | wait until N replicas have the data: stronger durability, less availability |
| `SYSTEM SYNC REPLICA` | wait until this replica has caught up, to read your own writes |
| Insert deduplication | identical blocks are recognised by hash and written only once |

### Goal of the lesson
The goal is a ShopStream that keeps serving dashboards and accepting events when one server dies, and a clear idea of what consistency that gives you. By the end you should be able to run and inspect a replicated cluster, explain the Keeper path in a `ReplicatedMergeTree` definition, and decide whether a workload needs `insert_quorum` or is fine with the default asynchronous replication. You should also be able to say exactly what lives in Keeper (metadata) and what doesn't (your data). Lesson 06 builds sharding on top of this cluster.

## What you'll learn
- Run a 3-node ClickHouse Keeper ensemble and a 4-node ClickHouse cluster
- How `macros` and `ON CLUSTER` DDL work
- Create `ReplicatedMergeTree` tables and watch parts replicate
- Consistency knobs: `insert_quorum`, `SYSTEM SYNC REPLICA`, insert deduplication

## Files in this lesson
| File | Purpose |
|---|---|
| `01_create_replicated.sql` | create `shop.events_local` on every node |
| `02_inspect.sql` | replication status and row counts per node |
| `03_consistency.sql` | quorum inserts, dedup, browse Keeper |
| `../../data/generate_events.sql` | the same ShopStream generator from Lesson 01 |
| `../../cluster/docker-compose.yml` | 3 Keeper + 4 ClickHouse containers |
| `../../cluster/keeper/keeper.xml` | Keeper Raft config |
| `../../cluster/clickhouse/config.d/cluster.xml` | cluster layout, macros, Keeper addresses |

## The topology you're building
```
            keeper1  keeper2  keeper3        (Raft; needs 2 of 3 up)
                \       |       /
   shard 01:  ch-s1r1 <--> ch-s1r2
   shard 02:  ch-s2r1 <--> ch-s2r2
```
Host ports: native 9001–9004, HTTP 8123–8126.

---

## Step 1 — Read the config first
Open `cluster/clickhouse/config.d/cluster.xml` and find:
- `<macros>`: `shard` and `replica` come from env vars set per container in `docker-compose.yml`.
  So ch-s1r2 has `shard=01, replica=ch-s1r2`.
- `<remote_servers><prod>`: the cluster named `prod`, 2 shards × 2 replicas.
  `internal_replication=true` means "write to one replica; replication copies it".
- `<zookeeper>`: the three Keeper nodes.

Open `cluster/keeper/keeper.xml`: each Keeper gets `server_id` from `KEEPER_ID` and knows the other two via `raft_configuration`.

## Step 2 — Start the cluster
Stop the single node first (same ports), then:
```bash
cd single-node && docker compose down && cd ..
cd cluster
docker compose up -d --wait
docker compose ps
```
All 7 containers should be `healthy`.

Aliases for the next lessons:
```bash
alias ch1='docker compose exec ch-s1r1 clickhouse-client --password learn'
alias ch2='docker compose exec ch-s1r2 clickhouse-client --password learn'
alias ch3='docker compose exec ch-s2r1 clickhouse-client --password learn'
```

## Step 3 — Check Keeper and the cluster
```bash
for k in keeper1 keeper2 keeper3; do
  echo -n "$k: "; echo mntr | docker compose exec -T $k nc localhost 9181 | grep zk_server_state
done
```
Expected: one `leader`, two `follower`.

```bash
ch1 -q "SELECT cluster, shard_num, replica_num, host_name FROM system.clusters WHERE cluster='prod'"
ch2 -q "SELECT getMacro('shard'), getMacro('replica')"
```

## Step 4 — Create a replicated table
```bash
ch1 --queries-file /lessons/05-replication/01_create_replicated.sql
```
Look at the `CREATE TABLE` in the file. It's the same ShopStream events schema as Lesson 01, with a new engine:
```sql
ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/shop/events_local', '{replica}')
```
- The **path** is the same for both replicas of a shard, so they know they hold the same data.
- The **replica name** is unique per node.
- `ON CLUSTER prod` ran the DDL on all four nodes through Keeper.

Now load 100k ShopStream events into **ch-s1r1 only**, using the same generator as Lesson 01:
```bash
ch1 --param_db=shop --param_table=events_local --param_rows=100000 --param_seed=0 \
    --queries-file /data/generate_events.sql
ch1 --queries-file /lessons/05-replication/02_inspect.sql --format PrettyCompact
```
`02_inspect.sql` shows `system.replicas` for this node, then counts rows per host. Expected:
```
ch-s1r1  100000
ch-s1r2  100000     ← replicated automatically
ch-s2r1  0          ← different shard
ch-s2r2  0
```

## Step 5 — Write to the other replica
Replicas are multi-leader: any of them accepts writes.
```bash
ch2 --param_db=shop --param_table=events_local --param_rows=1000 --param_seed=1 \
    --queries-file /data/generate_events.sql
ch1 -q "SELECT count() FROM shop.events_local WHERE event_time >= '2026-08-30'"
```
Seed 1 puts these events 90 days later (September onwards), so this counts exactly the rows written on ch-s1r2. Expected: `1000` on ch-s1r1.

## Step 6 — Watch replication catch up
```bash
docker compose pause ch-s1r2
ch1 --param_db=shop --param_table=events_local --param_rows=1000000 --param_seed=2 \
    --queries-file /data/generate_events.sql
docker compose unpause ch-s1r2
ch2 -q "SELECT table, type, new_part_name FROM system.replication_queue"
ch2 -q "SELECT absolute_delay, queue_size FROM system.replicas WHERE table='events_local'"
```
Right after unpausing you may see `GET_PART` tasks in the queue. Run the last query a few times until `queue_size` is 0.

## Step 7 — Consistency knobs
```bash
ch1 --queries-file /lessons/05-replication/03_consistency.sql --format PrettyCompact
```
What to notice:
1. `insert_quorum = 2`: the insert returns only after both replicas have the data.
2. `SYSTEM SYNC REPLICA`: blocks until this replica has caught up. Useful before reading your own writes elsewhere.
3. **Deduplication**: the same row inserted twice → `count() = 1`. This makes Lesson 03's retries safe.
4. `system.zookeeper`: what Keeper stores. Metadata only (`log`, `replicas`, `blocks`…), never your data.

## Step 8 — See the quorum trade-off
```bash
docker compose pause ch-s1r2
ch1 -q "INSERT INTO shop.events_local (event_time, tenant_id, event_type, revenue)
        SETTINGS insert_quorum=2, insert_quorum_timeout=5000
        SELECT now(), 42, 'purchase', 25.00"
docker compose unpause ch-s1r2
```
The insert fails after 5 s: quorum can't be reached with one replica down. Default (no quorum) would have succeeded.

📝 **Rule:** async replication (default) is right for most analytics. Use `insert_quorum` only when losing the last
seconds of writes on a node crash is unacceptable, and accept that one replica down can block writes.

---

## ✅ Checkpoint
- What is stored in Keeper, and what is not?
- Why does the replicated path include `{shard}` but not `{replica}`?
- What does `insert_quorum = 2` buy you, and what does it cost?

## 🏋️ Exercises
1. Stop one Keeper (`docker compose stop keeper2`). Can you still insert? Stop a second one. Now? (Preview of Lesson 09.)
2. Recreate the database with `ENGINE = Replicated('/clickhouse/databases/shop2', '{shard}', '{replica}')` and create a
   table in it on ch-s1r1 **without** `ON CLUSTER`. Does it appear on the other nodes?
3. **Your project:** how many replicas do you need, and in which zones?

Keep the cluster running for Lesson 06.
