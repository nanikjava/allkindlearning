-- Lesson 04 / 3: projections (a second sort order inside the same table) and dictionaries.

-- ShopStream question 4: "show me what shopper X did" (support tickets).
-- Our key is (tenant_id, event_type, event_time), so filtering by user_id alone scans everything.
SELECT count() FROM shop.events WHERE user_id = 123456 SETTINGS log_comment = 'l04-proj-before';

-- A projection stores the data (or an aggregate) again, sorted differently, inside each part.
-- The optimizer picks it automatically. Costs: extra disk + insert time.
ALTER TABLE shop.events ADD PROJECTION by_user
(
    SELECT * ORDER BY user_id, event_time
);
ALTER TABLE shop.events MATERIALIZE PROJECTION by_user SETTINGS mutations_sync = 2;

SELECT count() FROM shop.events WHERE user_id = 123456 SETTINGS log_comment = 'l04-proj-after';

SYSTEM FLUSH LOGS;
SELECT log_comment, read_rows, projections
FROM system.query_log
WHERE type = 'QueryFinish' AND log_comment LIKE 'l04-proj%' AND event_time > now() - INTERVAL 5 MINUTE
ORDER BY event_time;

-- Dictionaries: in-memory key->value lookups, much cheaper than JOINs for dimension data.
DROP DICTIONARY IF EXISTS shop.tenants_dict;
-- shop.tenants comes from /data/generate_dimensions.sql (run it first, see README step 5).

CREATE DICTIONARY shop.tenants_dict
(
    tenant_id UInt32,
    name      String,
    tier      String
)
PRIMARY KEY tenant_id
SOURCE(CLICKHOUSE(TABLE 'tenants' DB 'shop' USER 'default' PASSWORD 'learn'))
LAYOUT(FLAT())
LIFETIME(MIN 60 MAX 120);   -- reloads every 1-2 minutes

-- ShopStream question 5: traffic and revenue per plan tier
SELECT dictGet('shop.tenants_dict', 'tier', tenant_id) AS tier, count() AS events, sum(revenue) AS revenue
FROM shop.events
GROUP BY tier ORDER BY revenue DESC;

-- Refreshable MV: recompute a result on a schedule (good for "top N" or heavy joins).
DROP VIEW IF EXISTS shop.top_products;
CREATE MATERIALIZED VIEW shop.top_products
REFRESH EVERY 5 MINUTE
ENGINE = MergeTree ORDER BY tuple()
AS SELECT url, count() AS purchases, sum(revenue) AS revenue
FROM shop.events WHERE event_type = 'purchase'
GROUP BY url ORDER BY revenue DESC LIMIT 100;

SELECT * FROM shop.top_products LIMIT 5;
SELECT view, status, last_success_time, next_refresh_time FROM system.view_refreshes;
