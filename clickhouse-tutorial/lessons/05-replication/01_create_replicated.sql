-- Lesson 05 / step 4: create the ShopStream events table as a replicated table. Run on ch-s1r1.

-- ON CLUSTER sends the DDL through Keeper to every node in cluster 'prod'.
CREATE DATABASE IF NOT EXISTS shop ON CLUSTER prod;

DROP TABLE IF EXISTS shop.events_local ON CLUSTER prod SYNC;

CREATE TABLE shop.events_local ON CLUSTER prod
(
    event_time   DateTime64(3, 'UTC') CODEC(Delta, ZSTD(3)),
    tenant_id    UInt32,
    user_id      UInt64,
    session_id   UUID,
    event_type   LowCardinality(String),
    country      LowCardinality(String),
    device       LowCardinality(String),
    url          String,
    duration_ms  UInt32,
    revenue      Decimal(12, 2)
)
-- Keeper path is unique per SHARD; replica name is unique per NODE. Both come from <macros>.
-- {uuid} would make the path unique per table incarnation (needs Atomic db + ON CLUSTER). We keep it explicit.
ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/shop/events_local', '{replica}')
PARTITION BY toYYYYMM(event_time)
ORDER BY (tenant_id, event_time);

-- Who am I and what do the macros say?
SELECT hostName(), getMacro('shard') AS shard, getMacro('replica') AS replica;

