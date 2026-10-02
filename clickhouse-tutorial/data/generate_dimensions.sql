-- ShopStream dimension tables: shops (tenants) and a few shopper profiles.
-- Usage: clickhouse-client --password learn --param_db=shop --queries-file /data/generate_dimensions.sql

CREATE TABLE IF NOT EXISTS {db:Identifier}.tenants
(
    tenant_id UInt32,
    name      String,
    tier      LowCardinality(String),   -- free / pro / enterprise plan of the shop
    country   LowCardinality(String)
)
ENGINE = MergeTree ORDER BY tenant_id;

TRUNCATE TABLE {db:Identifier}.tenants;

INSERT INTO {db:Identifier}.tenants
SELECT
    number + 1,
    concat(['Acme','Nordic','Sunrise','Blue','Urban','Pixel','Green','Metro'][1 + number % 8],
           ' ', ['Outfitters','Books','Gadgets','Home','Coffee','Sports'][1 + intDiv(number, 8) % 6],
           ' #', toString(number + 1)),
    multiIf(number < 20, 'enterprise', number < 200, 'pro', 'free'),   -- big shops are the low ids
    ['US','DE','IN','BR','GB','FR','JP','ID','NG','CA'][1 + cityHash64(number) % 10]
FROM numbers(1000);
