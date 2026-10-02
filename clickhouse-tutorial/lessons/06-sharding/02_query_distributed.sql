-- Lesson 06 / step 4: query the sharded ShopStream data.

-- Where did the data go?
SELECT _shard_num AS shard, count() AS rows, uniqExact(tenant_id) AS tenants
FROM shop.events_all GROUP BY shard ORDER BY shard;

-- ShopStream question 2 across the whole platform (funnel).
-- Query fan-out: each shard aggregates locally, the initiator merges partial results.
SELECT event_type, count(), uniq(user_id) FROM shop.events_all GROUP BY event_type ORDER BY 2 DESC;

-- ShopStream question 1 ("how is my shop doing"): one tenant.
-- Shard pruning: with the sharding key in WHERE, only one shard is queried.
SELECT count() FROM shop.events_all WHERE tenant_id = 42
SETTINGS optimize_skip_unused_shards = 1, log_comment = 'l06-prune';

SYSTEM FLUSH LOGS ON CLUSTER prod;
-- Which hosts ran a sub-query for the pruned query? (Should be one shard.)
SELECT hostName() AS host, count() AS subqueries
FROM clusterAllReplicas('prod', system.query_log)
WHERE log_comment = 'l06-prune' AND type = 'QueryFinish' AND is_initial_query = 0
  AND event_time > now() - INTERVAL 5 MINUTE
GROUP BY host;
