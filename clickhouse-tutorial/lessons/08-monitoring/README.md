# Lesson 08 · Monitoring

⏱ 60 minutes · 🧰 Uses `cluster/` + `cluster/monitoring/` + `go-client/` · ⬅️ [Lesson 07](../07-backup-restore/README.md) · ➡️ [Lesson 09](../09-failure-drills/README.md)

## Objective
Know ShopStream's cluster is unhealthy before shop owners notice, and have the queries ready to find out why.

## ShopStream in this lesson
A provisioned Grafana dashboard shows ShopStream's business view (events, revenue, funnel, top shops) next to cluster health, while the Go ingester sends live traffic. (Background: [the ShopStream dataset](../../data/README.md).)

## What you'll learn
- Scrape ClickHouse metrics with Prometheus and view them in Grafana
- The system-table queries you'll run in every incident
- Which alerts matter
- A synthetic health check in Go

## Files in this lesson
| File | Purpose |
|---|---|
| `health_queries.sql` | 9 incident queries across the whole cluster |
| `../../cluster/monitoring/docker-compose.monitoring.yml` | Prometheus + Grafana |
| `../../cluster/monitoring/prometheus.yml` | scrape config |
| `../../cluster/monitoring/alerts.yml` | alert rules |
| `../../cluster/monitoring/grafana-datasources.yml` | Prometheus + ClickHouse data sources |
| `../../cluster/monitoring/grafana-dashboards.yml` | tells Grafana to load the dashboard below |
| `../../cluster/monitoring/shopstream-dashboard.json` | the ShopStream dashboard: business panels + cluster health |
| `../../go-client/cmd/health/main.go` | health checker (and `-listen` readiness endpoint) |

---

## Step 1 — Start the monitoring stack
```bash
cd cluster
docker compose -f docker-compose.yml -f monitoring/docker-compose.monitoring.yml up -d
alias ch1='docker compose exec ch-s1r1 clickhouse-client --password learn'
```
- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000 (user `admin`, password `learn`)

## Step 2 — Look at raw metrics
Each node exposes Prometheus metrics on port 9363 (enabled by `<prometheus>` in `cluster.xml`):
```bash
curl -s localhost:9363/metrics | grep -E 'ReadonlyReplica|ReplicasMaxAbsoluteDelay|MaxPartCountForPartition'
```
In Prometheus, open **Status → Targets**: all four ClickHouse nodes should be `UP`.

## Step 3 — Put load on the cluster
In another terminal:
```bash
cd go-client
go run ./cmd/ingest -addrs localhost:9001,localhost:9002,localhost:9003,localhost:9004 -table events_all
```
Leave it running for the rest of the lesson.

## Step 4 — Graph something
In Prometheus **Graph**, try:
```
rate(ClickHouseProfileEvents_InsertedRows[1m])
ClickHouseAsyncMetrics_MaxPartCountForPartition
ClickHouseMetrics_Query
```
You should see inserted rows/sec rising on the nodes that receive writes.

## Step 5 — The ShopStream dashboard
The business panels read the per-minute rollup from Lesson 04, which doesn't exist on the cluster yet. Create it
with the cluster's table names (run once):
```bash
ch1 -q "CREATE TABLE shop.events_per_minute ON CLUSTER prod
  (tenant_id UInt32, minute DateTime('UTC'), event_type LowCardinality(String),
   events AggregateFunction(count), users AggregateFunction(uniq, UInt64),
   revenue AggregateFunction(sum, Decimal(12, 2)), p95_dur AggregateFunction(quantile(0.95), UInt32))
  ENGINE = ReplicatedAggregatingMergeTree('/clickhouse/tables/{shard}/shop/events_per_minute', '{replica}')
  ORDER BY (tenant_id, event_type, minute)"
ch1 -q "CREATE MATERIALIZED VIEW shop.mv_events_per_minute ON CLUSTER prod TO shop.events_per_minute AS
  SELECT tenant_id, toStartOfMinute(event_time) AS minute, event_type, countState() AS events,
         uniqState(user_id) AS users, sumState(revenue) AS revenue, quantileState(0.95)(duration_ms) AS p95_dur
  FROM shop.events_local GROUP BY tenant_id, minute, event_type"
```
The MV sits on `events_local`, so each shard rolls up its own rows. (The panels query node ch-s1r1's rollup, so
they show shard 01; making a Distributed table over the rollup is a good extra exercise.)

Open Grafana → **Dashboards → ShopStream on ClickHouse**. Top row: events per minute, revenue per hour, funnel,
top shops. Bottom rows: inserted rows/sec, parts, replication delay, read-only replicas, queries, memory.
With the ingester running, the top-left panel moves every 30 s.

## Step 6 — The incident queries
Open `health_queries.sql`. Run the queries one at a time in `ch1` so you can read each result:
1. Replica health on every node
2. Stuck replication tasks
3. Partitions with too many parts
4. Running merges and unfinished mutations
5. Slowest queries in the last hour
6. Errors
7. Disk space and Keeper connections
8. Distributed insert backlog
9. What's running right now (and how to `KILL QUERY`)

They use `clusterAllReplicas('prod', system.X)` to ask all nodes at once. Save this file somewhere handy: it's your incident toolkit.

## Step 7 — Alerts
Open `cluster/monitoring/alerts.yml`, then Prometheus **Alerts**. All should be green. The ones that matter:

| Alert | Meaning |
|---|---|
| ClickHouseDown | node unreachable |
| ReadonlyReplica | lost Keeper; inserts failing |
| ReplicationLag | a replica is > 5 min behind |
| TooManyParts | inserts too small or merges behind |
| DistributedQueueGrowing | a shard is unreachable |
| DiskAlmostFull | merges need free space |

Trigger one: `docker compose stop ch-s1r2`, wait a minute, and watch `ClickHouseDown` fire. Start it again.

## Step 8 — Synthetic health check
```bash
cd go-client
go run ./cmd/health -addrs localhost:9001,localhost:9002,localhost:9003,localhost:9004
echo "exit code: $?"
```
One line per node per replicated table, `OK` / `LAGGING` / `READONLY` / `DEGRADED`, and exit code 1 if anything
is wrong. Run it with a node stopped to see it fail.

## Step 9 — System log retention
`query_log`, `part_log`, `trace_log` and friends grow forever by default. In production, add TTLs in config:
```xml
<query_log><ttl>event_date + INTERVAL 30 DAY DELETE</ttl></query_log>
```

---

## ✅ Checkpoint
- Which metric tells you inserts are failing because of Keeper?
- Where do you look when a replica is lagging and won't catch up?
- Why does the Go health check connect to each node separately?

## 🏋️ Exercises
1. Run ingest with `-batch 10 -flush 10ms` and watch `MaxPartCountForPartition` climb.
2. Build a Grafana panel of inserted rows/min per host from `system.part_log` using the ClickHouse data source.
3. Wrap `cmd/health` in an HTTP handler that returns 503 when unhealthy (a readiness probe).
   Solution: `go run ./cmd/health -listen :8080 -addrs localhost:9001,localhost:9002` then `curl -i localhost:8080/ready`.
4. Add an alert for "no successful backup in 26 hours" using `system.backups`.
