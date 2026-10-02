# Lesson 03 · Go client

⏱ 60 minutes · 🧰 Uses `single-node/` + `go-client/` · ⬅️ [Lesson 02](../02-schema-design/README.md) · ➡️ [Lesson 04](../04-materialized-views/README.md)

## Objective
Write the Go code ShopStream's ingest and API services would use: efficient, retry-safe inserts and safe, bounded queries.

## ShopStream in this lesson
`cmd/ingest` plays the tracking-snippet backend sending live events into `shop.events`; `cmd/query` plays the dashboard API answering question 1 for a shop; `cmd/export` is the "download my data" button. (Background: [the ShopStream dataset](../../data/README.md).)

## What you'll learn
- Connect from Go with `clickhouse-go/v2` (native protocol, connection pool)
- Insert efficiently: client-side batches vs server-side async inserts
- Retry safely without duplicating data
- Query with typed structs, server-side parameters, per-query limits, and streaming

## Code you'll use
| File | Purpose |
|---|---|
| `go-client/internal/chconn/chconn.go` | builds the connection from flags / env vars |
| `go-client/cmd/ingest/main.go` | event producer: `-mode batch` or `-mode async` |
| `go-client/cmd/query/main.go` | read patterns |
| `go-client/cmd/health/main.go` | replication health check (used in Lessons 08–09) |
| `go-client/cmd/export/main.go` | "download my shop's data" as CSV (solution to exercise 2) |
| `go-client/go.mod`, `go.sum` | pinned dependencies |

## Before you start
```bash
cd single-node && docker compose up -d --wait && cd ..
alias ch='docker compose -f single-node/docker-compose.yml exec clickhouse clickhouse-client --password learn'
ch -q "SELECT count() FROM shop.events"       # table from Lesson 01 must exist
go version                                     # 1.25+
```

---

## Step 1 — Get the dependencies
```bash
cd go-client
go mod tidy
go build ./...
```
If the build fails, fix it before going on (it's a good way to read the code).

## Step 2 — Read the connection code
Open `internal/chconn/chconn.go`. Note:
- `clickhouse.Open` returns a **pool** (`driver.Conn`). Create one per process and share it.
- `Addr` is a list. With several nodes, `ConnOpenRoundRobin` spreads connections and fails over. You'll use this in Lesson 06.
- `Compression: LZ4` shrinks network traffic for inserts.
- Defaults come from env vars `CH_ADDRS`, `CH_USER`, `CH_PASSWORD`, `CH_DB`.

## Step 3 — Batch ingestion
Open `cmd/ingest/main.go` and find `runBatch` and `insertBatch`. The pattern:
1. A producer goroutine generates events into a channel.
2. `runBatch` buffers them and flushes every **50k rows or 1 second**, whichever comes first.
3. `insertBatch` does `PrepareBatch → Append (per row) → Send`. One `Send` = one part.

Run it for 30 seconds:
```bash
go run ./cmd/ingest -mode batch -rate 50000 -duration 30s
```
Every 5 s it logs `sent=… (…/s)`. In another terminal, check how many parts were created:
```bash
ch -q "SELECT event_time, rows FROM system.part_log
       WHERE table='events' AND event_type='NewPart'
       ORDER BY event_time DESC LIMIT 10 FORMAT PrettyCompact"
```
Expected: about one new part per second, each with tens of thousands of rows. That's healthy.

## Step 4 — See what bad batching looks like
```bash
go run ./cmd/ingest -mode batch -rate 2000 -batch 1 -flush 1ms -duration 20s
ch -q "SELECT count() FROM system.parts WHERE table='events' AND active"
ch -q "SELECT count() FROM system.merges"
```
One row per insert creates hundreds of parts and keeps merges busy. Keep going and you'd hit
`TOO_MANY_PARTS` (limit `parts_to_throw_insert`, default 3000).

## Step 5 — Async inserts (when you can't batch)
Find `runAsync`. It sends single-row inserts from 50 goroutines with:
- `async_insert=1`: the **server** buffers small inserts and writes one part per flush.
- `wait_for_async_insert=1`: the call returns only after the data is written. Setting it to 0 is faster but can lose data on a crash.

```bash
go run ./cmd/ingest -mode async -rate 2000 -duration 20s
ch -q "SELECT count() FROM system.parts WHERE table='events' AND active"
ch -q "SELECT flush_time, rows, status FROM system.asynchronous_insert_log
       ORDER BY flush_time DESC LIMIT 5 FORMAT PrettyCompact"
```
Compare the parts count with Step 4: far fewer parts for the same kind of traffic.

📝 **Rule:** batch in your app when one service owns the stream. Use async inserts when there are many small independent writers.

## Step 6 — Retries without duplicates
Read `withRetry` and `retryable` at the bottom of `ingest/main.go`.
- It retries the **exact same batch** with exponential backoff and jitter.
- On replicated tables (Lesson 05), ClickHouse remembers hashes of recent blocks, so resending an identical block is
  ignored. A retry after a lost acknowledgement doesn't duplicate data.
- Errors like syntax or unknown table are not retried.

Try it: while ingest runs, run `docker compose -f ../single-node/docker-compose.yml restart clickhouse`.
Watch the ingester log `attempt 1 failed … retrying`, then recover.

## Step 7 — Querying
Open `cmd/query/main.go`, then run:
```bash
go run ./cmd/query -tenant 1
```
What it shows:
1. **Server-side parameters**: `{tenant:UInt32}` in SQL, values via `clickhouse.WithParameters`. Never `fmt.Sprintf` user input into SQL.
2. **Guardrails**: `max_execution_time` and `max_memory_usage` via `WithSettings`.
3. **Query IDs**: `WithQueryID`, so you can find the query in `system.query_log` or `KILL QUERY` it.
4. **Struct scanning**: `conn.Select(ctx, &[]TypeStat{}, …)` with `ch:"…"` tags.
5. **Streaming**: `conn.Query` + `rows.Next()` keeps memory flat for big results.

Find your query afterwards:
```bash
ch -q "SELECT query_id, read_rows, query_duration_ms FROM system.query_log
       WHERE query_id LIKE 'tutorial-%' AND type='QueryFinish' ORDER BY event_time DESC LIMIT 3"
```

---

## ✅ Checkpoint
- Why does one `Send()` of 50k rows beat 50k single-row inserts?
- When would you pick async inserts over client-side batching?
- Why is retrying the *same* batch important?

## 🏋️ Exercises
1. Stop ClickHouse for 10 seconds during ingest. What happens to events generated meanwhile? (The producer drops on
   back-pressure.) How would you make it durable? Hint: Kafka upstream.
2. Write a program that streams all of shop 1's events to CSV with flat memory. Solution: `go-client/cmd/export`
   (`go run ./cmd/export -tenant 1 -out shop1.csv`). Try it yourself before reading it.
3. Add an `insert_deduplication_token` per batch so retries are safe on a non-replicated table too.
