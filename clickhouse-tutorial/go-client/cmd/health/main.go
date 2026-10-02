// health connects to each node individually and reports replication health.
// Use it as a readiness probe or a cron check; exits 1 when something is unhealthy.
//
//   go run ./cmd/health -addrs localhost:9001,localhost:9002,localhost:9003,localhost:9004
//
// With -listen :8080 it serves GET /ready instead (200 healthy, 503 not): a readiness
// probe for ShopStream's ingest service (Lesson 08, exercise 3).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/you/clickhouse-tutorial/go-client/internal/chconn"
)

type replica struct {
	Database     string `ch:"database"`
	Table        string `ch:"table"`
	IsReadonly   uint8  `ch:"is_readonly"`
	IsSession    uint8  `ch:"is_session_expired"`
	AbsoluteDly  uint64 `ch:"absolute_delay"`
	QueueSize    uint32 `ch:"queue_size"`
	ActiveRepls  uint8  `ch:"active_replicas"`
	TotalRepls   uint8  `ch:"total_replicas"`
}

func main() {
	fs := flag.NewFlagSet("health", flag.ExitOnError)
	cfg := chconn.RegisterFlags(fs)
	maxDelay := fs.Uint64("max-delay", 60, "max acceptable replication delay in seconds")
	listen := fs.String("listen", "", "serve /ready on this address instead of checking once")
	_ = fs.Parse(os.Args[1:])

	if *listen != "" {
		http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
			if check(cfg, *maxDelay) {
				fmt.Fprintln(w, "ok")
				return
			}
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		})
		log.Printf("serving readiness on %s/ready", *listen)
		log.Fatal(http.ListenAndServe(*listen, nil))
	}
	if !check(cfg, *maxDelay) {
		os.Exit(1)
	}
}

// check prints one line per node and replicated table and reports overall health.
func check(cfg *chconn.Config, maxDelay uint64) bool {
	unhealthy := false
	for _, addr := range strings.Split(cfg.Addrs, ",") {
		c := *cfg
		c.Addrs = addr
		c.Database = "default"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, err := chconn.Open(ctx, &c)
		if err != nil {
			fmt.Printf("%-16s DOWN  %v\n", addr, err)
			unhealthy = true
			cancel()
			continue
		}
		var host string
		_ = conn.QueryRow(ctx, "SELECT hostName()").Scan(&host)
		var reps []replica
		err = conn.Select(ctx, &reps, `
			SELECT database, table, is_readonly, is_session_expired, absolute_delay,
			       queue_size, active_replicas, total_replicas
			FROM system.replicas`)
		if err != nil {
			fmt.Printf("%-16s ERROR %v\n", addr, err)
			unhealthy = true
		}
		for _, r := range reps {
			status := "OK"
			switch {
			case r.IsReadonly == 1 || r.IsSession == 1:
				status = "READONLY"
			case r.AbsoluteDly > maxDelay:
				status = "LAGGING"
			case r.ActiveRepls < r.TotalRepls:
				status = "DEGRADED"
			}
			if status != "OK" {
				unhealthy = true
			}
			fmt.Printf("%-16s %-8s %-9s %s.%s delay=%ds queue=%d replicas=%d/%d\n",
				addr, host, status, r.Database, r.Table, r.AbsoluteDly, r.QueueSize, r.ActiveRepls, r.TotalRepls)
		}
		conn.Close()
		cancel()
	}
	return !unhealthy
}
