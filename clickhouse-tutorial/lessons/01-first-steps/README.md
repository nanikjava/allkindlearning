# Lesson 01 · First steps

⏱ 45 minutes · 🧰 Uses `single-node/` · ⬅️ [Lesson 00](../00-mental-model/README.md) · ➡️ [Lesson 02](../02-schema-design/README.md)

## Objective
Run ClickHouse locally, load ShopStream's first 10 million tracking events, and learn to measure how much data a query reads, which is the skill every other lesson builds on.

## ShopStream in this lesson
You create `shop.events`, the raw tracking table, and answer ShopStream question 1 ("how is my shop doing this week?") for one shop. (Background: [the ShopStream dataset](../../data/README.md).)

## What you'll learn
- Start ClickHouse with Docker and connect to it
- Create a `MergeTree` table and load 10 million rows
- Read `system.parts` and `system.columns` to see storage and compression
- Use `EXPLAIN indexes = 1` and `system.query_log` to see how much data a query read

## Files in this lesson
| File | What it does |
|---|---|
| `01_schema.sql` | creates database `shop` and table `shop.events` |
| `02_explore.sql` | the queries you'll run in steps 5–9 |
| `../../data/generate_events.sql` | shared ShopStream event generator (used again in Lessons 05–07) |
| `../../single-node/docker-compose.yml` | the ClickHouse container |

---

## Step 1 — Start ClickHouse
From the tutorial root:
```bash
cd single-node
docker compose up -d --wait
```
`--wait` returns once the healthcheck passes. Check it's running:
```bash
docker compose ps
```
You should see `ch-single` with status `healthy`.

## Step 2 — Connect
Make a shortcut for the client (you'll use it in every lesson up to 04):
```bash
alias ch='docker compose exec clickhouse clickhouse-client --password learn'
ch -q "SELECT version()"
```
Expected: `25.8.x.x`.

Now open an interactive session:
```bash
ch
```
You get a `ch-single :)` prompt. Type `SELECT 1;` and press Enter. Type `exit` to leave.

Also try the HTTP interface, which is what most tools and dashboards use:
```bash
curl 'http://default:learn@localhost:8123/?query=SELECT+now()'
```

## Step 3 — Create the table
Open `01_schema.sql` and read it before running it. Notice three things:
- `LowCardinality(String)` for columns with few distinct values (event types, countries).
- `PARTITION BY toYYYYMM(event_time)` → one partition per month.
- `ORDER BY (tenant_id, event_type, event_time)` → how rows are sorted on disk and what the index covers.

Run it:
```bash
ch --queries-file /lessons/01-first-steps/01_schema.sql
ch -q "SHOW CREATE TABLE shop.events"
```

## Step 4 — Load 10 million ShopStream events
Skim `data/generate_events.sql` first. It takes parameters (`{db:Identifier}`, `{rows:UInt64}`…) that you pass on the
command line, so every lesson can reuse it for a different table:
```bash
time ch --param_db=shop --param_table=events --param_rows=10000000 --param_seed=0 \
        --queries-file /data/generate_events.sql
```
This takes about 10 seconds. It builds 90 days of events (June–August 2026) from `numbers()` using hash functions,
so you get the same data every run. Shops are skewed: shop 1 is huge, shop 900 is tiny, like real life.

Check:
```bash
ch -q "SELECT count() FROM shop.events"
```
Expected: `10000000`.

## Step 5 — How big is it?
Open `ch` and run query 1 from `02_explore.sql`:
```sql
SELECT count() AS parts, sum(rows) AS rows,
       formatReadableSize(sum(data_compressed_bytes))   AS on_disk,
       formatReadableSize(sum(data_uncompressed_bytes)) AS uncompressed
FROM system.parts
WHERE database = 'shop' AND table = 'events' AND active;
```
You'll see about **350 MiB on disk** for 10M rows, and a dozen or so parts.

## Step 6 — Which columns cost the most?
Run query 2 (per-column sizes from `system.columns`). Look at the `ratio` column:
- `tenant_id` compresses **>100×**: it's first in ORDER BY, so it's long runs of the same number.
- `session_id` (a random UUID) compresses **~1×** and is the biggest column.

💡 **Lesson:** random high-cardinality columns are what cost you disk. Columns early in ORDER BY are nearly free.

## Step 7 — Watch parts and merges
Run query 3 (parts per partition). Note the numbers. Wait a minute and run it again: the count may drop as
background merges combine parts. You can force it (don't do this routinely in production):
```sql
OPTIMIZE TABLE shop.events FINAL;
```
Run query 3 again. Now there's one part per partition.

## Step 8 — See the index at work
Query 4 is ShopStream question 1 for shop 42: "what happened in my shop in the first week of July?" Run it, then run query 5:
```sql
EXPLAIN indexes = 1
SELECT count() FROM shop.events
WHERE tenant_id = 42 AND event_time >= '2026-07-01' AND event_time < '2026-07-08';
```
Find the `PrimaryKey` section and its `Granules: X/Y` line. Something like `5/422`: ClickHouse will read
5 granules (~40k rows) out of millions.

Now run query 6 (`WHERE country = 'DE'`). The PrimaryKey section says `Granules: 1226/1226`: a full scan,
because `country` is not in ORDER BY.

## Step 9 — Measure what queries actually read
Run query 6b (the real `country = 'DE'` query), then query 7, which reads `system.query_log`.
Compare `read_rows` for the tenant query (~40k) and the country query (10M). This is the habit to build:
**every time you write a query, check `read_rows`.**

---

## ✅ Checkpoint
You're done with this lesson when you can answer:
- How many parts does `shop.events` have right now, and why might that number change by itself?
- Which column is the most expensive to store, and why?
- Why does `WHERE tenant_id = 42` read so much less than `WHERE country = 'DE'`?

## 🏋️ Exercises
1. Write "revenue per country for tenant 1 in July". Check its `read_rows`. Repeat for tenant 900. Why the difference?
2. Insert one row 50 times:
   ```bash
   for i in $(seq 50); do ch -q "INSERT INTO shop.events (tenant_id) VALUES (1)"; done
   ```
   Then count parts again. Imagine 1000 of these per second.
3. Compare `read_bytes` for `SELECT * FROM shop.events LIMIT 10` and `SELECT tenant_id FROM shop.events LIMIT 10`.

## Clean up
Keep the container running; Lesson 02 uses this data. To stop later: `docker compose down` (keeps data) or
`docker compose down -v` (deletes data).
