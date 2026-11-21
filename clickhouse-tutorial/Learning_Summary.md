# 

### Explain Query

This section shows what happens when `EXPLAIN` is used to do a query. As an example te following query is run with `EXPLAIN`



```
EXPLAIN indexes = 1
SELECT event_type, count() AS events, uniq(user_id) AS users
FROM shop.events
WHERE tenant_id = 42 AND event_time >= '2026-07-01' AND event_time < '2026-07-08'
GROUP BY event_type
ORDER BY events DESC;

```

ouput the following



```
1	Expression (Project names)
2	  Extremes
3	    Sorting (Sorting for ORDER BY)
4	      Expression ((Before ORDER BY + Projection))
5	        Aggregating
6	          Expression (Before GROUP BY)
7	            Expression ((WHERE + Change column names to column identifiers))
8	              ReadFromMergeTree (shop.events)
9	              Indexes:
10	                MinMax
11	                  Keys:
12	                    event_time
13	                  Condition: and((event_time in (-Inf, '1783468800')), (event_time in ['1782864000', +Inf)))
14	                  Parts: 1/3
15	                  Granules: 841/2443
16	                Partition
17	                  Keys:
18	                    toYYYYMM(event_time)
19	                  Condition: and((toYYYYMM(event_time) in (-Inf, 202607]), (toYYYYMM(event_time) in [202607, +Inf)))
20	                  Parts: 1/1
21	                  Granules: 841/841
22	                PrimaryKey
23	                  Keys:
24	                    tenant_id
25	                    event_time
26	                  Condition: and((event_time in (-Inf, '1783468800')), and((event_time in ['1782864000', +Inf)), (tenant_id in [42, 42])))
27	                  Parts: 1/1
28	                  Granules: 4/841
29	                  Search Algorithm: generic exclusion search
30	                  Ranges: 1
```

What the `EXPLAIN` statement means

The EXPLAIN lists the steps in the order they run.

1. **MinMax:** Every `Part` stores the min and max `event_time` in a tiny file called `minmax_event_time.idx`. In the testing following is what we have in the drive:

   ```
   ./f15f7487-0f33-4d2b-98a8-1b7eeadcaf0a/202607_2_53_4/minmax_event_time.idx
   ./f15f7487-0f33-4d2b-98a8-1b7eeadcaf0a/202608_3_54_4/minmax_event_time.idx
   ./f15f7487-0f33-4d2b-98a8-1b7eeadcaf0a/202606_1_52_4/minmax_event_time.idx
   ```

   

   As can be seen the folders are named based on the partition configuration when schema was created:

   ```
   ...
   PARTITION BY toYYYYMM(event_time)
   ```

   

   ClickHouse compares the week against those ranges and drops the two parts that can’t match. Dropping it from 3 to 1 part, and the 1 part that will be used contains 841 granules.
2. **Partition:** This checks each remaining part’s partition value and since the `MinMax` step already removed the other months, so nothing more goes here, hence the result showing `1/1`.
3. **PrimaryKey:** Inside the part that’s left, ClickHouse uses `primary.cidx` file to pick the granules that can hold tenant 42 in that week (841 → 4).



The folder naming follows the `PARTITION` config eg: `202607_2_53_4`.  Clickhouse always **starts with the partition ID**. The format is `<partition>_<min block>_<max block>_<level>`:

- `202607`: the partition this part belongs to
- `2` and `53`: the range of insert block numbers merged into it
- `4`: how many rounds of merging it has gone through



> ALWAYS REMEMBER: 
>
> `Part` is a physical folder created by Clickhouse. The value of `Part` can be viewed in `system.parts`



In the `EXPLAIN` result there is mention of `PrimaryKey` which in reality it is using `ORDER BY` for our table because we didn't declare specifically `PRIMARY KEY` config as part of the schema declaration. If we want to use `PRIMARY KEY` the field specified must be subset of `ORDER BY` like the example below:

```
...
ORDER BY (tenant_id, event_type, event_time)
PRIMARY KEY (tenant_id, event_type)
```

This can be used with `SummingMergeTree` or `AggregatingMergeTree`

> REMEMBER:
> 
>
> Unlike in Postgres or MySQL, the primary key here is **not unique**.



`system.tables` contains information about primary keys.



