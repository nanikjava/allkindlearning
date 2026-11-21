# ClickHouse tutorial: start, stop and reset the lab environments.
#   make help            list targets
#   make reset           delete ALL data (single node + cluster) and start a fresh single node
#
# Destructive targets ask for confirmation. Skip the prompt with: make reset YES=1

SHELL := /bin/bash
.DEFAULT_GOAL := help

SINGLE  := docker compose -f single-node/docker-compose.yml
CLUSTER := docker compose -f cluster/docker-compose.yml -f cluster/monitoring/docker-compose.monitoring.yml

.PHONY: help confirm \
        single-up single-down single-reset single-client \
        cluster-up cluster-down cluster-reset \
        reset volumes

help: ## list targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

confirm:
	@if [ "$(YES)" != "1" ]; then \
	  read -r -p "This permanently deletes tutorial data. Type 'yes' to continue: " ans; \
	  [ "$$ans" = "yes" ] || { echo "Aborted."; exit 1; }; \
	fi

# ---- single node (Lessons 01-04) ----
single-up: ## start the single node (stop the cluster first: same ports)
	$(SINGLE) up -d --wait

single-down: ## stop the single node, keep its data
	$(SINGLE) down

single-reset: confirm ## delete single-node data and start it empty
	$(SINGLE) down -v --remove-orphans
	$(SINGLE) up -d --wait
	$(SINGLE) exec clickhouse clickhouse-client --password learn -q "SHOW DATABASES"

single-client: ## open clickhouse-client on the single node
	$(SINGLE) exec clickhouse clickhouse-client --password learn

# ---- cluster (Lessons 05-09) ----
cluster-up: ## start the cluster (stop the single node first: same ports)
	docker compose -f cluster/docker-compose.yml up -d --wait

cluster-down: ## stop the cluster and monitoring, keep their data
	$(CLUSTER) down

cluster-reset: confirm ## delete cluster data (replicas, Keepers, backups); leaves it stopped
	$(CLUSTER) down -v --remove-orphans

# ---- everything ----
reset: confirm ## delete ALL data (single node + cluster) and start a fresh single node
	$(MAKE) --no-print-directory cluster-reset YES=1
	$(MAKE) --no-print-directory single-reset YES=1
	@$(MAKE) --no-print-directory volumes

volumes: ## list tutorial Docker volumes
	@docker volume ls --format '{{.Name}}' | grep -E '^ch-(single|cluster)_' || echo "  no tutorial volumes"
