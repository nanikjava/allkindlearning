-- Lesson 06 / step 2: a Distributed table over events_local. Run on any node.

DROP TABLE IF EXISTS shop.events_all ON CLUSTER prod SYNC;

-- Distributed(cluster, database, local_table, sharding_key)
-- Sharding by tenant keeps all of a tenant's rows on one shard:
--   + per-tenant queries touch one shard (with optimize_skip_unused_shards)
--   + JOINs/IN on tenant can run locally (distributed_product_mode / GLOBAL avoided)
--   - a huge tenant makes a hot shard. Alternative: cityHash64(tenant_id, user_id)
CREATE TABLE shop.events_all ON CLUSTER prod AS shop.events_local
ENGINE = Distributed(prod, shop, events_local, cityHash64(tenant_id));

-- Start clean: the next step loads 5M ShopStream events through events_all.
TRUNCATE TABLE shop.events_local ON CLUSTER prod SYNC;

