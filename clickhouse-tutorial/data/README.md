# The ShopStream dataset

Every lesson uses the same story and the same data, so you can follow one dataset from a single table
(Lesson 01) to a replicated, sharded, backed-up cluster (Lessons 05–09).

## The story
You're building **ShopStream**, a SaaS that gives online shops (tenants) a real-time analytics dashboard:
page views, funnels, revenue, top products. Each shop embeds a tracking snippet; every page view, click,
add-to-cart and purchase becomes one row in `shop.events`.

The questions ShopStream's dashboard must answer (these drive every design decision in the tutorial):
1. "How is **my shop** doing this week?" — events, visitors, revenue per day for one tenant.
2. "What does my funnel look like?" — page_view → add_to_cart → purchase per tenant.
3. "Which products sell best?" — revenue per URL.
4. "Show me what shopper X did." — one user's timeline (support tickets).
5. Internal: "Which shops are on which plan, and how much traffic do they send?"

## Tables
| Table | What it is | Created in |
|---|---|---|
| `shop.events` | raw tracking events (the big one) | Lesson 01 |
| `shop.tenants` | the shops: name, plan tier, country | Lesson 04 (via `generate_dimensions.sql`) |
| `shop.users` | shopper profiles that change over time | Lesson 04 |
| `shop.events_per_minute` | rollup feeding the dashboard | Lesson 04 |
| `shop.events_local` / `shop.events_all` | the same events, replicated and sharded | Lessons 05–06 |

## `shop.events` columns
| Column | Example | Meaning |
|---|---|---|
| event_time | 2026-07-03 14:22:05.123 | when it happened (UTC) |
| tenant_id | 1 … 1000 | which shop. Skewed: shop 1 is huge, shop 900 is tiny |
| user_id | 0 … 5M | shopper |
| session_id | UUID | browsing session |
| event_type | page_view, click, search, add_to_cart, purchase, signup | what happened |
| country, device | DE, mobile | where / how |
| url | /product/1234 | 20k products |
| duration_ms | 0 … 30000 | time on page |
| revenue | 129.99 | only for purchases |

## Why synthetic data?
Real e-commerce clickstreams are either behind a login (Kaggle) or too big for a laptop lab. The generators
here are **deterministic** (hash-based), so your numbers match the lesson text, and they reproduce the shape that
matters for learning: skewed tenants, a funnel, random UUIDs, time series.

Want real data too? ClickHouse's docs list public example datasets (web analytics "hits", NYC taxi, UK house
prices) at https://clickhouse.com/docs/getting-started/example-datasets. Once you've finished Lesson 02, try
redesigning one of them with what you learned.

## Generators
| File | Use |
|---|---|
| `generate_events.sql` | insert N events into any table: `--param_db --param_table --param_rows --param_seed` |
| `generate_dimensions.sql` | create and fill `shop.tenants` |

The `data/` folder is mounted into every ClickHouse container at `/data`.
