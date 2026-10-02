# ShopStream ClickHouse runbook — <failure name>

Copy this file once per drill (e.g. `runbook-keeper-quorum.md`) and fill it from your notes.

## Symptom
What users / shop owners notice (dashboard slow? empty? ingest errors?):

## How it shows up
| Signal | Where | What you see |
|---|---|---|
| Alert | Prometheus (`cluster/monitoring/alerts.yml`) | |
| Query | `lessons/08-monitoring/health_queries.sql` #… | |
| Client log | `go-client/cmd/ingest` output | |
| Health check | `go run ./cmd/health …` | |

## Impact
- Reads: yes / no / partial
- Writes: yes / no / queued
- Data loss risk:

## Actions
1.
2.
3.

## Verify recovery
- [ ] `system.replicas`: `is_readonly = 0`, `absolute_delay` ≈ 0 on every node
- [ ] `system.distribution_queue` empty
- [ ] ShopStream event total still growing (`scripts/watch.sh`)
- [ ] Health check exits 0

## Time to recover measured in the drill:
## Follow-ups (prevent it next time):
