# Lesson 04 · Materialized views and the MergeTree family

⏱ 60 minutes · 🧰 Uses `single-node/` with Lesson 01 data · ⬅️ [Lesson 03](../03-go-client/README.md) · ➡️ [Lesson 05](../05-replication/README.md)

## Objective
Make ShopStream's dashboard stay fast as data grows by precomputing results, and learn how to handle data that changes without UPDATE.

## ShopStream in this lesson
You build the per-minute rollup behind the live dashboard (questions 1–3), keep shopper profiles up to date (`shop.users`), speed up question 4 with a projection, and answer question 5 with the `shop.tenants` dictionary. (Background: [the ShopStream dataset](../../data/README.md).)

## What you'll learn
- Build real-time rollups with materialized views and `AggregatingMergeTree`
- Model counters with `SummingMergeTree` and updates with `ReplacingMergeTree`
- Add a second access path with a projection
- Use dictionaries instead of JOINs, and refreshable MVs for scheduled results

## Files in this lesson
| File | Topic |
|---|---|
| `01_rollups.sql` | per-minute rollup via MV |
| `02_engines.sql` | SummingMergeTree, ReplacingMergeTree |
| `03_projections_dicts.sql` | projection, dictionary, refreshable MV |
| `../../data/generate_dimensions.sql` | creates and fills `shop.tenants` (the shops) |
| `../../go-client/cmd/ingest` | sends live events for step 3 |
| `../../cluster/monitoring/shopstream-dashboard.json` | the dashboard these rollups feed (Lesson 08) |

## Before you start
```bash
cd single-node && docker compose up -d --wait
alias ch='docker compose exec clickhouse clickhouse-client --password learn'
ch -q "SELECT count() FROM shop.events"
```

---

## Step 1 — Understand what an MV is
A materialized view in ClickHouse is an **insert trigger**. Every block inserted into the source table is run
through the view's `SELECT`, and the result is inserted into a target table.
- It only sees **new** inserts, never existing data.
- It only sees the **block being inserted**, not the whole table.

## Step 2 — Build a per-minute rollup
Open `01_rollups.sql` and read the target table. Columns like `AggregateFunction(uniq, UInt64)` store partial
aggregation **states**, which can be merged later.

Run it:
```bash
ch --queries-file /lessons/04-materialized-views/01_rollups.sql --format PrettyCompact
```
The script:
1. Creates `shop.events_per_minute` (`AggregatingMergeTree`).
2. Creates the MV `shop.mv_events_per_minute` with `countState`, `uniqState`, `sumState`, `quantileState`.
3. **Backfills** existing history once with `INSERT … SELECT` (the MV won't do that).
4. Queries hourly numbers with `countMerge`, `uniqMerge` and so on.
5. Compares sizes: the rollup is a small fraction of the raw table.

## Step 3 — Prove it's real-time
In another terminal, send new data with the Go ingester from Lesson 03:
```bash
cd go-client && go run ./cmd/ingest -duration 15s
```
Then query the latest minutes:
```sql
SELECT minute, countMerge(events) AS events
FROM shop.events_per_minute
WHERE minute > now() - INTERVAL 5 MINUTE
GROUP BY minute ORDER BY minute;
```
New minutes appear as data arrives, with no batch job.

⚠️ Always `GROUP BY` when reading aggregating tables. Rows for the same key are only combined at merge time.

## Step 4 — SummingMergeTree and ReplacingMergeTree
```bash
ch --queries-file /lessons/04-materialized-views/02_engines.sql --format PrettyCompact
```
Look at the output of the `shop.users` part:
- `no FINAL`: user 1 appears twice (free and pro), user 2 twice. Old versions haven't been merged away yet.
- `FINAL`: user 1 is `pro`, user 2 is gone (`is_deleted = 1`). Correct.
- `argMax`: the same result without FINAL, and it scales well.

📝 **Rule:** for data that changes (users, orders, CDC from Postgres) use `ReplacingMergeTree(version, is_deleted)`
and read with `FINAL` or `argMax`. Avoid `ALTER UPDATE`.

## Step 5 — Projections, dictionaries, refreshable MVs
First create the shops table the dictionary reads from:
```bash
ch --param_db=shop --queries-file /data/generate_dimensions.sql
ch -q "SELECT * FROM shop.tenants LIMIT 5"
```
Then:
```bash
ch --queries-file /lessons/04-materialized-views/03_projections_dicts.sql --format PrettyCompact
```
Watch for:
1. **Projection**: `WHERE user_id = 123456` reads all rows before, a few granules after adding
   `PROJECTION by_user (SELECT * ORDER BY user_id, event_time)`. The `projections` column of query_log shows it was used.
   Cost: the data is stored twice.
2. **Dictionary**: `dictGet('shop.tenants_dict', 'tier', tenant_id)` looks up tenant tiers from memory. Faster than a JOIN.
3. **Refreshable MV**: `shop.top_products` recomputes every 5 minutes. `system.view_refreshes` shows its schedule.

## Step 6 — Pick the right tool
| Need | Tool |
|---|---|
| Dashboard over billions of rows, fixed dimensions | MV → AggregatingMergeTree |
| Simple counters/sums per key | MV → SummingMergeTree |
| Mutable entities | ReplacingMergeTree + FINAL/argMax |
| Second access pattern | Projection |
| Small dimension lookup | Dictionary |
| Heavy query, refreshed on schedule | Refreshable MV |

---

## ✅ Checkpoint
- Why did you have to backfill manually?
- What goes wrong if you query `events_per_minute` without `GROUP BY`?
- Two ways to get the latest version of a row in ReplacingMergeTree?

## ⚠️ Gotchas to remember
- If an MV fails, the source insert fails too (the source rows may already be written). Test MVs carefully.
- Each MV adds insert cost. Keep a few per table, not dozens.

## 🏋️ Exercises
1. Build an hourly rollup that reads from `events_per_minute`. Why do you need `-MergeState` combinators?
2. Model an `orders` table fed by CDC where status changes 3 times. Query "orders currently shipped".
3. **Your project:** which dashboard would benefit from a rollup? Write the target table and MV.

## Clean up
You're done with the single node. Stop it before starting the cluster (it uses the same ports):
```bash
cd single-node && docker compose down
```
