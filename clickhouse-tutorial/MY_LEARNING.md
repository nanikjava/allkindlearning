## Parts cleanup

Why running `OPTIMIZE TABLE shop.events FINAL;` the on_disk size increases ? because the old parts aren't deleted right away.

Check whether you're counting inactive parts, if your size query reads `system.parts` without filtering on active, it includes the old parts:

```
SELECT active, count() AS parts, formatReadableSize(sum(bytes_on_disk)) AS size
FROM system.parts
WHERE database = 'shop' AND table = 'events'
GROUP BY active;
```

will see something like this

```
   ┌─active─┬─parts─┬─size───────┐
1. │      0 │    15 │ 1.90 GiB   │
2. │      1 │     3 │ 619.78 MiB │
   └────────┴───────┴────────────┘
```

Each table has its own cleanup task that deletes inactive (old) parts. These MergeTree settings control how often it runs:

| Setting                           | Default | Meaning                                                                                     |
| --------------------------------- | ------- | ------------------------------------------------------------------------------------------- |
| `cleanup_delay_period`            | 30 s    | base interval between runs                                                                  |
| `cleanup_delay_period_random_add` | 10 s    | random 0–10 s added so tables don't all clean up at the same moment                         |
| `max_cleanup_delay_period`        | 300 s   | upper limit on the interval (newer versions lengthen the gap when there's nothing to clean) |
| `old_parts_lifetime`              | 480 s   | how long a part must have been inactive before it can be deleted                            |

Check the current values:

```sql
SELECT name, value FROM system.merge_tree_settings
WHERE name IN ('cleanup_delay_period','cleanup_delay_period_random_add',
               'max_cleanup_delay_period','old_parts_lifetime');
```

To check remove item the part became inactive, can use the following:

```
SELECT name, remove_time,
       remove_time + INTERVAL 480 SECOND AS eligible_at,
       dateDiff('second', now(), eligible_at) AS seconds_left
FROM system.parts
WHERE database='shop' AND table='events' AND NOT active;
```

`480` is the default old_parts_lifetime, if you changed it, use your value.

### When parts were actually removed ?

This uses part_log. It's enabled in the default server config. If the table doesn't exist on your server, enable part_log in the config first.

```
SELECT event_time, part_name
FROM system.part_log
WHERE database='shop' AND table='events' AND event_type='RemovePart'
ORDER BY event_time DESC LIMIT 20;
```
