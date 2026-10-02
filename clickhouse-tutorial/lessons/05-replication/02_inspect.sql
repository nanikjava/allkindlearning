-- Lesson 05 / step 5: inspect replication after loading ShopStream events on ch-s1r1.

-- Replication status from this node's point of view
SELECT database, table, is_leader, is_readonly, absolute_delay, queue_size,
       active_replicas, total_replicas, replica_path
FROM system.replicas WHERE table = 'events_local' FORMAT Vertical;

-- Count on every node of the cluster: shard 01 replicas have 100k, shard 02 has 0.
SELECT hostName() AS host, count() AS rows
FROM clusterAllReplicas('prod', shop.events_local)
GROUP BY host ORDER BY host;
