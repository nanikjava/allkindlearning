#!/usr/bin/env bash
# Shows which Keeper is leader/follower/down.
cd "$(dirname "$0")/../../../cluster" || exit 1
for k in keeper1 keeper2 keeper3; do
  state=$(echo mntr | docker compose exec -T "$k" nc -w 2 localhost 9181 2>/dev/null | awk '/zk_server_state/ {print $2}')
  echo "$k: ${state:-DOWN}"
done
