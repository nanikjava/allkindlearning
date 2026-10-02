// export streams one ShopStream shop's events to CSV with flat memory use
// (Lesson 03, exercise 2).
//
//   go run ./cmd/export -tenant 1 -out shop1.csv
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/you/clickhouse-tutorial/go-client/internal/chconn"
)

func main() {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	cfg := chconn.RegisterFlags(fs)
	table := fs.String("table", "events", "events or events_all")
	tenant := fs.Uint("tenant", 1, "shop (tenant) id to export")
	out := fs.String("out", "export.csv", "output file")
	_ = fs.Parse(os.Args[1:])

	ctx := context.Background()
	conn, err := chconn.Open(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 1<<20)
	w := csv.NewWriter(bw)
	_ = w.Write([]string{"event_time", "user_id", "session_id", "event_type", "country", "device", "url", "duration_ms", "revenue"})

	// rows.Next() pulls one block at a time from the server: memory stays flat.
	rows, err := conn.Query(ctx, `
		SELECT event_time, user_id, session_id, event_type, country, device, url, duration_ms, revenue
		FROM `+*table+` WHERE tenant_id = ? ORDER BY event_time`, uint32(*tenant))
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var (
		n                                    int
		ts                                   time.Time
		userID                               uint64
		session                              uuid.UUID
		eventType, country, device, url      string
		dur                                  uint32
		revenue                              decimal.Decimal
	)
	for rows.Next() {
		if err := rows.Scan(&ts, &userID, &session, &eventType, &country, &device, &url, &dur, &revenue); err != nil {
			log.Fatal(err)
		}
		_ = w.Write([]string{ts.Format(time.RFC3339Nano), strconv.FormatUint(userID, 10), session.String(),
			eventType, country, device, url, strconv.FormatUint(uint64(dur), 10), revenue.StringFixed(2)})
		n++
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
	w.Flush()
	if err := bw.Flush(); err != nil {
		log.Fatal(err)
	}
	log.Printf("exported %d events of shop %d to %s", n, *tenant, *out)
}