> REMEMBER:
>
> As a good practise remember to every time check `read_rows`after running queries, as an example below:
>
> Why this is important it gives an idea whether the query is effective and whether the ORDER BY or INDEX is working in an optimized way.
>
> ```
> SYSTEM FLUSH LOGS;
> SELECT
>     event_time,
>     query_duration_ms,
>     read_rows,
>     formatReadableSize(read_bytes) AS read,
>     substring(query, 1, 80)        AS q
> FROM system.query_log
> WHERE type = 'QueryFinish' AND 
> query ILIKE '%FROM shop.events%' AND 
> query NOT ILIKE '%system.query_log%'
> ORDER BY event_time DESC
> LIMIT 10;
> ```



### Indexing

In Clickhouse there is a config `ignore_data_skipping_indices` this config is another way of saying <u>don't use the index specified</u>. For example:

```
SELECT count() FROM shop.events WHERE session_id = '…'
SETTINGS ignore_data_skipping_indices = 'idx_session';
```

The query will **NOT** use the `idx_session` index when running the query.

The following tables outlined the meaning of each of setting configured 

| Setting                                 | Meaning                                                                                       |
| --------------------------------------- | --------------------------------------------------------------------------------------------- |
| *(none)*                                | use every index that fits (**the default**)                                                   |
| `ignore_data_skipping_indices = 'a, b'` | use all indexes except `a` and `b`                                                            |
| `force_data_skipping_indices = 'a'`     | this forces Clickhouse to use index `a` and if it cannot be used then the **the query fails** |
| `use_skip_indexes = 0`                  | use no skip indexes at all                                                                    |

Clickhouse does not have  **use only this index**  like the ones in MySQL. To replicate this kind of functionality we can switch off the others with `ignore_data_skipping_indices`

The configuration in the table are suitable for **troubleshooting and maintenance** purposes. This configuration are used together by doing comparison with the number of rows returned in query by reading the **read_rows** (using the `SETTINGS log_comment = 'l02-skip-after';` configuration in query)

### Benefits of Partition

As we know `part` is a physical folder that is created based on the `PARTITION` configuration of the schema and indexes resides physically inside the partition folder. There are a number of benefits for partition as Clickhouse allows us to attach/detach partition on the fly:

* **Speed:** they finish instantly regardless of size, because they move or delete files and never rewrite rows.

* **Low cost:** no heavy background work, so queries and inserts aren't slowed down while they run.

* **Precision:** only the chosen partition is affected.

* **Safety:** DETACH is reversible, so you can hide data first and decide later whether to delete it.

* **Immediate disk space:** DROP frees space at once.

* **All-or-nothing:** a whole month appears, disappears or gets replaced in one step.

* **Flexibility:** data can be moved between tables, disks or servers by moving its files.

Following testing validate the impact of attaching or detaching a partition for a query. Following is the query used for the testing:

```
EXPLAIN indexes = 1
SELECT count() FROM shop.events
WHERE tenant_id = 42 AND event_time >= '2026-07-01' AND event_time < '2026-08-01';
```

detaching the partition as follows:

```
ALTER TABLE shop.events DETACH PARTITION 202607;
```

we can see the result it cannot use the index:

```
Expression ((Project names + Projection))                                                                                           |
  Aggregating                                                                                                                       |
    Expression (Before GROUP BY)                                                                                                    |
      Expression ((WHERE + Change column names to column identifiers))                                                              |
        ReadFromMergeTree (shop.events)                                                                                             |
        Indexes:                                                                                                                    |
          MinMax                                                                                                                    |
            Keys:                                                                                                                   |
              event_time                                                                                                            |
            Condition: and((event_time in (-Inf, '1785542400')), (event_time in ['1782864000', +Inf)))                              |
            Parts: 0/8                                                                                                              |
            Granules: 0/804                                                                                                         |
          Partition                                                                                                                 |
            Keys:                                                                                                                   |
              toYYYYMM(event_time)                                                                                                  |
            Condition: and((toYYYYMM(event_time) in (-Inf, 202608]), (toYYYYMM(event_time) in [202607, +Inf)))                      |
            Parts: 0/0                                                                                                              |
            Granules: 0/0                                                                                                           |
          PrimaryKey                                                                                                                |
            Keys:                                                                                                                   |
              tenant_id                                                                                                             |
              event_time                                                                                                            |
            Condition: and((event_time in (-Inf, '1785542400')), and((event_time in ['1782864000', +Inf)), (tenant_id in [42, 42])))|
            Parts: 0/0                                                                                                              |
            Granules: 0/0                                                                                                           |
            Ranges: 0                                                                                                               |
```

When we attach the partition back:

```
ALTER TABLE shop.events ATTACH PARTITION 202607;
```

we get the following result:

