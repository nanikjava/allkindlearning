#!/usr/bin/env bash
# Break and heal the ShopStream cluster. Usage: ./drill.sh <drill> <break|heal>
#   1 replica      one replica of shard 01 dies
#   2 keeper-leader  the current Keeper leader dies
#   3 keeper-quorum  two of three Keepers die
#   4 shard        both replicas of shard 02 die
#   5 disk         ch-s1r2 loses its data volume
#   6 keeper-data  all Keeper data is lost
#   7 truncate     human error: TRUNCATE on the whole cluster
#   8 partition    ch-s1r1 is cut off from the network
set -euo pipefail
cd "$(dirname "$0")/../../../cluster"
ch() { docker compose exec -T "$1" clickhouse-client --password learn -q "$2"; }
leader() { for k in keeper1 keeper2 keeper3; do
  echo mntr | docker compose exec -T "$k" nc -w 2 localhost 9181 2>/dev/null | grep -q 'zk_server_state\s*leader' && echo "$k"; done; }
NET=ch-cluster_default

case "${1:-} ${2:-}" in
  "1 break"|"replica break")           docker compose kill ch-s1r2 ;;
  "1 heal"|"replica heal")             docker compose start ch-s1r2 ;;

  "2 break"|"keeper-leader break")     l=$(leader); echo "killing leader $l"; echo "$l" > /tmp/shopstream-leader; docker compose kill "$l" ;;
  "2 heal"|"keeper-leader heal")       docker compose start "$(cat /tmp/shopstream-leader)" ;;

  "3 break"|"keeper-quorum break")     docker compose kill keeper2 keeper3 ;;
  "3 heal"|"keeper-quorum heal")       docker compose start keeper2 keeper3 ;;

  "4 break"|"shard break")             docker compose kill ch-s2r1 ch-s2r2 ;;
  "4 heal"|"shard heal")               docker compose start ch-s2r1 ch-s2r2 ;;

  "5 break"|"disk break")
    docker compose rm -sf ch-s1r2
    docker volume rm ch-cluster_s1r2
    docker compose up -d --wait ch-s1r2
    echo "ch-s1r2 is back but empty:"; ch ch-s1r2 "SHOW DATABASES" ;;
  "5 heal"|"disk heal")
    ch ch-s1r2 "CREATE DATABASE IF NOT EXISTS shop"
    ddl=$(ch ch-s1r1 "SHOW CREATE TABLE shop.events_local FORMAT TSVRaw")
    ch ch-s1r2 "$ddl"
    ddl=$(ch ch-s1r1 "SHOW CREATE TABLE shop.events_all FORMAT TSVRaw")
    ch ch-s1r2 "$ddl"
    echo "recreated tables; re-fetching data from ch-s1r1..."
    ch ch-s1r2 "SYSTEM SYNC REPLICA shop.events_local"
    ch ch-s1r2 "SELECT count() FROM shop.events_local" ;;

  "6 break"|"keeper-data break")
    docker compose kill keeper1 keeper2 keeper3
    docker compose rm -f keeper1 keeper2 keeper3
    docker volume rm ch-cluster_keeper1 ch-cluster_keeper2 ch-cluster_keeper3
    docker compose up -d --wait keeper1 keeper2 keeper3
    echo "Keeper is empty. Tables are read-only:"; ch ch-s1r1 "SELECT hostName(), is_readonly FROM system.replicas" ;;
  "6 heal"|"keeper-data heal")
    for n in ch-s1r1 ch-s1r2 ch-s2r1 ch-s2r2; do ch "$n" "SYSTEM RESTART REPLICA shop.events_local"; done
    for n in ch-s1r1 ch-s1r2 ch-s2r1 ch-s2r2; do ch "$n" "SYSTEM RESTORE REPLICA shop.events_local" || true; done
    ch ch-s1r1 "SELECT hostName(), is_readonly FROM clusterAllReplicas('prod', system.replicas)" ;;

  "7 break"|"truncate break")          ch ch-s1r1 "TRUNCATE TABLE shop.events_local ON CLUSTER prod SYNC" ;;
  "7 heal"|"truncate heal")
    ch ch-s1r1 "RESTORE TABLE shop.events_local ON CLUSTER prod FROM Disk('backups', 'shop-incr-2') SETTINGS allow_non_empty_tables = 1"
    ch ch-s1r1 "SELECT count() FROM shop.events_all" ;;

  "8 break"|"partition break")         docker network disconnect "$NET" ch-cluster-ch-s1r1-1 ;;
  "8 heal"|"partition heal")           docker network connect "$NET" ch-cluster-ch-s1r1-1 ;;

  *) sed -n '2,11p' "$0"; exit 1 ;;
esac
