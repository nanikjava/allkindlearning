# Lesson 10 · Production checklist

⏱ 1–2 hours · 🧰 No setup · ⬅️ [Lesson 09](../09-failure-drills/README.md)

## Objective
Turn everything from Lessons 01–09 into a concrete launch plan for your own project.

## ShopStream in this lesson
You review the decisions made for ShopStream and write down what the equivalent decision is for your project. (Background: [the ShopStream dataset](../../data/README.md).)

## What you'll learn
- How to turn Lessons 01–09 into a plan for **your** project

## Files in this lesson
| File | Purpose |
|---|---|
| `clickhouse-readiness.md` | blank checklist template to copy into your project |
| `../09-failure-drills/runbook-template.md` | runbook template from Lesson 09 |

## ShopStream's answers (worked example)
Before filling in yours, here is what the tutorial decided for ShopStream, so you can see what a filled line looks like:
| Decision | ShopStream | Lesson |
|---|---|---|
| ORDER BY | `(tenant_id, event_time)`: every dashboard query filters by shop, then time | 02 |
| Partition | `toYYYYMM(event_time)`, TTL for retention | 02 |
| Ingest | Go service batching 50k rows / 1 s, retrying identical batches | 03 |
| Dashboard speed | per-minute rollup MV; projection for "one shopper's timeline" | 04 |
| HA | 2 replicas per shard, 3 Keepers | 05 |
| Scale | 2 shards, sharded by `cityHash64(tenant_id)`; watch shop 1 as a hot spot | 06 |
| Backups | full + incremental with `BACKUP … ON CLUSTER`, restore tested | 07 |
| Monitoring | Prometheus alerts + ShopStream Grafana dashboard + Go health check | 08 |
| Runbooks | one per drill | 09 |

## How to use this lesson
1. **Step 1:** copy `clickhouse-readiness.md` from this folder into your project repo. It holds the checklist below as a blank template.
2. **Step 2:** go through each section. Tick what's done, and for each unticked line write who owns it and when.
3. **Step 3:** for any line you don't understand, reread the lesson in brackets.
4. **Step 4:** fill in the capacity estimate at the bottom with your numbers.
5. **Step 5:** review it again before launch and after every major change.

## Topology
- [ ] Start with **1 shard × 2–3 replicas**; add shards only for capacity (06).
- [ ] Replicas in **different AZs/racks**. Keeper: **3 (or 5) dedicated nodes** across AZs, SSD, low latency (05).
- [ ] Prefer `Replicated` database engine for new clusters (auto DDL, easy node replacement).
- [ ] Load balancer / client with multiple addresses (`clickhouse-go` `Addr` list, 03).
- [ ] Or: consider ClickHouse Cloud if you don't want to operate this — compare the ops cost honestly.

## Hardware & OS
- [ ] RAM:disk data ratio ~1:50–1:100 for hot data; ≥ 32 GB RAM per node for real workloads.
- [ ] NVMe/SSD for hot; object storage (`s3` disk, tiered via TTL `TO VOLUME`) for cold.
- [ ] `ulimit -n 262144`, disable transparent huge pages, no swap, `vm.overcommit_memory` per docs.
- [ ] Keep ≥ 20% disk free for merges.

## Schema (02, 04)
- [ ] ORDER BY derived from your real top queries; time last.
- [ ] Monthly (or coarser) partitions; < few hundred partitions per table.
- [ ] Smallest types, `LowCardinality`, no needless `Nullable`, codecs on timestamps.
- [ ] TTL for retention; `ttl_only_drop_parts` if aligned with partitions.
- [ ] Rollups via MVs for dashboards; projections for secondary access paths.
- [ ] Schema changes via versioned migrations (e.g. golang-migrate has a ClickHouse driver), always `ON CLUSTER`
      or Replicated DB.

## Ingestion (03)
- [ ] Batches ≥ 10k rows or ≤ 1 insert/sec/table; else async inserts with `wait_for_async_insert=1`.
- [ ] Durable buffer upstream (Kafka/Redpanda) so a ClickHouse outage doesn't lose events.
- [ ] Retries resend identical blocks (dedup) or use `insert_deduplication_token`.
- [ ] `insert_quorum` if losing seconds of data on a node crash is unacceptable.

## Security
- [ ] Change `default` password; ideally disable `default` for network access. One user per app, least privilege
      (`GRANT SELECT, INSERT ON shop.* TO ingest`), no DROP/TRUNCATE for app users.
- [ ] TLS on 9440/8443; inter-server `secret`; Keeper not exposed publicly.
- [ ] Quotas and settings profiles: `max_memory_usage`, `max_execution_time`, `max_concurrent_queries_for_user`.
- [ ] `max_table_size_to_drop` / `max_partition_size_to_drop` guardrails.

## Operations
- [ ] Backups: daily full + incremental to S3 in another account, object lock, **automated restore tests** (07).
- [ ] Monitoring: Prometheus alerts from 08, synthetic `health` check, system-log TTLs.
- [ ] Runbooks from 09 drills; drills repeated quarterly.
- [ ] Rolling upgrades one replica at a time; stay on **LTS** releases; read the changelog's backward-incompatible
      section; test in staging with production-like data.
- [ ] Capacity plan: rows/day × bytes/row (from `system.parts`) × retention × replicas × 1.3 headroom.

## Capacity estimate (fill in)
| Input | Your value |
|---|---|
| rows per day | |
| bytes per row on disk (from `system.parts`: compressed bytes ÷ rows) | |
| retention (days) | |
| replicas | |
| **disk needed = rows/day × bytes/row × days × replicas × 1.3** | |

## Further reading
- ClickHouse docs: https://clickhouse.com/docs (guides → "Sparse primary indexes", "Replication", "Backup")
- Altinity Knowledge Base: https://kb.altinity.com — the best practical ops resource
- `clickhouse-go` examples: https://github.com/ClickHouse/clickhouse-go/tree/main/examples
