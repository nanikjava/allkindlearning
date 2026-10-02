-- Lesson 08: the queries you'll run during every incident. Save them.

-- 1. Replica health across the cluster
SELECT hostName() AS host, database, table, is_readonly, is_session_expired,
       absolute_delay, queue_size, inserts_in_queue, merges_in_queue, active_replicas, total_replicas
FROM clusterAllReplicas('prod', system.replicas)
ORDER BY absolute_delay DESC, host;

-- 2. Stuck replication tasks
SELECT hostName() AS host, table, type, new_part_name, num_tries, last_exception, postpone_reason
FROM clusterAllReplicas('prod', system.replication_queue)
WHERE num_tries > 3 OR last_exception != ''
ORDER BY num_tries DESC LIMIT 20;

-- 3. Parts pressure (TOO_MANY_PARTS early warning)
SELECT hostName() AS host, database, table, partition, count() AS parts
FROM clusterAllReplicas('prod', system.parts)
WHERE active GROUP BY host, database, table, partition
HAVING parts > 50 ORDER BY parts DESC LIMIT 20;

-- 4. Running merges and mutations
SELECT hostName() AS host, table, round(progress, 2) AS progress, elapsed, formatReadableSize(total_size_bytes_compressed) AS size
FROM clusterAllReplicas('prod', system.merges);
SELECT hostName() AS host, table, mutation_id, command, parts_to_do, latest_fail_reason
FROM clusterAllReplicas('prod', system.mutations) WHERE NOT is_done;

-- 5. Slowest / heaviest queries in the last hour
SELECT hostName() AS host, user, query_duration_ms, formatReadableSize(memory_usage) AS mem,
       read_rows, substring(query, 1, 100) AS q
FROM clusterAllReplicas('prod', system.query_log)
WHERE type = 'QueryFinish' AND event_time > now() - INTERVAL 1 HOUR AND is_initial_query
ORDER BY query_duration_ms DESC LIMIT 10;

-- 6. Errors: failed queries and server-side error counters
SELECT exception_code, count() AS n, any(substring(exception, 1, 120)) AS example
FROM clusterAllReplicas('prod', system.query_log)
WHERE type IN ('ExceptionBeforeStart', 'ExceptionWhileProcessing') AND event_time > now() - INTERVAL 1 DAY
GROUP BY exception_code ORDER BY n DESC;
SELECT hostName() AS host, name, value, last_error_time, last_error_message
FROM clusterAllReplicas('prod', system.errors) WHERE last_error_time > now() - INTERVAL 1 HOUR
ORDER BY last_error_time DESC LIMIT 20;

-- 7. Disks and Keeper connection
SELECT hostName() AS host, name, formatReadableSize(free_space) AS free, formatReadableSize(total_space) AS total
FROM clusterAllReplicas('prod', system.disks);
SELECT hostName() AS host, name, host AS keeper_host, is_expired, connected_time
FROM clusterAllReplicas('prod', system.zookeeper_connection);

-- 8. Distributed insert backlog
SELECT hostName() AS host, database, table, data_files, formatReadableSize(data_compressed_bytes) AS pending, last_exception
FROM clusterAllReplicas('prod', system.distribution_queue);

-- 9. What's running right now (and how to stop it)
SELECT hostName() AS host, query_id, user, elapsed, formatReadableSize(memory_usage) AS mem, substring(query, 1, 80) AS q
FROM clusterAllReplicas('prod', system.processes) ORDER BY elapsed DESC;
-- KILL QUERY ON CLUSTER prod WHERE query_id = '...';
