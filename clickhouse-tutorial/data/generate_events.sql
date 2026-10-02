-- ShopStream event generator, shared by every lesson.
--
-- Usage (clickhouse-client params):
--   clickhouse-client --password learn \
--     --param_db=shop --param_table=events --param_rows=10000000 --param_seed=0 \
--     --queries-file /data/generate_events.sql
--
-- Same seed + same rows = same data, so numbers in the lessons match yours.
-- Use a different seed to append "new" days of traffic without duplicating old rows.

INSERT INTO {db:Identifier}.{table:Identifier}
    (event_time, tenant_id, user_id, session_id, event_type, country, device, url, duration_ms, revenue)
SELECT
    -- 90 days of traffic starting 2026-06-01 (shifted by seed so new batches land later)
    toDateTime64('2026-06-01 00:00:00', 3, 'UTC')
        + toIntervalDay({seed:UInt32} * 90)
        + toIntervalMillisecond(cityHash64(n, 0) % (90 * 86400 * 1000))                AS event_time,
    -- 1000 shops (tenants); skewed: shop 1 is a giant, shop 900 is tiny
    toUInt32(1 + floor(pow((cityHash64(n, 1) % 1000000) / 1000000.0, 3) * 1000))      AS tenant_id,
    -- 5M shoppers, namespaced by shop
    cityHash64(n, 2) % 5000000                                                          AS user_id,
    generateUUIDv4(n)                                                                   AS session_id,
    -- funnel-shaped mix: many views, fewer carts, fewer purchases
    ['page_view','page_view','page_view','page_view','click','click',
     'add_to_cart','purchase','signup','search'][1 + cityHash64(n, 3) % 10]             AS event_type,
    ['US','DE','IN','BR','GB','FR','JP','ID','NG','CA'][1 + cityHash64(n, 4) % 10]     AS country,
    ['desktop','mobile','mobile','tablet'][1 + cityHash64(n, 5) % 4]                   AS device,
    concat('/product/', toString(cityHash64(n, 6) % 20000))                            AS url,
    toUInt32(cityHash64(n, 7) % 30000)                                                  AS duration_ms,
    if(event_type = 'purchase', toDecimal64((cityHash64(n, 8) % 50000) / 100, 2), 0)  AS revenue
FROM (SELECT number + {seed:UInt32} * 1000000000 AS n FROM numbers({rows:UInt64}));
