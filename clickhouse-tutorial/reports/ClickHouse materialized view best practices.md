# Design ClickHouse Materialized Views Around Insert Blocks

Almost every best practice for ClickHouse materialized views comes from one fact. **An incremental MV is an insert trigger that runs its SELECT only on the block being inserted into the left-most source table. It never re-reads stored data, never sees updates, deletes or merges, and never re-fires when a JOINed table changes** ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view); [Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/)). Several rules follow from that:

- Write through an explicit `TO` target table.
- Store partial aggregate states (`-State` written, `-Merge` read), and always re-aggregate at query time.
- Keep few MVs per table, and only ones that shrink the data.
- Backfill with a time cutoff rather than `POPULATE`.
- Treat aggregates over mutable or CDC data as a job for refreshable MVs (RMVs) over `FINAL`.
- On a cluster, attach MVs to local Replicated tables, with Distributed tables only at the insert and query edges.

The credible literature is concentrated in a dozen official docs pages, a handful of ClickHouse blog posts, three Altinity KB pages plus their 2019–2020 webinar series and a 2026 cluster case study, den-crane's annotated "Everything you should know about Materialized Views" talk, a few GitHub issues, and Tinybird's docs. The full de-duplicated list is at the end of this report.

For a reader on **ClickHouse 25.8**, several defaults in today's docs do not apply yet:

- Dependent-MV deduplication and `async_insert` both became on by default only in 26.2.
- Atomic `POPULATE` arrived in 26.8. (Sources say `max_insert_threads` became auto (`0`) in 26.8, but a 25.8 lab server already reports `0`; check `system.settings` on your own version.)
- `materialized_views_squash_parallel_inserts` arrived in 25.10.
- Timer-less RMV `DEPENDS ON` arrived in 26.6, and `APPEND INCREMENTAL` in 26.9.

Current docs read naively will therefore mislead you about both correctness and performance in your lab.

## Who to trust: official docs for semantics, Altinity for scar tissue, Tinybird for guardrails

The official ClickHouse documentation is now the most current and most complete source on MV semantics. Four pages carry most of the load:

