-- Lesson 01: the first MergeTree table.
-- Run: docker compose exec clickhouse clickhouse-client --password learn --queries-file /lessons/01-first-steps/01_schema.sql

CREATE DATABASE IF NOT EXISTS shop;

DROP TABLE IF EXISTS shop.events;

CREATE TABLE shop.events
(
    event_time   DateTime64(3, 'UTC'),
    tenant_id    UInt32,
    user_id      UInt64,
    session_id   UUID,
    event_type   LowCardinality(String),   -- ~10 distinct values: dictionary-encoded
    country      LowCardinality(String),
    device       LowCardinality(String),
    url          String,
    duration_ms  UInt32,
    revenue      Decimal(12, 2)
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(event_time)                -- unit of data management (drop/move/backup), NOT an index
ORDER BY (tenant_id, event_type, event_time);   -- physical sort order == sparse primary index
