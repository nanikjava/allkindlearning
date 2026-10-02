// ingest simulates a production event producer.
//
// Two modes:
//   -mode=batch  : client-side batching. Rows are buffered in a channel and flushed every
//                  -batch rows or -flush interval, whichever first. Best throughput; the
//                  pattern you want for services that own a stream (Kafka consumer, etc.).
//   -mode=async  : many tiny inserts with async_insert=1. The server buffers and flushes.
//                  Use when you can't batch (many independent writers, serverless, etc.).
//
// Failed batches are retried with exponential backoff. Retries can produce duplicates unless
// the table deduplicates identical blocks (Replicated*MergeTree does this by default for
// inserts with the same block contents), which is why we retry the *same* batch.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/you/clickhouse-tutorial/go-client/internal/chconn"
)

type Event struct {
	EventTime  time.Time
	TenantID   uint32
	UserID     uint64
	SessionID  uuid.UUID
	EventType  string
	Country    string
	Device     string
	URL        string
	DurationMs uint32
	Revenue    float64 // Decimal(12,2) accepts float64 or decimal.Decimal
}

var (
	eventTypes = []string{"page_view", "page_view", "page_view", "click", "add_to_cart", "purchase", "search"}
	countries  = []string{"US", "DE", "IN", "BR", "GB", "FR", "JP"}
	devices    = []string{"desktop", "mobile", "tablet"}
)

func randomEvent() Event {
	et := eventTypes[rand.IntN(len(eventTypes))]
	e := Event{
		EventTime:  time.Now().UTC(),
		TenantID:   uint32(1 + rand.IntN(1000)),
		UserID:     rand.Uint64N(5_000_000),
		SessionID:  uuid.New(),
		EventType:  et,
		Country:    countries[rand.IntN(len(countries))],
		Device:     devices[rand.IntN(len(devices))],
		URL:        fmt.Sprintf("/product/%d", rand.IntN(20000)),
		DurationMs: uint32(rand.IntN(30000)),
	}
	if et == "purchase" {
		e.Revenue = float64(rand.IntN(50000)) / 100
	}
	return e
}

func main() {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	cfg := chconn.RegisterFlags(fs)
	table := fs.String("table", "events", "target table (use events_all on the cluster)")
	mode := fs.String("mode", "batch", "batch | async")
	rate := fs.Int("rate", 20000, "events per second to generate")
	batchSize := fs.Int("batch", 50000, "max rows per batch (batch mode)")
	flush := fs.Duration("flush", time.Second, "max time between flushes (batch mode)")
	duration := fs.Duration("duration", 0, "stop after this long (0 = until Ctrl-C)")
	_ = fs.Parse(os.Args[1:])

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	conn, err := chconn.Open(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	events := make(chan Event, *batchSize*2)
	go produce(ctx, events, *rate)

	var sent, failed atomic.Int64
	go report(ctx, &sent, &failed)

	switch *mode {
	case "batch":
		runBatch(ctx, conn, *table, events, *batchSize, *flush, &sent, &failed)
	case "async":
		runAsync(ctx, conn, *table, events, &sent, &failed)
	default:
		log.Fatalf("unknown mode %q", *mode)
	}
	log.Printf("done: sent=%d failed=%d", sent.Load(), failed.Load())
}

func produce(ctx context.Context, out chan<- Event, rate int) {
	defer close(out)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	perTick := max(1, rate/100)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for range perTick {
				select {
				case out <- randomEvent():
				default: // back-pressure: drop rather than block forever (count it in real life)
				}
			}
		}
	}
}

