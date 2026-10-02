# Lesson 02 · Schema design

⏱ 60 minutes · 🧰 Uses `single-node/` with Lesson 01 data · ⬅️ [Lesson 01](../01-first-steps/README.md) · ➡️ [Lesson 03](../03-go-client/README.md)

## Objective
Be able to design a ClickHouse table from a list of queries: pick the sort key, types, codecs, indexes and retention, and prove each choice with measurements.

## ShopStream in this lesson
You test different layouts of `shop.events` against question 1 (one shop, one week), speed up question 4 (one shopper's session) with an index, and set a retention policy for old tracking data. (Background: [the ShopStream dataset](../../data/README.md).)

## What you'll learn
- How to choose `ORDER BY`, the decision you can't cheaply change later
- How data types and codecs change disk usage
- When skip indexes help, and when they don't
- How to manage data lifecycle with partitions and TTL

## Files in this lesson
| File | Experiment |
|---|---|
| `01_order_by.sql` | same data, three sort keys, same query |
| `02_types_codecs.sql` | same column stored different ways |
| `03_skip_indexes.sql` | bloom filter vs useless minmax |
| `04_ttl_and_partitions.sql` | detach/attach partitions, TTL |

## Before you start
```bash
cd single-node
docker compose up -d --wait
alias ch='docker compose exec clickhouse clickhouse-client --password learn'
ch -q "SELECT count() FROM shop.events"      # expect 10000000; if not, redo Lesson 01 steps 3-4
```

---

## Step 1 — Predict first
Before running anything, write down your guess. Our query is "tenant 42, one week". Which table reads the fewest rows?
- A: `ORDER BY event_time`
- B: `ORDER BY (tenant_id, event_time)`
- C: `ORDER BY (event_type, country, device, tenant_id, event_time)`

## Step 2 — Run the ORDER BY experiment
```bash
ch --queries-file /lessons/02-schema-design/01_order_by.sql --format PrettyCompact
```
This copies the 10M rows into three tables (about a minute), then runs the same query on each.

You'll see two tables of results. Roughly:

| table | on disk | read_rows |
|---|---|---|
| ev_lowcard_first (C) | smallest | ~2.2M |
| ev_by_tenant (B) | middle | **~16k** |
| ev_by_time (A) | largest | ~780k |

## Step 3 — Understand the trade-off
- **C is smallest** because low-cardinality columns first make long runs of identical values that compress well.
  But `tenant_id` is 4th in the key, so the index can barely narrow by tenant.
- **A** can only prune by the time range, so it reads the whole week for all tenants.
- **B** puts the always-filtered column first and time last. It wins.

📝 **Rule:** list your top 10 queries. The column that appears in (nearly) all of them goes first in ORDER BY.
Then other frequent filters, lower cardinality first. Time usually goes last.

You can't change ORDER BY in place. The fix is a new table plus `INSERT … SELECT`. For a second access pattern,
use a projection (Lesson 04) instead of compromising the key.

## Step 4 — Types and codecs
```bash
ch --queries-file /lessons/02-schema-design/02_types_codecs.sql --format PrettyCompact
```
The table stores the same values three ways each. Read the `on_disk` column:
- Timestamp: default ≈ 46 MiB, `Delta, ZSTD` ≈ 21 MiB, `DoubleDelta, ZSTD` ≈ 19 MiB.
- Country: `String` > `LowCardinality(String)` > `Enum8`.
- Duration: `UInt32` ≈ 36 MiB, `UInt16` ≈ 19 MiB. Same values, half the space.

📝 **Rules:**
- Smallest integer type that fits. `DateTime` (seconds) unless you need milliseconds.
- `LowCardinality(String)` for under ~10k distinct values.
- Avoid `Nullable` unless NULL means something different from 0/''.
- `Delta`/`DoubleDelta` for timestamps and counters, `T64` for small-range integers, `ZSTD` as the general compressor.

## Step 5 — Skip indexes
```bash
ch --queries-file /lessons/02-schema-design/03_skip_indexes.sql --format PrettyCompact
```
What happens in the script:
1. Look up one `session_id`. It's random and not in ORDER BY, so it reads all 10M rows (`l02-skip-before`).
2. Add a `bloom_filter` index and build it for existing data with `MATERIALIZE INDEX`.
3. Run the same lookup (`l02-skip-after`). Compare `read_rows` in the final table: it should drop dramatically.
4. Add a `minmax` index on `duration_ms`. `EXPLAIN` shows it skips almost nothing, because every granule contains
   the full range of random durations.

📝 **Rule:** a skip index only helps when the value you search for is **rare within a granule**. If you need
many skip indexes, your ORDER BY is probably wrong.

## Step 6 — Partitions and TTL
```bash
ch --queries-file /lessons/02-schema-design/04_ttl_and_partitions.sql --format PrettyCompact
```
Follow along in the file:
1. Rows per partition (one per month).
2. `DETACH PARTITION 202606`: count drops instantly. The files move to `detached/`.
3. `ATTACH PARTITION 202606`: count is back. Both are metadata-only, no data rewritten.
4. `MODIFY TTL … + INTERVAL 60 DAY DELETE` then `MATERIALIZE TTL`: old rows disappear. `min(event_time)` jumps forward.

📝 **Rule:** retention = TTL (or drop old partitions from a cron). `ttl_only_drop_parts = 1` makes TTL cheap
when it lines up with partitions.

---

## ✅ Checkpoint
- Why did the smallest table give the slowest query?
- Which codec would you use for a `created_at` column? For a `temperature Float64`?
- Why did the bloom filter help on `session_id` but minmax didn't help on `duration_ms`?

## 🏋️ Exercises
1. Design an ORDER BY for an app whose top queries are "a user's last 50 events" and "daily active users per tenant".
   One key? Or a key plus a projection?
2. Make a copy of `shop.events` with `url LowCardinality(String)` (20k distinct values). Is it worth it? Measure.
3. **Your project:** write the `CREATE TABLE` for your main table. Justify each ORDER BY column with a query.

## Clean up (optional)
```sql
DROP TABLE shop.ev_by_time; DROP TABLE shop.ev_by_tenant; DROP TABLE shop.ev_lowcard_first;
DROP TABLE shop.ev_codecs; DROP TABLE shop.ev_lifecycle;
```
Keep `shop.events` for Lessons 03 and 04.
