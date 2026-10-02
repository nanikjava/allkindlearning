-- Lesson 04 / 2: other MergeTree family engines.

-- SummingMergeTree: sums numeric columns for rows with the same ORDER BY key during merges.
DROP TABLE IF EXISTS shop.daily_revenue;
CREATE TABLE shop.daily_revenue
(
    day       Date,
    tenant_id UInt32,
    orders    UInt64,
    revenue   Decimal(18, 2)
)
ENGINE = SummingMergeTree
ORDER BY (tenant_id, day);

CREATE MATERIALIZED VIEW IF NOT EXISTS shop.mv_daily_revenue TO shop.daily_revenue AS
SELECT toDate(event_time) AS day, tenant_id, count() AS orders, sum(revenue) AS revenue
FROM shop.events WHERE event_type = 'purchase'
GROUP BY day, tenant_id;

-- ReplacingMergeTree: "upserts". Keeps the row with the highest version per ORDER BY key,
-- eventually (at merge time). Use FINAL or argMax to get correct results before merges.
DROP TABLE IF EXISTS shop.users;
CREATE TABLE shop.users
(
    user_id    UInt64,
    tenant_id  UInt32,
    email      String,
    plan       LowCardinality(String),
    updated_at DateTime64(3, 'UTC'),
    is_deleted UInt8 DEFAULT 0
)
ENGINE = ReplacingMergeTree(updated_at, is_deleted)
ORDER BY (tenant_id, user_id);

INSERT INTO shop.users VALUES (1, 1, 'a@x.io', 'free', '2026-07-01 00:00:00', 0);
INSERT INTO shop.users VALUES (1, 1, 'a@x.io', 'pro',  '2026-07-05 00:00:00', 0);   -- upgrade
INSERT INTO shop.users VALUES (2, 1, 'b@x.io', 'free', '2026-07-01 00:00:00', 0);
INSERT INTO shop.users VALUES (2, 1, 'b@x.io', 'free', '2026-07-09 00:00:00', 1);   -- delete

SELECT 'no FINAL' AS how, * FROM shop.users ORDER BY user_id, updated_at;   -- duplicates visible
SELECT 'FINAL'    AS how, * FROM shop.users FINAL ORDER BY user_id;          -- correct
-- Alternative that scales without FINAL: argMax
SELECT user_id, argMax(plan, updated_at) AS plan
FROM shop.users GROUP BY user_id HAVING argMax(is_deleted, updated_at) = 0;