- **[Incremental materialized view](https://clickhouse.com/docs/materialized-view/incremental-materialized-view)** covers mechanics, JOINs, UNION/CTE limits and `parallel_view_processing`.
- **[CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view)** covers DDL, POPULATE, error and dedup semantics, and the full refreshable-MV grammar.
- **[Backfilling data](https://clickhouse.com/docs/data-modeling/backfilling)** covers the cutoff, `_v2` and Null-table patterns.
- **[Deduplicating inserts on retries](https://clickhouse.com/docs/guides/developer/deduplicating-inserts-on-retries)** covers exactly-once delivery through MVs.

Smaller pages fill gaps. The [cascading MVs guide](https://clickhouse.com/docs/guides/developer/cascading-materialized-views), the [tips-and-tricks lessons page](https://clickhouse.com/docs/tips-and-tricks/materialized-views) (the 20 GB→190 GB storage blow-up), [ALTER … MODIFY QUERY](https://clickhouse.com/docs/sql-reference/statements/alter/view), [system.query_views_log](https://clickhouse.com/docs/operations/system-tables/query_views_log) and [system.view_refreshes](https://clickhouse.com/docs/operations/system-tables/view_refreshes) round out the set.

The docs are generated from `src/Core/Settings.cpp`. That file embeds a version history for every default and is the only reliable way to tell what applies to 25.8 versus 26.x ([Settings.cpp master](https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/Settings.cpp)).

Altinity is the second pillar. Its [MV KB page](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/) is the origin of "always use TO, avoid POPULATE, avoid JOINs". Its [idempotent-inserts page](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/idempotent_inserts_mv/) is the canonical explanation of why source and MV inserts are not atomic. Stefan Kaeser's [benchmark](https://altinity.com/blog/performance-impact-of-materialized-views-in-clickhouse) is the only published measurement of how insert throughput degrades with MV count. Diego Nieto's [July 2026 pipeline case study](https://altinity.com/blog/pipeline-optimization-for-clickhouse-distributed-tables-with-synchronous-inserts) is the best real-world account of MVs on a sharded cluster.

Some Altinity material is older and describes behaviour that later versions changed, most notably POPULATE with `TO`: the 2019 "Illuminated" posts and the "Magic of Materialized Views" webinars. Tinybird's [MV best-practices page](https://www.tinybird.co/docs/work-with-data/process-and-copy/materialized-views/best-practices) (updated September 2026) is a credible third-party restatement with stricter guardrails. A cluster of mass-produced OneUptime posts dated 2026-03-31 restate the same material with no added evidence and are not relied on here.

## Every incremental-MV rule follows from "trigger on the inserted block"

### Target-table design: explicit TO, partial states, matched keys

Create the target first, then attach the MV with `TO`. That is Altinity's rule #1. It exists because the implicit `.inner_id.<uuid>` table is hard to manage, and because `CREATE OR REPLACE` on a view without `TO` **drops the inner table and its data**, while with `TO` "the target table and its data are unaffected" ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view); [Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/)). Dropping the MV removes only the trigger ([ClickHouse blog, 2023](https://clickhouse.com/blog/using-materialized-views-in-clickhouse)).

Columns are matched **by name, not position**. Any target column missing from the SELECT silently receives its default, so a misspelled alias writes zeros rather than raising an error. Alias every column explicitly ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view)).

Choose the engine by aggregate type:

- **SummingMergeTree** for plain sums and counts.
- **AggregatingMergeTree** for everything else, with `-State` functions in the MV and `-Merge` at read time. Averaging sub-range averages is wrong, so partial states are mandatory for correctness.
- **`SimpleAggregateFunction(sum|max|min, T)`** for cheap columns that stay readable without `-Merge`.
- **Plain MergeTree** for row transforms or for re-keying a table under a different ORDER BY.

Sources: [incremental MV docs](https://clickhouse.com/docs/materialized-view/incremental-materialized-view); [cascading MVs](https://clickhouse.com/docs/guides/developer/cascading-materialized-views).

The MV's GROUP BY should equal the target's ORDER BY. Misalignment causes "inefficient merges or data discrepancies" ([incremental MV docs](https://clickhouse.com/docs/materialized-view/incremental-materialized-view)). Tinybird goes further and requires every GROUP BY column in the sorting key and nothing else ([Tinybird](https://www.tinybird.co/docs/work-with-data/process-and-copy/materialized-views/best-practices)).

Merges are asynchronous, so **every query on an aggregating target must re-aggregate** with `GROUP BY` plus `sum()` or `-Merge`. Prefer that over `FINAL` on large tables ([ClickHouse blog, 2023](https://clickhouse.com/blog/using-materialized-views-in-clickhouse)).

### Only build MVs that shrink the data

The tips page documents a community case, Kirill Sapchuk's "ClickHouse at CommonRoom" talk, where a **20 GB log table produced a 190 GB MV**. The view emitted one row per attribute, and each log line had about ten attributes. The page's rule: "if a GROUP BY creates more rows than it eliminates, you are building an expensive index" ([tips page](https://clickhouse.com/docs/tips-and-tricks/materialized-views)).

Before creating an MV, run `uniq(group_by_cols)/count()` on the source:

- **Under 10%** means good compression.
- **Over 70%** means reconsider the design.

This matches the 2023 blog's heuristic to aggregate only when rows shrink by at least 10x. The blog also suggests pairing the aggregate with a short TTL on the raw table ([ClickHouse blog](https://clickhouse.com/blog/using-materialized-views-in-clickhouse)). The ratio that actually matters is the ratio *within each insert block*. Small inserts against high-cardinality keys aggregate worse than the table-level number suggests.

### Block-local blind spots: window functions, DISTINCT, UNION ALL, CTEs

Anything that needs global state sees only the current block: window functions, `ORDER BY`, `neighbor`, `DISTINCT`, nested GROUP BY ([Tinybird](https://www.tinybird.co/docs/work-with-data/process-and-copy/materialized-views/best-practices)). A `UNION ALL` MV fires only for its first table, so use one MV per branch writing to a shared target. CTEs are inlined and re-executed on every insert ([incremental MV docs](https://clickhouse.com/docs/materialized-view/incremental-materialized-view)).

### JOINs: allowed, but they rarely do what people expect

The official page is unusually blunt about JOINs ([incremental MV docs](https://clickhouse.com/docs/materialized-view/incremental-materialized-view)):

- Only the left-most table triggers the MV.
- **Right-side tables are read in full on every insert.**
- Changes on the right side "won't retroactively update the materialized view".
- A dimension row that arrives late leaves a permanent null or default.
- Inside the MV, every reference to the source table, including one inside a JOIN subquery, is replaced by the inserted block.

The docs' mitigation is to filter the right side by keys present in the block, e.g. `WHERE Id IN (SELECT UserId FROM badges)`. In their example this cut a single insert **from 7.517 s to 0.583 s**. The docs also recommend inserting dimension data before facts, preferring fully loaded dictionaries over cache layouts, and moving complex JOINs to refreshable MVs.

Altinity calls JOINs in MVs "a very bad idea" for most use cases and recommends dictionaries, `engine=Join` or `engine=Set` instead ([Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/)). Robert Hodges' 2020 post, written against version 20.3, adds three points: use LEFT joins so unmatched rows are not dropped, qualify and alias right-side columns, and to "join" two streams, write both into one AggregatingMergeTree with `argMaxState` ([Altinity, 2020](https://altinity.com/blog/2020-07-14-joins-in-clickhouse-materialized-views)). No 24.x–26.x change to JOIN trigger semantics was found.

## Cascades, fan-in and Null sources trade flexibility for insert latency

### Cascades

Chaining (raw → per-minute → per-10-minutes, or monthly → yearly) works when the coarser level can be derived exactly from the finer one. The downstream MV reads the upstream target's insert blocks and combines states with `-MergeState` (e.g. `uniqMergeState`), or finalizes them with `-Merge` into a SummingMergeTree ([ClickHouse blog: Chaining MVs, Mark Needham, 2024](https://clickhouse.com/blog/chaining-materialized-views); [cascading MVs](https://clickhouse.com/docs/guides/developer/cascading-materialized-views)).

The critical caveat is that **a downstream MV sees the upstream block as inserted, never the merged table**. Replacing, Collapsing or Summing semantics in an upstream target do not propagate down the chain ([cascading MVs](https://clickhouse.com/docs/guides/developer/cascading-materialized-views)). A failure in any hop fails the original INSERT ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view)). Keep `parallel_view_processing=0` whenever views depend on one another ([incremental MV docs](https://clickhouse.com/docs/materialized-view/incremental-materialized-view)).

The chaining blog contains an apparent erratum. Its `byTenMinutes_mv` uses `toStartOfMinute`, while its backfill query uses `toStartOfTenMinutes`; use the latter. This was observed through a fetch-tool summary and is worth re-checking against the page.

### Fan-in

Several MVs can write `TO` one target, for example impressions and clicks into a single `daily_overview`. Each MV emits `0`, or omits the column, for the metrics it doesn't own, and the target uses `SimpleAggregateFunction(sum, UInt64)` columns ([cascading MVs](https://clickhouse.com/docs/guides/developer/cascading-materialized-views)). All contributing MVs must produce the same key grain.

### Null-engine sources

A Null-engine source stores nothing but still fires its MVs. That makes it the standard pattern for:

- ingest, transform and route, using `WHERE` clauses to send valid and invalid rows to different tables;
- testing MV logic;
- memory-safe backfills.

Sources: [ClickHouse blog](https://clickhouse.com/blog/using-materialized-views-in-clickhouse); [Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/). The cost is that there is no raw data left to reprocess when the MV logic turns out to be wrong.

## Backfill with a cutoff and evolve with MODIFY QUERY; on 25.8, POPULATE is still non-atomic

The official backfilling guide recommends against `POPULATE` "for anything other than small datasets where ingest is paused" ([Backfilling](https://clickhouse.com/docs/data-modeling/backfilling)). Altinity agrees: rows inserted during POPULATE are missed, and a manual backfill allows batching, verification and retries ([Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/)).

The current CREATE VIEW page describes a safer POPULATE controlled by `materialized_views_populate_atomically=1` ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view)). It takes a brief exclusive lock and a snapshot, and every concurrent insert is then delivered exactly once. The guarantee covers only inserts on the same server, only MergeTree or Memory sources, and never `CREATE OR REPLACE` or Replicated databases.

**[26.x only] Atomic POPULATE does not exist on 25.8.** That setting first appears in **26.8** ([Settings.cpp](https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/Settings.cpp)). On 25.8, POPULATE is the legacy kind, so concurrent rows "can be missed or duplicated". In all versions, POPULATE is rejected on Replicated databases unless `database_replicated_allow_heavy_create` is set, and it is unsupported in ClickHouse Cloud.

### The default recipe: create with a cutoff, then backfill

The recommended pattern has three steps ([Backfilling](https://clickhouse.com/docs/data-modeling/backfilling)):

1. Create the MV with `WHERE ts >= <near-future cutoff>`.
2. Wait for the cutoff to pass.
3. Run `INSERT INTO target SELECT … WHERE ts < cutoff` using the same `-State` functions, in hour, day or partition chunks.

The `<` versus `>=` boundary is the only thing preventing double counting, so get it exact.

### Variants for heavy or risky loads

- **Null-table backfill.** Insert into a Null table carrying a copy of the MV, so aggregation happens block by block instead of in one huge in-memory GROUP BY.
- **Duplicate-table backfill.** Load `_v2` copies of the source and target, then `ALTER TABLE … MOVE PARTITION` into the live table. The move is lightweight and atomic.
- **No monotonic timestamp.** Pause inserts, take a hard-link snapshot with `ATTACH PARTITION … FROM`, create the MV, resume inserts, then backfill from the snapshot.
- **Memory pressure.** Set `max_threads=1` and `max_insert_threads=1`. In the docs example this cut peak memory from about 640 MiB to about 270 MiB, at the cost of a longer runtime.

Sources: [Backfilling](https://clickhouse.com/docs/data-modeling/backfilling).

Altinity's [controlled-backfill page](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/backfill-populate-mv-in-a-controlled-manner/) adds partition-at-a-time loads into a staging table, followed by `ATTACH PARTITION`. When even one partition's GROUP BY won't fit in RAM, it stages into an AggregatingMergeTree using `initializeAggregation(...)`, so merges do the aggregation work.

### Evolving an MV

Use `ALTER TABLE mv MODIFY QUERY` on TO-style views, in this order ([ALTER VIEW](https://clickhouse.com/docs/sql-reference/statements/alter/view)):

1. ALTER the source and target to add the new columns, optionally with `MODIFY ORDER BY` on the target. This is safe because the MV's columns need not match the target during the transition.
2. Run MODIFY QUERY.

The change applies only to new inserts, so history needs a separate cutoff backfill. For inner-table MVs, MODIFY QUERY is "very limited". With `ON CLUSTER`, the host running the statement must itself have the view, or the statement fails.

Incompatible changes, such as a new grain or new state types, call for a parallel `_v2` target plus MV with a cutoff, a backfill, and then a switch of readers. Renaming a source table while an MV is attached breaks the MV and blocks INSERTs ([Altinity Illuminated Part 2](https://altinity.com/blog/clickhouse-materialized-views-illuminated-part-2)).

## Mutations, CDC and retries break incremental aggregates in predictable ways

### Changes to stored data never reach the MV

The docs state it outright: "any changes to existing data of source table (like update, delete, drop partition, etc.)" do not change the view ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view)). Lightweight UPDATE writes patch parts that are only materialized at merge time ([UPDATE docs](https://clickhouse.com/docs/sql-reference/statements/update)), and lightweight DELETE masks rows via `_row_exists` ([lightweight DELETE](https://clickhouse.com/docs/guides/developer/lightweight-delete)). Neither goes through the insert path.

No page names lightweight UPDATE, lightweight DELETE, TTL or TRUNCATE individually, but all follow from the general rule. The target therefore needs its own TTL, its own mirrored DROP PARTITION, or a periodic recompute of affected partitions.

Projections are the counterpoint: they "are automatically updated and kept in-sync" through merges and mutations. They do not support JOINs, WHERE clauses or a separate TTL, and lightweight updates and deletes are blocked by default on tables that have them ([Projections](https://clickhouse.com/docs/data-modeling/projections)).

### ReplacingMergeTree and CDC sources double count

ClickPipes/PeerDB writes Postgres changes into ReplacingMergeTree:

- An update is a new row with a higher `_peerdb_version`.
- A delete is a new row with `_peerdb_is_deleted=1`.

Source: [ClickPipes dedup](https://clickhouse.com/docs/integrations/clickpipes/postgres/deduplication).

The official CDC modeling post is unambiguous: with incremental MVs, "deduplication cannot occur at insert time". Aggregating new blocks "would lead to duplicate counts", and deleted rows are never reflected ([Postgres→ClickHouse modeling tips v2, 2025](https://clickhouse.com/blog/postgres-to-clickhouse-data-modeling-tips-v2)). Tinybird's docs say the same: "Do not build materialized views with an AggregatingMergeTree on top of a ReplacingMergeTree" ([Tinybird dedup guide](https://www.tinybird.co/docs/guides/deduplication-strategies)).

The supported patterns are:

- **`FINAL` plus `_peerdb_is_deleted=0` at query time**, hidden behind views or row policies.
- **A refreshable MV over `FINAL`**, so the dedup cost is paid once per refresh.
- **A row-level, non-aggregating MV into another ReplacingMergeTree** with the same key and version columns, still read with `FINAL`.

An `argMaxState(col, version)` per key gives you the latest value for each key. It cannot retract that key's contribution to a coarser grain such as per-day counts, so it does not fix double counting.

Sign-weighted CollapsingMergeTree sources can feed additive MV aggregates (`sum(Sign*x)`, `sum(Sign)`), but min, max and uniq cannot be retracted ([CollapsingMergeTree](https://clickhouse.com/docs/reference/engines/table-engines/mergetree-family/collapsingmergetree)). No source covers MVs over Collapsing tables specifically.

### The source insert and the MV inserts are not one transaction

If an MV throws, the INSERT fails, but the source may already hold the block. The official fix is to **retry the same INSERT with insert deduplication enabled for both the source and dependent views** ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view)). `materialized_views_ignore_errors=1` only hides the error. The failing view still ends up with partial data ([MV settings](https://clickhouse.com/docs/reference/settings/session-settings/materialized-views)).

Altinity documents both failure modes ([Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/idempotent_inserts_mv/), citing [#30240](https://github.com/ClickHouse/ClickHouse/issues/30240) and [#56642](https://github.com/ClickHouse/ClickHouse/issues/56642)):

- **MV dedup off.** A retried insert is deduplicated on the Replicated source but skipped in the MV, which leaves the MV permanently missing data.
- **MV dedup on.** Two *different* inserts can produce identical aggregated MV blocks, and the second is falsely dropped: the MV counts 100 rows where the source has 200.

Altinity's fix is a client-supplied `insert_id` column, stored with `any(insert_id)`.

The current official guide says MV block IDs are derived from the source block IDs, so identical MV blocks from different source inserts are *not* deduplicated ([dedup guide](https://clickhouse.com/docs/guides/developer/deduplicating-inserts-on-retries)). That suggests Altinity's false-dedup case reflects older hashing. Verify it in your lab.

### Deduplication on 25.8 differs from today's docs

| Behaviour | 25.8 | 26.x |
|---|---|---|
| `deduplicate_blocks_in_dependent_materialized_views` default | **false** | true since **26.2** ([Settings.cpp](https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/Settings.cpp)) |
| `deduplicate_insert` | Use `insert_deduplicate` (default true) | Enum setting, default `enable` since **26.2** ([deduplicate_insert](https://clickhouse.com/docs/reference/settings/session-settings/deduplicate-insert)) |
| Async-insert dedup with dependent MVs | Unreliable before 26.1 ([26.1 blog](https://clickhouse.com/blog/clickhouse-release-26-01)). `throw_if_deduplication_in_dependent_materialized_views_enabled_with_async_insert` (added 24.3, obsolete 26.1) still applies | Works since 26.1 |
| Dedup hashing | Legacy per-insert hashing | Unified hash for sync and async inserts since **26.8** ([26.8 notes](https://clickhouse.com/docs/resources/changelogs/cloud/release-notes/26_8)) |
| Plain (non-replicated) MergeTree | `non_replicated_deduplication_window=0`, so no dedup at all | Same |

On 25.8, set `deduplicate_blocks_in_dependent_materialized_views=1` in the profile yourself. Don't combine dependent-MV dedup with async inserts. Give each logical batch an explicit `insert_deduplication_token`, and never reuse a token for different data.

## Refreshable MVs are production-ready on 25.8, but 26.x adds the best features

A refreshable MV has no insert trigger. It re-runs its whole SELECT, which may use JOINs, UNIONs or table functions, on a schedule ([CREATE VIEW: refreshable](https://clickhouse.com/docs/sql-reference/statements/create/view#refreshable-materialized-view)):

- **`EVERY`** aligns to clock boundaries and accepts `OFFSET`.
- **`AFTER`** schedules the next run relative to the last completion.
- **`DEPENDS ON`** runs after other RMVs.

In the default REPLACE mode, the new result is **swapped in atomically**, so readers see the old data until the swap. `APPEND` behaves like a plain, non-atomic `INSERT … SELECT`. Only one refresh runs at a time per view, and missed slots are skipped, not queued.

Refresh settings go in `REFRESH … SETTINGS`:

- `refresh_retries`: default 2.
- `refresh_retry_initial_backoff_ms`: default 100, doubling on each retry.
- `refresh_retry_max_backoff_ms`: default 60000.
- `all_replicas`: default false.

`ALTER … MODIFY REFRESH` replaces all of these at once, and anything you omit resets to its default.

### Version history

- **23.12:** introduced as experimental ([23.12 blog](https://clickhouse.com/blog/clickhouse-release-23-12)).
- **24.9:** added `APPEND`, retries and `SYSTEM WAIT VIEW` ([24.9 blog](https://clickhouse.com/blog/clickhouse-release-24-09)).
- **24.10:** declared **production-ready**, with Replicated-database support ([24.10 notes](https://clickhouse.com/docs/resources/changelogs/cloud/release-notes/24_10)).

So everything above works on 25.8 without `allow_experimental_refreshable_materialized_view`.

**[26.x only]** Three features postdate 25.8:

- **26.6 reworked `DEPENDS ON`.** Previously, dependents ran on their own timers and "could skip or lag by a full refresh cycle". From 26.6, a dependent can use `REFRESH DEPENDS ON parent` with no timer ([26.6 blog](https://clickhouse.com/blog/clickhouse-release-26-06)). On 25.8, give the parent and its dependents identical `EVERY` periods, and expect occasional skipped slots.
- **26.9 added `APPEND INCREMENTAL`.** Each refresh reads only the rows committed since the last one. It requires a single MergeTree source with block-number and block-offset columns enabled ([26.9 blog](https://clickhouse.com/blog/clickhouse-release-26-09)).
- **`SYSTEM PAUSE VIEW` appears in the 2026 changelog.** The exact version is unconfirmed ([2026 changelog](https://clickhouse.com/docs/resources/changelogs/oss/2026)).

### When to use RMVs

The official guidance ([best practices](https://clickhouse.com/docs/best-practices/use-materialized-views); [RMV guide](https://clickhouse.com/docs/materialized-view/refreshable-materialized-view)) says to use RMVs when:

- some staleness is acceptable;
- the query is cheap, or runs infrequently (top-N caches, lookup tables);
- JOINs span tables where any side can change;
- you want a dbt-style DAG;
- you need deduplicated CDC snapshots via `FINAL`.

The Medallion pattern is an example: incremental MVs build the Silver layer and RMVs build the Gold layer over `FINAL` ([Medallion blog](https://clickhouse.com/blog/building-a-medallion-architecture-with-clickhouse)).

The guide's rules are "don't schedule a view to be updated every 10 seconds if the query itself takes at least 10 seconds", keep the result smaller than the source, and never let a result set grow without bound. Incremental MVs remain the choice for single-table aggregates, which "scale to potentially petabytes".

### Two operational traps

- **Restart storms.** Uncoordinated RMVs, such as those in an Atomic database, forget their last refresh time on restart and can all fire at once ([KB: RMVs on restart](https://clickhouse.com/docs/resources/support-center/knowledge-base/materialized-views/what-happens-to-refreshable-materialized-views-when-the-server-restarts)). The mitigation is the experimental `stop_refreshable_materialized_views_on_startup` setting, followed by starting views one at a time.
- **Silent missing dependencies.** A `DEPENDS ON` that names a table, a non-refreshable view, or a typo leaves the view stuck in `MissingDependencies` forever ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view#refreshable-materialized-view)).

## Insert cost scales with rows written, so batch and keep MVs few

Stefan Kaeser's Altinity benchmark is the only systematic measurement of MV insert overhead ([Altinity benchmark, 2022, updated 2026](https://altinity.com/blog/performance-impact-of-materialized-views-in-clickhouse)). It used MergeTree sources with 1M-row batches.

| MVs attached | Inserts per second (chained) | Change vs no MV (5.8/s) |
|---|---|---|
| 0 | 5.8 | baseline |
| 1 | 3.7 | about −36% |
| 5 | 1.3 | about −78% |
| 10 | 0.7 | about −88% |
| 50 | 0.15 | about −97% |

Fanned-out views were "nearly identical" to chained ones. Filtered fan-out, where each view keeps one slice, fell only to 1.7/s at 50 views. Null-engine runs showed the trigger itself is cheap; **the rows written dominate the cost**. Two caveats: the benchmark did not test `parallel_view_processing`, and it does not state the ClickHouse version.

### How many MVs is too many

Guidance on the number of views differs by source:

| Source | Guidance |
|---|---|
| 2023 ClickHouse blog | Under about 10 per table ([blog](https://clickhouse.com/blog/using-materialized-views-in-clickhouse)) |
| ClickHouse getting-started blog | "More than 50 is typically excessive" ([blog](https://clickhouse.com/blog/common-getting-started-issues-with-clickhouse)) |
| ClickStack (observability product only) | Under 20 ([ClickStack](https://clickhouse.com/docs/clickstack/managing/materialized-views)) |
| ClickHouse AI-schemas post | "Start with zero materialized views" and profile first ([blog](https://clickhouse.com/blog/ai-generated-clickhouse-schemas-mistakes-and-advice)) |

The benchmark's linear curve is the better guide than any fixed number. **Budget total rows written across all MVs, not the view count.** Many quantile states slow inserts too ([getting-started blog](https://clickhouse.com/blog/common-getting-started-issues-with-clickhouse)).

### Parts multiply too

Every insert block creates at least one part per partition in the source and in every MV target. Batching therefore matters more as you add views. The docs recommend batches of 10k–100k rows at about one insert per second, or async inserts with `wait_for_async_insert=1` ([insert strategy](https://clickhouse.com/docs/best-practices/selecting-an-insert-strategy)). They also say to keep partition-key cardinality under about 1,000 ([getting-started blog](https://clickhouse.com/blog/common-getting-started-issues-with-clickhouse)), and never to partition an MV target more finely than its source. An MV on an async-insert table is evaluated at flush time.

### Parallel view processing

`parallel_view_processing=1` cut one docs example from about 3.8 s to about 1.6 s. The costs are more CPU and memory and no guaranteed arrival order. Use it only for independent views ([incremental MV docs](https://clickhouse.com/docs/materialized-view/incremental-materialized-view)).

### Settings that matter, with 25.8 status

All defaults below are from [Settings.cpp](https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/Settings.cpp) and its embedded version history. Confirm each one on your lab with `SELECT name, value, default, changed FROM system.settings WHERE name ILIKE '%view%' OR name ILIKE '%dedup%' OR name ILIKE 'async_insert%' OR name = 'max_insert_threads'`.

| Setting | On 25.8 | Current (26.10) | Recommendation |
|---|---|---|---|
| `parallel_view_processing` | false | false | Enable only for independent views when CPU is spare |
| `materialized_views_squash_parallel_inserts` | **Absent (added 25.10)** | true | On 25.8, parallel views write one part per insert thread, so keep `max_insert_threads` low when MVs are attached |
| `min_insert_block_size_rows/bytes_for_materialized_views` | 0 | 0 | Raise to squash small MV blocks; the backfill guide says usually leave at default |
| `max_insert_threads` | **0** (verified on a 25.8 lab server; sources claim 1) | 0 / auto (sources say since 26.8) | Upgrades change MV part counts and memory without any config edit, so pin it explicitly |
| `async_insert` | **false** | true since **26.2** (source default) | Pin it explicitly |
| `deduplicate_blocks_in_dependent_materialized_views` | **false** | true since **26.2** | On Replicated sources, set it to 1 and add tokens |
| `materialized_views_ignore_errors` | false | false | Only for `system.*_log`-style pipelines where inserts must never block |
| `materialized_views_populate_atomically` | **Absent** | true since **26.8** | On 25.8, avoid POPULATE |
| `wait_for_part_commit_in_dependent_materialized_views` | **Absent** | false, added **26.6** | Lets cascades that JOIN back to the source see the committed part |
| `use_async_executor_for_materialized_views` | false (since 24.12) | false | Speeds up MV execution at a memory cost |
| `allow_materialized_view_with_bad_select` | false (since 25.4) | false | Leave false |
| `ignore_materialized_views_with_dropped_target_table` | false (since 24.1) | false | Leave false so missing targets surface |
| `parallel_replicas_allow_materialized_views` | Likely false (changed in 25.12) | true | Verify on 25.8 |
| `log_query_views` | true | true | Needed for `query_views_log` |

Two more gaps: no MV-specific `max_threads` exists, and the per-MV squash thresholds' behaviour at 0 (whether they fall back to the regular insert thresholds) is not documented.

### Alternatives when an MV only re-sorts the data

If an MV's only job is to re-sort the same columns, a projection costs a similar double write but is kept in sync and chosen by the optimizer. Since 25.5, projections can store `_part_offset` and act like an index, and since 25.6 a single query can use several projections' primary indexes. Both are available on 25.8 ([Projections](https://clickhouse.com/docs/data-modeling/projections)).

## Monitor views by duration, rows out and parts, not just insert errors

`system.query_views_log` is the per-view surface. For every view execution it records `view_duration_ms`, `read_rows`, `written_rows`, `peak_memory_usage`, `status` and `exception`, linked to the triggering INSERT through `initial_query_id`. It is on by default via `log_query_views=1`, provided the server has a `query_views_log` config section ([query_views_log](https://clickhouse.com/docs/operations/system-tables/query_views_log)). Altinity uses it alongside `system.text_log` to debug MV errors ([Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/)).

The tips page lists the warning signs to watch for after adding an MV ([tips page](https://clickhouse.com/docs/tips-and-tricks/materialized-views)):

- insert latency climbing from about 10 ms to 100 ms or more;
- more frequent "Too many parts" errors;
- CPU spikes during inserts;
- new insert timeouts.

Track them by comparing `system.query_log` INSERT durations before and after the change.

Useful counters include `DelayedInserts`, `RejectedInserts`, `DelayedInsertsMilliseconds`, `AsyncInsertFlush` versus `AsyncInsertQuery`, and `FailedInsertQuery` ([ProfileEvents.cpp](https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Common/ProfileEvents.cpp)). The relevant MergeTree thresholds are `parts_to_delay_insert` at 1000 and `parts_to_throw_insert` at 3000 active parts per partition ([MergeTreeSettings.cpp](https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Storages/MergeTree/MergeTreeSettings.cpp)).

For refreshable MVs, `system.view_refreshes` exposes `status`, `last_success_time`, `last_success_duration_ms`, `next_refresh_time`, `exception`, `retry`, `progress` and `last_refresh_replica` ([view_refreshes](https://clickhouse.com/docs/operations/system-tables/view_refreshes)). Alert when any of these hold:

- `exception` is not empty;
- `status = 'MissingDependencies'`;
- `now() - last_success_time` exceeds about twice the refresh interval;
- `last_success_duration_ms` approaches the interval.

## On clusters, keep MVs local and put Distributed tables only at the edges

An MV fires only on the server where its source table receives the insert. Replication between replicas does **not** re-run MVs; the other replicas fetch the target's parts instead ([Alibaba Cloud](https://www.alibabacloud.com/blog/seven-common-errors-in-clickhouse-materialized-view_601482)). The target must therefore be `Replicated*MergeTree`.

The standard self-managed topology has five pieces:

1. `events_local`: ReplicatedMergeTree, created `ON CLUSTER`.
2. `mv`: created `ON CLUSTER`, `TO agg_local`.
3. `agg_local`: ReplicatedAggregatingMergeTree.
4. A Distributed `events` table over `events_local` for inserts, with `internal_replication=true`.
5. A Distributed `agg` table over `agg_local` for queries.

Sources: [Altinity Illuminated Part 2 comments](https://altinity.com/blog/clickhouse-materialized-views-illuminated-part-2); [PostHog handbook](https://posthog.com/handbook/engineering/clickhouse/replication).

Forget `ON CLUSTER` on an Atomic database and the MV exists on one node only, which shows up as the classic "empty MV" ([Google Groups thread](https://groups.google.com/g/clickhouse/c/xPKvXGC01go)). A Replicated database replicates DDL automatically, but not data or partition operations ([Replicated database](https://clickhouse.com/docs/engines/database-engines/replicated)). Note that its `default_replica_*` settings require 25.10 or later, so they are not available on 25.8.

### Duplicated backfills

Running POPULATE or a backfill on two replicas of the same shard duplicates data. den-crane's advice: "run POPULATE only once at each shard" ([GitHub #9081](https://github.com/ClickHouse/ClickHouse/issues/9081)). Backfill one replica per shard, from that shard's local source.

### MVs that write into Distributed tables

Diego Nieto's 2026 case study ran 2 shards × 2 replicas. Two MVs wrote *into* Distributed tables under `distributed_foreground_insert=1`, and spent about 3.3 s per execution waiting on remote acknowledgements. Repointing the enrichment MV to the `_local` table cut average lag from 8.13 s to 3.87 s. Async Distributed mode had failed earlier, with about 525K spool files ([Altinity, 2026](https://altinity.com/blog/pipeline-optimization-for-clickhouse-distributed-tables-with-synchronous-inserts)). Only route through a Distributed target when rows genuinely need cross-shard placement at write time, as in [cross-cluster migration](https://kb.altinity.com/altinity-kb-setup-and-maintenance/altinity-kb-data-migration/distributed-table-cluster/).

### Per-shard targets hold partial aggregates

Each shard's target holds partial aggregates, so queries through the Distributed target must finalize with `-Merge` and `GROUP BY`. `FINAL` is per shard only. Replacing semantics require the sharding key to co-locate rows that share a dedup key.

### Refreshable MVs on clusters

Put RMVs in a Replicated database with ReplicatedMergeTree targets. Keeper then ensures that one replica runs each refresh ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view#refreshable-materialized-view)). `RANDOMIZE FOR` drifts earlier as you add replicas, because the earliest random pick wins.

ClickHouse Cloud's SharedMergeTree makes engine substitution transparent ([SharedMergeTree](https://clickhouse.com/docs/products/cloud/features/infrastructure/shared-merge-tree)). No official page states exact `ON CLUSTER` semantics on Cloud.

## Where sources disagree

| Topic | Position A | Position B | Resolution |
|---|---|---|---|
| Default sequential MV order | `uuid` order ([incremental MV docs](https://clickhouse.com/docs/materialized-view/incremental-materialized-view)) | Alphabetical ([Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/); [2023 blog](https://clickhouse.com/blog/using-materialized-views-in-clickhouse)) | Trust the current docs. Never rely on ordering; chain explicitly instead. When the change happened is undocumented |
| POPULATE with `TO` | Allowed, appends to target ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view)) | "Doesn't work with TO" ([Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/); [Illuminated Pt 2, 2019](https://altinity.com/blog/clickhouse-materialized-views-illuminated-part-2)) | Version change. Still avoid POPULATE on 25.8 |
| POPULATE safety | Atomic by default ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view)) | "We don't recommend" POPULATE ([Backfilling](https://clickhouse.com/docs/data-modeling/backfilling)) | Atomicity is **26.8+ only**, so the backfilling guide's advice governs 25.8 |
| False dedup of identical MV blocks | Doesn't happen; MV block IDs come from source block IDs ([dedup guide](https://clickhouse.com/docs/guides/developer/deduplicating-inserts-on-retries)) | Happens: 100 vs 200 rows ([Altinity KB](https://kb.altinity.com/altinity-kb-schema-design/materialized-views/idempotent_inserts_mv/)) | Likely older hashing; reproduce on 25.8 before relying on either |
| Async insert + dependent-MV dedup | Works together ([dedup guide](https://clickhouse.com/docs/guides/developer/deduplicating-inserts-on-retries)) | Throws by default ([DEV source analysis](https://dev.to/cheng-w/clickhouse-source-code-analysis-insert-deduplication-3jep)) | The throwing guard was made obsolete in 26.1, so on 25.8 expect the older behaviour |
| Max MVs per table | Under 10 ([2023 blog](https://clickhouse.com/blog/using-materialized-views-in-clickhouse)) | Over 50 excessive ([getting-started blog](https://clickhouse.com/blog/common-getting-started-issues-with-clickhouse)); under 20 ([ClickStack](https://clickhouse.com/docs/clickstack/managing/materialized-views)) | Cost is linear in rows written ([Altinity benchmark](https://altinity.com/blog/performance-impact-of-materialized-views-in-clickhouse)); measure instead |
| `async_insert_max_data_size` | 100 MiB ([insert strategy docs](https://clickhouse.com/docs/best-practices/selecting-an-insert-strategy)) | 10 MiB ([Settings.cpp](https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/Settings.cpp)) | 100 MiB is the Cloud value; OSS is 10 MiB. The getting-started blog's 1 MB / 1 s / 100 values are outdated |
| `max_memory_usage` default | 10 GB ([getting-started blog](https://clickhouse.com/blog/common-getting-started-issues-with-clickhouse)) | 0, unlimited ([Settings.cpp](https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/Settings.cpp)) | 10 GB comes from the packaged `users.xml` profile |
| GROUP BY vs ORDER BY strictness | Should "match" ([docs](https://clickhouse.com/docs/materialized-view/incremental-materialized-view)) | Must be exactly the sorting key ([Tinybird](https://www.tinybird.co/docs/work-with-data/process-and-copy/materialized-views/best-practices)) | Follow Tinybird's stricter rule as the safe default |
| AggregatingMergeTree MV over ReplacingMergeTree | Don't ([Tinybird docs](https://www.tinybird.co/docs/guides/deduplication-strategies)) | `argMaxState` pattern ([Tinybird blog](https://www.tinybird.co/blog/clickhouse-replacingmergetree-example)) | The docs and the official CDC blog win; argMax doesn't fix coarse-grain counts |
| Cascading MVs | Documented pattern ([cascading docs](https://clickhouse.com/docs/guides/developer/cascading-materialized-views); [chaining blog](https://clickhouse.com/blog/chaining-materialized-views)) | Avoid chains ([Tinybird course](https://www.tinybird.co/clickhouse-course/watch/matviews-expert-advice)); "failure-prone" ([BigData Boutique](https://bigdataboutique.com/blog/clickhouse-materialized-views-guide)) | Chain only exactly-derivable rollups; keep chains shallow |
| `APPEND INCREMENTAL` source settings | 2 settings ([CREATE VIEW](https://clickhouse.com/docs/sql-reference/statements/create/view#refreshable-materialized-view)) | 5 settings ([26.9 blog](https://clickhouse.com/blog/clickhouse-release-26-09)) | Moot on 25.8 (26.9+ only) |
| Getting-started blog date | Page shows 2026-02-28 | Described as the 2022 "13 deadly sins" post | Treat as a 2022 post with refreshed dates; some defaults are stale |

## Conclusion

The docs present MVs as a free performance feature. The more useful mental model is that each MV is a second, synchronous write pipeline bolted onto every INSERT. It has its own failure semantics, its own part creation, its own dedup rules, and no awareness of anything except new rows. Seen that way, the 26.x changes are not cosmetic: dedup on by default, squashing of parallel MV parts, atomic POPULATE, part-commit ordering for cascades, and incremental RMV appends. Most of them close correctness gaps that 25.8 users must still close by hand.

On 25.8 that means five things:

- Pin dedup settings explicitly in your profiles.
- Keep `max_insert_threads` at 1 for MV-heavy tables.
- Use cutoff backfills only.
- Use RMVs with equal `EVERY` periods rather than timer-less `DEPENDS ON` chains.
- Treat every upgrade across 26.2 and 26.8 as a behaviour change to re-test, not a drop-in.

Several questions remain open in the literature. No official benchmark of `parallel_view_processing` at scale exists. No release note dates the change in MV ordering, or the shift to source-derived MV dedup hashes. And no cluster-specific MV guide exists from ClickHouse itself. For those, the Altinity material and den-crane's annotated slides remain the best available evidence, and your own `query_views_log` is better still.

---

## Reference list

A de-duplicated list of every credible source the research found. "Not reviewed" means the source was identified but its content was not read in full.

### Official ClickHouse documentation

| Title | URL | What it covers |
|---|---|---|
| Incremental materialized view | https://clickhouse.com/docs/materialized-view/incremental-materialized-view | Trigger semantics, State/Merge, JOINs, UNION/CTE, `parallel_view_processing`, Null engine |
| Refreshable materialized view (guide) | https://clickhouse.com/docs/materialized-view/refreshable-materialized-view | When to use, examples, APPEND, restart warning |
| Best practices: use materialized views | https://clickhouse.com/docs/best-practices/use-materialized-views | Incremental vs refreshable, refresh-cost rule, REPLACE vs APPEND |
| Cascading materialized views | https://clickhouse.com/docs/guides/developer/cascading-materialized-views | Chains, fan-in, "block not table" caveat |
| Backfilling data | https://clickhouse.com/docs/data-modeling/backfilling | Cutoff, `_v2` + MOVE PARTITION, Null-table backfill, memory tuning |
| CREATE VIEW (incl. refreshable) | https://clickhouse.com/docs/sql-reference/statements/create/view | DDL, TO vs inner, POPULATE, errors and dedup, SQL SECURITY, refresh grammar, Replicated coordination |
| ALTER VIEW / MODIFY QUERY | https://clickhouse.com/docs/sql-reference/statements/alter/view | Evolving TO-style MVs |
| Materialized views session settings | https://clickhouse.com/docs/reference/settings/session-settings/materialized-views | ignore_errors, populate_atomically, squash_parallel_inserts |
| Parallel settings (`parallel_view_processing`) | https://clickhouse.com/docs/reference/settings/session-settings/parallel#parallel_view_processing | Setting reference |
| Session settings index | https://clickhouse.com/docs/operations/settings/settings | Index |
| deduplicate_insert setting | https://clickhouse.com/docs/reference/settings/session-settings/deduplicate-insert | enable vs backward_compatible_choice |
| Deduplicating inserts on retries | https://clickhouse.com/docs/guides/developer/deduplicating-inserts-on-retries | Dedup through MVs, tokens, 26.2 change |
| Tips and tricks: materialized views | https://clickhouse.com/docs/tips-and-tricks/materialized-views | 20 GB→190 GB case, aggregation-ratio query, warning signs |
| Tips and tricks: too many parts | https://clickhouse.com/docs/resources/support-center/tips-and-tricks/too-many-parts.md | Related symptom |
| KB: RMVs on server restart | https://clickhouse.com/docs/resources/support-center/knowledge-base/materialized-views/what-happens-to-refreshable-materialized-views-when-the-server-restarts | Restart storms, staggered start |
| system.query_views_log | https://clickhouse.com/docs/operations/system-tables/query_views_log | Per-view metrics |
| system.view_refreshes | https://clickhouse.com/docs/operations/system-tables/view_refreshes | RMV monitoring |
| Selecting an insert strategy | https://clickhouse.com/docs/best-practices/selecting-an-insert-strategy | Batching, async inserts, MV at flush time |
| Partitioning keys | https://clickhouse.com/docs/concepts/best-practices/partitioning-keys | Low-cardinality partitions |
| Projections | https://clickhouse.com/docs/data-modeling/projections | Projections vs MVs |
| MergeTree settings (other) | https://clickhouse.com/docs/reference/settings/merge-tree-settings/other | Projection dedup and mutation modes |
| enable_block_number/offset settings | https://clickhouse.com/docs/reference/settings/merge-tree-settings/enable-block.md | APPEND INCREMENTAL / lightweight UPDATE prerequisites |
| UPDATE (lightweight updates) | https://clickhouse.com/docs/sql-reference/statements/update | Patch parts |
| Lightweight DELETE | https://clickhouse.com/docs/guides/developer/lightweight-delete | `_row_exists` |
| Updating data overview (not reviewed) | https://clickhouse.com/docs/updating-data/overview | Update methods |
| ALTER … APPLY PATCHES (not reviewed) | https://clickhouse.com/docs/reference/statements/alter/apply-patches | Patch materialization |
| CollapsingMergeTree | https://clickhouse.com/docs/reference/engines/table-engines/mergetree-family/collapsingmergetree | Sign-weighted aggregation |
| VersionedCollapsingMergeTree | https://clickhouse.com/docs/reference/engines/table-engines/mergetree-family/versionedcollapsingmergetree | Versioned collapsing |
| ClickPipes Postgres: deduplication | https://clickhouse.com/docs/integrations/clickpipes/postgres/deduplication | FINAL, row policies, RMV snapshots |
| ClickPipes Postgres: ordering keys | https://clickhouse.com/docs/integrations/clickpipes/postgres/ordering_keys | RMV re-keying |
| Distributed table engine | https://clickhouse.com/docs/engines/table-engines/special/distributed | Initiator buffering, internal_replication |
| Replicated database engine | https://clickhouse.com/docs/engines/database-engines/replicated | DDL replication |
| SharedMergeTree | https://clickhouse.com/docs/products/cloud/features/infrastructure/shared-merge-tree | Cloud engine family |
| Cluster deployment guide | https://clickhouse.com/docs/architecture/cluster-deployment | Shard/replica setup |
| ClickStack: materialized views | https://clickhouse.com/docs/clickstack/managing/materialized-views | Product-specific MV limits |
| Cloud release notes 24.10 | https://clickhouse.com/docs/resources/changelogs/cloud/release-notes/24_10 | RMVs production-ready |
| Cloud release notes 26.2 | https://clickhouse.com/docs/resources/changelogs/cloud/release-notes/26_02 | Dedup on by default |
| Cloud release notes 26.8 | https://clickhouse.com/docs/resources/changelogs/cloud/release-notes/26_8 | Unified dedup hash |
| OSS changelog 2026 | https://clickhouse.com/docs/resources/changelogs/oss/2026 | SYSTEM PAUSE VIEW, DEPENDS ON |
| OSS changelog index | https://clickhouse.com/docs/en/whats-new/changelog | 24.9 RMV entries |
| Changelog 25.4 | https://clickhouse.com/docs/changelogs/25.4 | `allow_materialized_view_with_bad_select` change |

### ClickHouse source code (authoritative defaults)

| Title | URL |
|---|---|
| Settings.cpp (master) | https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/Settings.cpp |
| Settings.cpp (v26.10.8.34-stable) | https://raw.githubusercontent.com/ClickHouse/ClickHouse/v26.10.8.34-stable/src/Core/Settings.cpp |
| MergeTreeSettings.cpp | https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Storages/MergeTree/MergeTreeSettings.cpp |
| Defines.h | https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/Defines.h |
| ProfileEvents.cpp | https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Common/ProfileEvents.cpp |
| CurrentMetrics.cpp | https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Common/CurrentMetrics.cpp |
| Releases list | https://api.github.com/repos/ClickHouse/ClickHouse/releases |

### ClickHouse blog

| Title | Author / date | URL |
|---|---|---|
| Using Materialized Views in ClickHouse | Denys Golotiuk, 2023-01-19 | https://clickhouse.com/blog/using-materialized-views-in-clickhouse |
| Chaining Materialized Views in ClickHouse | Mark Needham, 2024-04-16 | https://clickhouse.com/blog/chaining-materialized-views |
| Common getting started issues with ClickHouse | McDiarmid, Schreiber, Genz (page dated 2026-02-28) | https://clickhouse.com/blog/common-getting-started-issues-with-clickhouse |
| AI-generated ClickHouse schemas: mistakes and advice | ClickHouse | https://clickhouse.com/blog/ai-generated-clickhouse-schemas-mistakes-and-advice |
| Postgres to ClickHouse: Data Modeling Tips V2 | Palacin & Srirampur, 2025-03-05 | https://clickhouse.com/blog/postgres-to-clickhouse-data-modeling-tips-v2 |
| Building a Medallion architecture with ClickHouse | ClickHouse, 2024-12 (updated 2026-04) | https://clickhouse.com/blog/building-a-medallion-architecture-with-clickhouse |
| Fast UPDATEs Part 2: SQL-style UPDATEs | ClickHouse, 2025 | https://clickhouse.com/blog/updates-in-clickhouse-2-sql-style-updates |
| UPDATEs Part 3: Benchmarks (not reviewed) | ClickHouse, 2025 | https://clickhouse.com/blog/updates-in-clickhouse-3-benchmarks |
| Cloud boosts performance with SharedMergeTree | ClickHouse | https://clickhouse.com/blog/clickhouse-cloud-boosts-performance-with-sharedmergetree-and-lightweight-updates |
| 10 best practice tips (not reviewed) | ClickHouse | https://clickhouse.com/blog/10-best-practice-tips.md |
| Release 23.12 (RMVs introduced) | 2024-01-10 | https://clickhouse.com/blog/clickhouse-release-23-12 |
| Release 24.9 (RMV APPEND) | 2024-10-03 | https://clickhouse.com/blog/clickhouse-release-24-09 |
| November 2024 newsletter (RMV GA) | 2024-11 | https://clickhouse.com/blog/202411-newsletter |
| Release 26.1 (async dedup with MVs) | 2026 | https://clickhouse.com/blog/clickhouse-release-26-01 |
| Release 26.6 (DEPENDS ON rework) | 2026-07 | https://clickhouse.com/blog/clickhouse-release-26-06 |
| Release 26.9 (APPEND INCREMENTAL) | 2026-09 | https://clickhouse.com/blog/clickhouse-release-26-09 |

### Altinity (KB, blog, webinars)

| Title | Date | URL |
|---|---|---|
| KB: ClickHouse Materialized Views | ongoing | https://kb.altinity.com/altinity-kb-schema-design/materialized-views/ |
| KB: Idempotent inserts into a materialized view | updated 2025-05-23 | https://kb.altinity.com/altinity-kb-schema-design/materialized-views/idempotent_inserts_mv/ |
| KB: Backfill/populate MV in a controlled manner | updated 2024-07-30 | https://kb.altinity.com/altinity-kb-schema-design/materialized-views/backfill-populate-mv-in-a-controlled-manner/ |
| KB: Distributed table to ClickHouse cluster | ongoing | https://kb.altinity.com/altinity-kb-setup-and-maintenance/altinity-kb-data-migration/distributed-table-cluster/ |
| Blog: Performance impact of materialized views (Stefan Kaeser) | 2022-12-05, updated 2026-06-03 | https://altinity.com/blog/performance-impact-of-materialized-views-in-clickhouse |
| Blog: Pipeline optimization for Distributed tables with synchronous inserts (Diego Nieto) | 2026-07-27 | https://altinity.com/blog/pipeline-optimization-for-clickhouse-distributed-tables-with-synchronous-inserts |
| Blog: Using Joins in ClickHouse Materialized Views (Robert Hodges) | 2020-07-14 | https://altinity.com/blog/2020-07-14-joins-in-clickhouse-materialized-views |
| Blog: Materialized Views Illuminated, Part 1 (Robert Hodges) | 2019 | https://altinity.com/blog/clickhouse-materialized-views-illuminated-part-1 |
| Blog: Materialized Views Illuminated, Part 2 (Robert Hodges) | 2019-09-09 | https://altinity.com/blog/clickhouse-materialized-views-illuminated-part-2 |
| Blog tag: materialized views (index) | various | https://altinity.com/blog/tag/materialized-views/ |
| Webinar: High performance, high reliability data loading (not reviewed) | n/d | https://altinity.com/webinarspage/high-performance-high-reliability-data-loading-on-clickhouse |

### Talks, webinars, videos and slides

| Title | Speaker / org, date | URL |
|---|---|---|
| ClickHouse and the Magic of Materialized Views (webinar + slides) | Robert Hodges & Altinity, 2019-06-26 | https://altinity.com/webinarspage/2019-6-26-clickhouse-and-the-magic-of-materialized-views ; https://altinity.com/presentations/2019/6/26/clickhouse-and-the-magic-of-materialized-views ; https://fr.slideshare.net/Altinity/clickhouse-and-the-magic-of-materialized-views-by-robert-hodges-and-altinity-engineering-team |
| Materialized Views Illuminated Part 1 / Part 2 (webinar videos) | Robert Hodges, 2019 | https://www.youtube.com/watch?app=desktop&v=j15dvPGzhyE ; https://www.youtube.com/watch?v=THDk625DGsQ |
| ClickHouse Materialized Views: The Magic Continues | Robert Hodges, 2020-02-26 | https://altinity.com/webinarspage/2020-02-26-clickhouse-materialized-views-the-magic-continues ; https://altinity.com/wp-content/uploads/2024/05/ClickHouse-Materialized-Views-The-Magic-Continues-1.pdf ; https://www.slideshare.net/slideshow/clickhouse-materialized-views-the-magic-continues/229243232 |
| ClickHouse Materialized Views: A Secret Weapon for High Performance Analytics (slides) | Robert Hodges, date unconfirmed | https://de.slideshare.net/Altinity/clickhouse-materialized-views-a-secret-weapon-for-high-performance-analytics-by-robert-hodges-altinity-ceo |
| Everything you should know about Materialized Views (talk + annotated slides) | Denny Crane (den-crane), date unconfirmed | https://www.youtube.com/watch?v=ckChUkC3Pns&t=9353s ; https://den-crane.github.io/Everything_you_should_know_about_materialized_views_commented.pdf |
| Deep Dive on ClickHouse Sharding and Replication (slides; URL slug mismatched) | Altinity, 2022 | https://www.slideshare.net/slideshow/tricks-every-clickhouse-designer-should-know-by-robert-hodges-altinity-ceo/164292253 |
| ClickHouse at CommonRoom (meetup talk) | Kirill Sapchuk, date unconfirmed | https://www.youtube.com/watch?v=liTgGiTuhJE |
| APJ Webinar: Materialized Views | Lokanathan & Chia (ClickHouse), 2026-01 | https://clickhouse.com/videos/202601-APJ-Webinar-Materialized-Views |
| Under-the-Hood: Incremental MVs and Dictionaries | ClickHouse, 2026 | https://clickhouse.com/company/events/202603-AMER-Webinar-MaterializedViews ; https://clickhouse.com/zh/videos/Webinar-MV |
| Materialized views explained (video) | ClickHouse | https://clickhouse.com/videos/materialized-views-explained |
| Backfilling materialized views: a practical guide (video, not reviewed) | ClickHouse | https://clickhouse.com/es/videos/backfill-materialized-view |
| Intro to Refreshable Materialized Views (video, not reviewed) | ClickHouse | https://clickhouse.com/jp/videos/intro-refreshable-materialized-views |
| Release 24.10 webinar slides (RMV history) | Alexey Milovidov, 2024 | https://presentations.clickhouse.com/2024-release-24.10/index.html |

### GitHub issues, PRs, gists, discussions

| Item | URL | Relevance |
|---|---|---|
| #9081 Duplicated data in MATERIALIZED VIEW (2020) | https://github.com/ClickHouse/ClickHouse/issues/9081 | Run POPULATE/backfill once per shard |
| #30240 (dedup proposal) | https://github.com/ClickHouse/ClickHouse/issues/30240 | Cited by Altinity idempotency KB (not opened) |
| #56642 (unexpected MV dedup) | https://github.com/ClickHouse/ClickHouse/issues/56642 | Cited by Altinity idempotency KB (not opened) |
| #7878 (MV ALTER step reordering, 19.16.13+) | https://github.com/ClickHouse/ClickHouse/issues/7878 | Inner-table schema change |
| #1226 (referenced by Altinity KB) | https://github.com/ClickHouse/ClickHouse/issues/1226 | Not verified |
| PR #7533 (referenced by Altinity KB) | https://github.com/ClickHouse/ClickHouse/pull/7533 | Not verified |
| PR #2411 (legacy yandex repo, Distributed + MV) | https://github.com/yandex/ClickHouse/pull/2411 | Not verified |
| Discussion #63490 | https://github.com/ClickHouse/ClickHouse/discussions/63490 | RMVs vs Postgres MVs (context) |
| den-crane gist: MV wrong deduplication | https://gist.github.com/den-crane/bc35a15a8d71899fe15e5ed36668d387 | Dedup edge-case repro |

### Tinybird

| Title | URL |
|---|---|
| Best practices for Materialized Views (updated 2026-09-22) | https://www.tinybird.co/docs/work-with-data/process-and-copy/materialized-views/best-practices |
| Deduplicate data in your data source | https://www.tinybird.co/docs/guides/deduplication-strategies |
| Build a lambda architecture | https://www.tinybird.co/docs/forward/work-with-data/optimize/guides/lambda-architecture |
| ClickHouse course: MV expert advice | https://www.tinybird.co/clickhouse-course/watch/matviews-expert-advice |
| Blog: ClickHouse ReplacingMergeTree example | https://www.tinybird.co/blog/clickhouse-replacingmergetree-example |
| Blog: When not to migrate your data (2025-06) | https://www.tinybird.co/blog/when-not-to-migrate-your-data |
| Blog: Materialized Views – common questions answered (not reviewed) | https://www.tinybird.co/blog-posts/materialized-views-common-questions-answered-sb |
| TOO_MANY_PARTS error explainer | https://www.tinybird.co/docs/sql-reference/clickhouse-errors/TOO_MANY_PARTS |

### Other third-party (credible practitioners and vendors)

| Title | Org / author | URL |
|---|---|---|
| Replacing cron jobs and dbt pipelines with RMVs (2025-11-28) | Aiven, Alexander Fridriksson | https://aiven.io/blog/replacing-cron-jobs-and-dbt-pipelines-with-clickhouse |
| Materialized views in Aiven for ClickHouse (not reviewed) | Aiven | https://aiven.io/blog/materialized-views-in-aiven-for-clickhouse |
| Aiven docs: materialized views; shards with Distributed | Aiven | https://aiven.io/docs/products/clickhouse/howto/materialized-views.html ; https://aiven.io/docs/products/clickhouse/howto/use-shards-with-distributed-table |
| Exoscale: create materialized views | Exoscale | https://community.exoscale.com/product/dbaas/service-specific/clickhouse/how-to/materialized-views/ |
| Seven common errors in ClickHouse materialized view | Alibaba Cloud | https://www.alibabacloud.com/blog/seven-common-errors-in-clickhouse-materialized-view_601482 |
| Partitioning-key best practices | Alibaba Cloud ApsaraDB | https://www.alibabacloud.com/help/en/clickhouse/use-cases/best-practices-in-selecting-table-partitioning-keys-for-apsaradb-for-clickhouse |
| Data replication and distributed queries | PostHog Handbook | https://posthog.com/handbook/engineering/clickhouse/replication |
| ClickHouse materialized views guide (snippet only) | BigData Boutique | https://bigdataboutique.com/blog/clickhouse-materialized-views-guide |
| Materialized views (2025-03; snippet only) | Yandex Cloud | https://yandex.cloud/en/blog/posts/2025/03/materialized-views |
| ClickHouse source code analysis: insert deduplication (likely pre-26.x) | cheng-w, DEV | https://dev.to/cheng-w/clickhouse-source-code-analysis-insert-deduplication-3jep |
| Idempotent inserts into MVs (restatement) | Pulse Support KB | https://pulse.support/kb/clickhouse-idempotent-inserts-materialized-view |
| ClickHouse Series Part 6: MVs & Operations | ifkarsyah (personal blog) | https://ifkarsyah.github.io/blog/clickhouse-series-6-materialized-views |
| dbt-clickhouse (MV materialization) | socket.dev / PyPI | https://socket.dev/pypi/package/dbt-clickhouse |
| Materialized view has no data | ClickHouse Google Group | https://groups.google.com/g/clickhouse/c/xPKvXGC01go |
| Duplicated data while replication (not opened) | ClickHouse Google Group | https://groups.google.com/g/clickhouse/c/qBAmDpRsyAM |
| MV ON CLUSTER examples, readonly after restore (Russian, 2025) | Habr | https://habr.com/ru/articles/896060 |
| Unused MVs causing performance issues (not reviewed) | SigNoz community | https://community-chat.signoz.io/t/30213150/title-unused-materialized-views-causing-clickhouse-performan |
| Refreshable MVs in release 23.12 (repost) | HackerNoon | https://hackernoon.com/refreshable-materialized-views-and-other-features-in-clickhouse-release-2312 |

### Not relied on (low-quality, mass-generated or aggregator)

| Source | Reason |
|---|---|
| OneUptime blog posts dated 2026-03-31: refreshable MVs, Medallion architecture, chaining MVs, multi-level aggregation chains, POPULATE vs manual backfill, backfilling MVs, common MV mistakes, schema changes in MVs, distributed query execution, lightweight UPDATE, CollapsingMergeTree, projections and mutations (e.g. https://oneuptime.com/blog/post/2026-03-31-clickhouse-populate-vs-manual-backfill-materialized-views/view) | Batch-published, likely mass-generated restatements. One unverified version claim (`lightweight_mutation_projection_mode` in 24.7) |
| plushcap.com chaining-MVs page (https://plushcap.com/analysis/clickhouse/chaining-materialized-views) | Aggregator copy of the ClickHouse blog |
| habr.com/ru/articles/810113 | Appears to be a translation of the ClickHouse chaining blog; not reviewed |
| sqltechhead.github.io; a calpoly.edu-hosted "population guide" page | Low-quality or spam-like content |
