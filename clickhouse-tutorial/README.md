# ClickHouse, Hands-On: From One Node to Resilient Infrastructure

A build-it-yourself tutorial. Every lesson has something you run, something you observe, and exercises.
Client code is Go (`clickhouse-go/v2`). Infrastructure is Docker Compose. ClickHouse version: **25.8 LTS**.

## The use case: ShopStream
All lessons follow one project: **ShopStream**, a SaaS that gives 1000 online shops a real-time analytics dashboard
(visits, funnel, revenue, top products). Every lesson works on the same `shop.events` data, created by the same
generator in [`data/`](data/README.md), so you can watch one dataset grow from a single table into a replicated,
sharded, backed-up and monitored cluster.

| Lesson | What happens to ShopStream |
|---|---|
| 01–02 | first 10M events on one node; design the table around the dashboard's queries |
| 03 | Go ingest service and dashboard API |
| 04 | live rollups, shopper profiles, shop plans |
| 05–06 | replicate, then shard by shop |
| 07 | back up, then recover from a dropped table |
| 08 | Grafana dashboard for the business and the cluster |
| 09 | break it while traffic flows |
| 10 | apply the same decisions to your own project |

## How to use this tutorial
Each lesson is its own folder with a `README.md` that walks you through it step by step. Work through them in
order: open the lesson's README, follow its steps, do the checkpoint, try the exercises, then click **Next**.

## Lessons
| # | Lesson | Time | Infra | You learn |
|---|--------|------|-------|-----------|
| 00 | [Mental model](lessons/00-mental-model/README.md) | 20 min | none | Columns, parts, granules, merges, sparse index |
| 01 | [First steps](lessons/01-first-steps/README.md) | 45 min | single-node | MergeTree, loading 10M rows, `system.parts`, `EXPLAIN` |
| 02 | [Schema design](lessons/02-schema-design/README.md) | 60 min | single-node | ORDER BY, types, codecs, skip indexes, TTL |
| 03 | [Go client](lessons/03-go-client/README.md) | 60 min | single-node | Batching, async inserts, retries, typed queries |
| 04 | [Materialized views](lessons/04-materialized-views/README.md) | 60 min | single-node | Rollups, Replacing/Summing engines, projections, dictionaries |
| 05 | [Replication](lessons/05-replication/README.md) | 75 min | cluster | Keeper, ReplicatedMergeTree, ON CLUSTER, quorum |
| 06 | [Sharding](lessons/06-sharding/README.md) | 60 min | cluster | Distributed tables, shard keys, pruning |
| 07 | [Backup & restore](lessons/07-backup-restore/README.md) | 45 min | cluster | Full/incremental backups, restore |
| 08 | [Monitoring](lessons/08-monitoring/README.md) | 60 min | cluster + Prometheus/Grafana | Metrics, incident queries, alerts |
| 09 | [Failure drills](lessons/09-failure-drills/README.md) | 2 h | cluster | Break it, recover, write a runbook |
| 10 | [Production checklist](lessons/10-production-checklist/README.md) | 1–2 h | none | Plan for your project |

Every lesson README follows the same shape: **Objective → ShopStream in this lesson → What you'll learn → Files → Before you start → Steps → Checkpoint → Exercises**.

## Layout

```
data/                 ShopStream dataset description + shared SQL generators (mounted at /data)
single-node/          docker compose for lessons 01-04
cluster/              2 shards x 2 replicas + 3 ClickHouse Keeper nodes (lessons 05-09)
cluster/monitoring/   Prometheus + Grafana overlay (lesson 08)
lessons/NN-*/         one folder per lesson: step-by-step README + its SQL
other-docs/           reference documents that are not lessons (e.g. columnar vs RDBMS)
go-client/            Go module: ingest, query, export, backup, health (from lesson 03 onward)
```

## Prerequisites

- Docker with Compose v2, ~8 GB RAM free for the cluster.
- Go 1.25+.
- Optional: `clickhouse` binary locally. Everything works via `docker compose exec` too.

## Conventions

- Password everywhere is `learn`. Change it before anything leaves your laptop.
- `ch` in lesson text means: `docker compose exec clickhouse clickhouse-client --password learn`
  (single node) or `docker compose exec ch-s1r1 clickhouse-client --password learn` (cluster).
  Add a shell alias.
- Run SQL files with `--queries-file /lessons/<lesson>/<file>.sql` (the lessons folder is mounted at `/lessons`).

## A note on your project

While going through ShopStream, keep asking: *what is my equivalent of `tenant_id`?* — the column nearly every
query filters on. That answer drives ORDER BY (Lesson 02) and the shard key (Lesson 06).
