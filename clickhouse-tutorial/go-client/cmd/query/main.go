// query shows idiomatic read patterns: struct scanning, server-side parameters,
// per-query settings, query IDs, progress callbacks, and streaming large results.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/you/clickhouse-tutorial/go-client/internal/chconn"
)

type TypeStat struct {
	EventType string  `ch:"event_type"`
	Events    uint64  `ch:"events"`
	Users     uint64  `ch:"users"`
	Revenue   float64 `ch:"revenue"`
}

func main() {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	cfg := chconn.RegisterFlags(fs)
	table := fs.String("table", "events", "table to query (events_all on the cluster)")
	tenant := fs.Uint("tenant", 1, "tenant id")
	_ = fs.Parse(os.Args[1:])

	ctx := context.Background()
	conn, err := chconn.Open(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// 1. Server-side parameters ({name:Type}) — safe against injection, typed by the server.
	//    Settings, a query ID (find it later in system.query_log) and a progress callback
	//    all ride on the context.
	qctx := clickhouse.Context(ctx,
		clickhouse.WithParameters(clickhouse.Parameters{
			"tenant": fmt.Sprint(*tenant),
			"since":  time.Now().Add(-365 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05"),
		}),
		clickhouse.WithSettings(clickhouse.Settings{
			"max_execution_time": 10, // seconds; protect the cluster from runaway queries
			"max_memory_usage":   2_000_000_000,
		}),
		clickhouse.WithQueryID(fmt.Sprintf("tutorial-%d", time.Now().UnixNano())),
		clickhouse.WithProgress(func(p *clickhouse.Progress) {
			log.Printf("progress: rows=%d bytes=%d", p.Rows, p.Bytes)
		}),
	)
	var stats []TypeStat
	err = conn.Select(qctx, &stats, `
		SELECT event_type,
		       count()                       AS events,
		       uniq(user_id)                 AS users,
		       toFloat64(sum(revenue))       AS revenue
		FROM `+*table+`
		WHERE tenant_id = {tenant:UInt32} AND event_time >= {since:DateTime}
		GROUP BY event_type
		ORDER BY events DESC`)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range stats {
		fmt.Printf("%-12s events=%-8d users=%-8d revenue=%.2f\n", s.EventType, s.Events, s.Users, s.Revenue)
	}

	// 2. Streaming: rows.Next() pulls blocks as they arrive, so memory stays flat
	//    no matter how big the result.
	rows, err := conn.Query(ctx, "SELECT user_id, count() AS c FROM "+*table+
		" WHERE tenant_id = ? GROUP BY user_id ORDER BY c DESC LIMIT 5", uint32(*tenant))
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	fmt.Println("top users:")
	for rows.Next() {
		var uid, c uint64
		if err := rows.Scan(&uid, &c); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  user=%d events=%d\n", uid, c)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}
