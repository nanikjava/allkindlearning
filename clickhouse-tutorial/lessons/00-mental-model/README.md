# Lesson 00 · The mental model

⏱ 20 minutes · 🧰 No setup · ➡️ Next: [Lesson 01](../01-first-steps/README.md)

## Objective
Build an accurate picture of how ClickHouse stores and reads data, so that every later design choice for ShopStream (sort key, batching, replication) makes sense instead of feeling like a rule to memorise.

## ShopStream in this lesson
Nothing runs yet. You meet ShopStream and its `shop.events` table on paper, and every example below uses it. (Background: [the ShopStream dataset](../../data/README.md).)

## About this lesson

### What this lesson teaches
How ClickHouse works inside, before you type any SQL. Five ideas, in order:
1. Data is stored **by column**, not by row.
2. Every insert writes an **immutable part** that is **merged** later.
3. Rows are **sorted by `ORDER BY`** and found through a **sparse index**.
4. **Partitions** exist to manage data, not to speed up queries.
5. **Replication** and **sharding** are separate layers, coordinated by **Keeper**.

Each idea ends with a "so what?" that turns it into a practical rule (batch your inserts, put the shop ID first in the sort key). Later lessons apply those rules.

### Concepts you'll learn
| Concept | In one line |
|---|---|
| Columnar storage | each column is its own compressed file, so a query reads only the columns it names |
| Part | an immutable, sorted chunk of a table that one `INSERT` writes |
| Merge | background work that combines small parts into bigger ones |
| Mutation | an `ALTER … UPDATE/DELETE` that rewrites whole parts; heavy, avoid when you can |
| Sort key (`ORDER BY`) | the order of rows on disk, and the only thing the primary index covers |
| Granule and sparse index | blocks of about 8192 rows, with one index entry per block, small enough to stay in memory |
| Partition | a group of parts (usually one month) that you can drop, move or back up as a unit |
| Replica, shard, Keeper | a copy of the data, a slice of the data, and the service that coordinates replicas |

### Goal of the lesson
By the end you should be able to predict how ClickHouse behaves before you run anything. Given a query and a table definition, you should be able to say roughly how much data it reads. Given an ingestion pattern, you should be able to say whether it will create too many parts. Given a failure, you should be able to say whether reads, writes or both stop. You've reached the goal when you can answer the self-check without scrolling up. Every later lesson assumes this picture.

## What you'll learn
- Why ClickHouse is fast (and when it isn't)
- What a **part**, **granule** and **merge** are
- Why `ORDER BY` is the most important line in your schema
- What replication and sharding actually do

Every surprise you hit later traces back to one of the five ideas below. Read them, then do the
self-check at the end. Come back here after Lesson 02; it will make more sense the second time.

---

## Step 1 — Columns, not rows
A row database (Postgres, MySQL) stores `row1(all columns), row2(all columns)…`.
ClickHouse stores **each column in its own file**:

```
event_time.bin   tenant_id.bin   user_id.bin   country.bin   ...
```

So what?
- A query touching 3 of 40 columns reads roughly 3/40 of the data.
- Values of one column look alike, so they compress 5–20×.
- `SELECT *` is the most expensive query you can write. Point lookups by id are not what it's built for.

👉 **Remember:** read few columns, over many rows.

## Step 2 — Every INSERT creates a part
An `INSERT` writes a **part**: an immutable folder of sorted, compressed column files.
In the background, **merges** combine small parts into bigger ones (like an LSM tree).

```
INSERT → part_1 (1k rows)
INSERT → part_2 (1k rows)          merge →  part_1_2 (2k rows)
```

So what?
- **Insert in batches** (10k–1M rows, or at most ~1 insert/sec per table). 1000 single-row inserts = 1000
  parts = eventually a `TOO_MANY_PARTS` error.
- `ALTER TABLE … UPDATE/DELETE` rewrite whole parts (they're called **mutations**). They're heavy and asynchronous.
  Lightweight `DELETE FROM` (and lightweight updates in 25.x) only mark rows, so they're cheaper, but deleted rows are
  still physically removed by later merges. Design so you rarely need any of them.

👉 **Remember:** few big inserts, not many small ones.

## Step 3 — Sorted by ORDER BY, indexed sparsely
Inside each part, rows are sorted by the table's `ORDER BY` key. Every 8192 rows (a **granule**), ClickHouse
keeps one index entry: the key of the granule's first row. This index is tiny and lives in memory.

```
granule 0: tenant 1  … tenant 3
granule 1: tenant 3  … tenant 7
granule 2: tenant 7  … tenant 12      WHERE tenant_id = 9  → read only granule 2
```

So what?
- A `WHERE` on the **leading columns** of ORDER BY skips most of the data.
- A `WHERE` on a column not in the key reads everything (fast, but everything).
- The key is **not unique**. It's a sort order, not a constraint.

👉 **Remember:** ORDER BY = the columns you filter on most, most important first.

## Step 4 — Partitions are for managing data, not for speed
`PARTITION BY toYYYYMM(event_time)` splits data into monthly groups. Parts never merge across partitions.
You can drop, move or back up one month cheaply.

So what?
- Too many partitions (by user, by hour over years) means too many parts, which is slow.
- Aim for tens to a few hundred partitions per table.

👉 **Remember:** partition by time for retention, index with ORDER BY.

## Step 5 — Replication and sharding are separate layers
- **Replica** = a full copy of some data. `ReplicatedMergeTree` copies **parts** between replicas.
  **ClickHouse Keeper** (a Raft service) coordinates who has which part. Any replica accepts writes.
  Replication is eventually consistent by default.
- **Shard** = a slice of the data. A `Distributed` table holds no data. It sends a query to one replica of each
  shard and merges the answers.
- If Keeper loses quorum, tables become **read-only**. Reads keep working; writes stop.

```
                 Distributed table (routes)
                  /                  \
        shard 1                       shard 2
   replica A ⇄ replica B        replica C ⇄ replica D
              \        coordinated by        /
                   Keeper 1 · 2 · 3
```

👉 **Remember:** replicas for availability, shards for capacity, Keeper for coordination.

---

## ✅ Self-check
Answer without scrolling up:
1. Why is `SELECT *` expensive in ClickHouse?
2. What happens if your app inserts one row per HTTP request at 500 req/s?
3. Your table is `ORDER BY (tenant_id, event_time)`. Which query is fast: `WHERE tenant_id = 5` or
   `WHERE event_time > now() - 1h`? Why is the other one slower?
4. Keeper goes down. Can users still see dashboards? Can you ingest?

<details><summary>Answers</summary>

1. It reads every column file.
2. ~500 parts/sec → merges can't keep up → `TOO_MANY_PARTS`. Batch or use async inserts (Lesson 03).
3. `tenant_id = 5` uses the index prefix. The time filter can't narrow much because time is the second key column.
4. Dashboards yes (reads work); ingest no (tables go read-only).
</details>

## Glossary
| Term | Meaning |
|---|---|
| part | immutable sorted chunk of a table on disk |
| granule | up to 8192 rows (fewer if rows are wide, via adaptive granularity); the smallest unit ClickHouse reads |
| merge | background combining of parts |
| mutation | `ALTER … UPDATE/DELETE` that rewrites parts |
| Keeper | coordination service for replication and `ON CLUSTER` DDL |
| shard / replica | a slice of the data / a copy of a slice |