func runBatch(ctx context.Context, conn driver.Conn, table string, in <-chan Event, size int, every time.Duration, sent, failed *atomic.Int64) {
	buf := make([]Event, 0, size)
	t := time.NewTicker(every)
	defer t.Stop()
	flushBuf := func() {
		if len(buf) == 0 {
			return
		}
		// Use a fresh context for the final flush so Ctrl-C doesn't lose the buffer.
		fctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := withRetry(fctx, 5, func() error { return insertBatch(fctx, conn, table, buf) }); err != nil {
			log.Printf("batch of %d dropped: %v", len(buf), err)
			failed.Add(int64(len(buf)))
		} else {
			sent.Add(int64(len(buf)))
		}
		buf = buf[:0]
	}
	for {
		select {
		case e, ok := <-in:
			if !ok {
				flushBuf()
				return
			}
			buf = append(buf, e)
			if len(buf) >= size {
				flushBuf()
			}
		case <-t.C:
			flushBuf()
		}
	}
}

func insertBatch(ctx context.Context, conn driver.Conn, table string, rows []Event) error {
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO "+table+
		" (event_time, tenant_id, user_id, session_id, event_type, country, device, url, duration_ms, revenue)")
	if err != nil {
		return err
	}
	defer batch.Abort() // no-op after Send
	for _, e := range rows {
		if err := batch.Append(e.EventTime, e.TenantID, e.UserID, e.SessionID, e.EventType,
			e.Country, e.Device, e.URL, e.DurationMs, e.Revenue); err != nil {
			return err
		}
	}
	return batch.Send()
}

func runAsync(ctx context.Context, conn driver.Conn, table string, in <-chan Event, sent, failed *atomic.Int64) {
	// async_insert=1: server buffers small inserts and flushes as one part.
	// wait_for_async_insert=1: the call returns only after the data is durably written
	// (safer; set 0 for fire-and-forget at the risk of silent loss).
	actx := clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"async_insert":                 1,
		"wait_for_async_insert":        1,
		"async_insert_busy_timeout_ms": 1000,
	}))
	q := "INSERT INTO " + table +
		" (event_time, tenant_id, user_id, session_id, event_type, country, device, url, duration_ms, revenue)" +
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	sem := make(chan struct{}, 50) // 50 concurrent "clients"
	for e := range in {
		sem <- struct{}{}
		go func(e Event) {
			defer func() { <-sem }()
			err := conn.AsyncInsert(actx, q, true, e.EventTime, e.TenantID, e.UserID, e.SessionID,
				e.EventType, e.Country, e.Device, e.URL, e.DurationMs, e.Revenue)
			if err != nil {
				failed.Add(1)
				return
			}
			sent.Add(1)
		}(e)
	}
}

// withRetry retries transient failures with exponential backoff + jitter.
func withRetry(ctx context.Context, attempts int, fn func() error) error {
	var err error
	backoff := 200 * time.Millisecond
	for i := range attempts {
		if err = fn(); err == nil {
			return nil
		}
		if !retryable(err) {
			return err
		}
		log.Printf("attempt %d failed: %v (retrying in %s)", i+1, err, backoff)
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(backoff + time.Duration(rand.Int64N(int64(backoff/2)))):
		}
		backoff = min(backoff*2, 10*time.Second)
	}
	return err
}

func retryable(err error) bool {
	var ex *clickhouse.Exception
	if errors.As(err, &ex) {
		switch ex.Code {
		case 60, // UNKNOWN_TABLE
			62, // SYNTAX_ERROR
			53, // TYPE_MISMATCH
			516: // AUTHENTICATION_FAILED
			return false
		}
		// 242 TABLE_IS_READ_ONLY (Keeper down), 252 TOO_MANY_PARTS, 202 TOO_MANY_SIMULTANEOUS_QUERIES,
		// 319 UNKNOWN_STATUS_OF_INSERT, 999 KEEPER_EXCEPTION ... are worth retrying.
		return true
	}
	return true // network errors, EOF, timeouts
}

func report(ctx context.Context, sent, failed *atomic.Int64) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var last int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s := sent.Load()
			log.Printf("sent=%d (%.0f/s) failed=%d", s, float64(s-last)/5, failed.Load())
			last = s
		}
	}
}
