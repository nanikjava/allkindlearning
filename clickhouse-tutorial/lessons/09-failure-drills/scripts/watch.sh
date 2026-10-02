#!/usr/bin/env bash
# Terminal 2 for every drill: replica health of shop.events_local on all 4 nodes, every 2s.
# Run from anywhere; it cds into cluster/.
cd "$(dirname "$0")/../../../cluster" || exit 1
while true; do
  clear; date
  docker compose exec -T ch-s1r1 clickhouse-client --password learn --format PrettyCompact -q "
    SELECT hostName() AS host, is_readonly AS ro, absolute_delay AS delay_s, queue_size AS queue,
           active_replicas AS active, total_replicas AS total
    FROM clusterAllReplicas('prod', system.replicas) WHERE table = 'events_local' ORDER BY host
    SETTINGS skip_unavailable_shards = 1" 2>&1 | tail -n +1
  docker compose exec -T ch-s1r1 clickhouse-client --password learn -q "
    SELECT 'ShopStream events total: ' || toString(count()) FROM shop.events_all
    SETTINGS skip_unavailable_shards = 1" 2>&1
  sleep 2
done