```
Expression ((Project names + Projection))                                                                                           |
  Aggregating                                                                                                                       |
    Expression (Before GROUP BY)                                                                                                    |
      Expression ((WHERE + Change column names to column identifiers))                                                              |
        ReadFromMergeTree (shop.events)                                                                                             |
        Indexes:                                                                                                                    |
          MinMax                                                                                                                    |
            Keys:                                                                                                                   |
              event_time                                                                                                            |
            Condition: and((event_time in (-Inf, '1785542400')), (event_time in ['1782864000', +Inf)))                              |
            Parts: 4/12                                                                                                             |
            Granules: 422/1226                                                                                                      |
          Partition                                                                                                                 |
            Keys:                                                                                                                   |
              toYYYYMM(event_time)                                                                                                  |
            Condition: and((toYYYYMM(event_time) in (-Inf, 202608]), (toYYYYMM(event_time) in [202607, +Inf)))                      |
            Parts: 4/4                                                                                                              |
            Granules: 422/422                                                                                                       |
          PrimaryKey                                                                                                                |
            Keys:                                                                                                                   |
              tenant_id                                                                                                             |
              event_time                                                                                                            |
            Condition: and((event_time in (-Inf, '1785542400')), and((event_time in ['1782864000', +Inf)), (tenant_id in [42, 42])))|
            Parts: 4/4                                                                                                              |
            Granules: 5/422                                                                                                         |
            Search Algorithm: generic exclusion search                                                                              |
            Ranges: 4                                                                                                               |
```

As can be seen in each `MinMax`,`Partition` and `PrimaryKey` the result is very contrast between attaching and detaching partition.

#### TTL

**TTL (time to live)** is a rule on a table that says what happens to data once it reaches a certain age. ClickHouse applies it automatically in the background.

Example:

```sql
ALTER TABLE shop.events MODIFY TTL event_time + INTERVAL 60 DAY DELETE;
```

It sets a retention rule on `shop.events`: **delete every row once its `event_time` is more than 60 days in the past.**

##### Benefits

Following are benefits of configuring TTL in a table:

1. **Automatic retention:** The rule lives in the table definition, so nobody can forget it.
2. **Bounded disk usage.** The table can't grow forever, which makes capacity predictable.
3. **Low cost when combined with partitions.** In example of monthly partitions and using  `ttl_only_drop_parts = 1`, ClickHouse drops whole expired parts instantly. Works exactly the same as `DROP PARTITION` but automatic.

   

More than just deleting, TTL can manage data as it ages, following table outlined some of the capabilities it provides:.

| TTL action     | Full SQL example                                                                                                                                                                                                                                                                                                                                                      | Benefit                                        |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------- |
| **DELETE**     | `CREATE TABLE shop.events_ttl_delete (event_time DateTime64(3,'UTC'), tenant_id UInt32, revenue Decimal(12,2)) ENGINE = MergeTree PARTITION BY toYYYYMM(event_time) ORDER BY (tenant_id, event_time) TTL event_time + INTERVAL 1 YEAR DELETE;`                                                                                                                        | remove old rows                                |
| **TO VOLUME**  | `CREATE TABLE shop.events_ttl_tiered (event_time DateTime64(3,'UTC'), tenant_id UInt32, revenue Decimal(12,2)) ENGINE = MergeTree PARTITION BY toYYYYMM(event_time) ORDER BY (tenant_id, event_time) TTL event_time + INTERVAL 30 DAY TO VOLUME 'cold', event_time + INTERVAL 1 YEAR DELETE SETTINGS storage_policy = 'hot_cold';`                                    | move old data to cheaper storage automatically |
| **RECOMPRESS** | `CREATE TABLE shop.events_ttl_recompress (event_time DateTime64(3,'UTC'), tenant_id UInt32, url String) ENGINE = MergeTree PARTITION BY toYYYYMM(event_time) ORDER BY (tenant_id, event_time) TTL event_time + INTERVAL 7 DAY RECOMPRESS CODEC(ZSTD(9));`                                                                                                             | compress old data harder to save space         |
| **GROUP BY**   | `CREATE TABLE shop.events_ttl_rollup (tenant_id UInt32, day Date, event_time DateTime64(3,'UTC'), events UInt64, revenue Decimal(12,2)) ENGINE = MergeTree PARTITION BY toYYYYMM(day) ORDER BY (tenant_id, day, event_time) TTL event_time + INTERVAL 30 DAY GROUP BY tenant_id, day SET event_time = min(event_time), events = sum(events), revenue = sum(revenue);` | collapse old detail into a summary             |
| **Column TTL** | `CREATE TABLE shop.events_ttl_column (event_time DateTime64(3,'UTC'), tenant_id UInt32, url String TTL event_time + INTERVAL 7 DAY, revenue Decimal(12,2)) ENGINE = MergeTree PARTITION BY toYYYYMM(event_time) ORDER BY (tenant_id, event_time);`                                                                                                                    | clear one bulky column early and keep the rest |

| TTL action              | Example                                                                                 | Benefit                                        |
| ----------------------- | --------------------------------------------------------------------------------------- | ---------------------------------------------- |
| `DELETE`                | `event_time + INTERVAL 1 YEAR DELETE`                                                   | remove old rows                                |
| `TO DISK` / `TO VOLUME` | `event_time + INTERVAL 30 DAY TO VOLUME 'cold'`                                         | move old data to cheaper storage automatically |
| `RECOMPRESS`            | `… + INTERVAL 7 DAY RECOMPRESS CODEC(ZSTD(9))`                                          | compress old data harder to save space         |
| `GROUP BY`              | `… + INTERVAL 30 DAY GROUP BY tenant_id, toDate(event_time) SET revenue = sum(revenue)` | collapse old detail into a summary             |
| Column TTL              | `url String TTL event_time + INTERVAL 7 DAY`                                            | clear one bulky column early and keep the rest |

> REMEMBER:
> 
>
> - TTL runs during background merges, so expired rows **can still be visible** for a while. 
> - Without `ttl_only_drop_parts`, it rewrites parts to remove expired rows much like `DELETE`. 



### Projections

When a query filters on columns that aren't a prefix of the table's ORDER BY, we can add a projection with a different **ORDER BY** that matches that filter. 

The optimizer uses it automatically, at the cost of extra storage and slower inserts. As an example we have the following query:

```
SELECT * FROM demo.e WHERE  user_id = 4242
```

We can add projection as follows

```
ALTER TABLE demo.e ADD PROJECTION p_full (SELECT * ORDER BY user_id);
ALTER TABLE demo.e MATERIALIZE PROJECTION p_full;
```

The query optimizer identify that the query is using `user_id` in the WHERE clause and automatically will use the new projection `p_full` 



### Materialized View (MV)

A materialized view in ClickHouse is an **insert trigger**. It only sees **new** inserts, never existing data.

MV data types are using what is called as **Aggregate Functions**. For example:

```
CREATE TABLE shop.events_per_minute
(
    tenant_id  UInt32,
    minute     DateTime('UTC'),
    event_type LowCardinality(String),
    events     AggregateFunction(count),
    users      AggregateFunction(uniq, UInt64),
    revenue    AggregateFunction(sum, Decimal(12, 2)),
    p95_dur    AggregateFunction(quantile(0.95), UInt32)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(minute)
ORDER BY (tenant_id, event_type, minute);
```

The `events`, `users`, `revenue` and `p95_dur` fields are `AggregateFunction`and these are used with *<u>state</u>* when creating MV as following:

```
CREATE MATERIALIZED VIEW shop.mv_events_per_minute TO shop.events_per_minute AS
SELECT
    tenant_id,
    toStartOfMinute(event_time) AS minute,
    event_type,
    countState()                 AS events,
    uniqState(user_id)           AS users,
    sumState(revenue)            AS revenue,
    quantileState(0.95)(duration_ms) AS p95_dur
FROM shop.events
GROUP BY tenant_id, minute, event_type;
```

and when querying MV the query will use *<u>merge</u>* as follows:

```
SELECT
    toStartOfHour(minute) AS hour,
    countMerge(events)    AS events,
    uniqMerge(users)      AS users,
    sumMerge(revenue)     AS revenue,
    quantileMerge(0.95)(p95_dur) AS p95_ms
FROM shop.events_per_minute
WHERE tenant_id = 1 AND minute >= '2026-07-01' AND minute < '2026-07-02'
GROUP BY hour ORDER BY hour;

```

`Function (count, etc) --> functionState (countState) --> functionMerge (countMerge)`

We query with `GROUP BY` + `-Merge` for two reasons:

1. **`GROUP BY`**: rows with the same key may still be in separate, unmerged parts, so we combine them at query time instead of waiting for Clickhouse background process to merge them.
2. **`-Merge`**: the columns hold intermediate aggregation states, not final values. `-Merge` combines the states and turns them into the final number. This is needed even when the table is fully merged.

It is **ALWAYS** recommended to use both `GROUP BY` and `-Merge` for querying 
