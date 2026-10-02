// backup runs an ASYNC ClickHouse backup of the ShopStream database, polls
// system.backups until it finishes, and exits non-zero on failure
// (Lesson 07, exercise 2). Run it from cron / a Kubernetes CronJob.
//
//   go run ./cmd/backup -addrs localhost:9001 -dest "Disk('backups', 'shop-{ts}')"
//   go run ./cmd/backup -addrs localhost:9001 -dest "Disk('backups', 'shop-{ts}')" -base "Disk('backups', 'shop-full-1')"
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"github.com/you/clickhouse-tutorial/go-client/internal/chconn"
)

func main() {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	cfg := chconn.RegisterFlags(fs)
	dest := fs.String("dest", "Disk('backups', 'shop-{ts}')", "backup destination; {ts} is replaced by a timestamp")
	base := fs.String("base", "", "base backup for an incremental backup (empty = full)")
	cluster := fs.String("cluster", "prod", "cluster name for ON CLUSTER (empty = this node only)")
	timeout := fs.Duration("timeout", 2*time.Hour, "give up after this long")
	_ = fs.Parse(os.Args[1:])

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	conn, err := chconn.Open(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	target := strings.ReplaceAll(*dest, "{ts}", time.Now().UTC().Format("20060102-150405"))
	q := "BACKUP DATABASE " + cfg.Database
	if *cluster != "" {
		q += " ON CLUSTER " + *cluster
	}
	q += " TO " + target
	if *base != "" {
		q += " SETTINGS base_backup = " + *base
	}
	q += " ASYNC"

	var id, status string
	if err := conn.QueryRow(ctx, q).Scan(&id, &status); err != nil {
		log.Fatalf("start backup: %v", err)
	}
	log.Printf("started %s -> %s (id %s)", q, status, id)

	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Fatalf("backup %s did not finish in %s", id, *timeout)
		case <-t.C:
		}
		var st, errMsg string
		var files uint64
		err := conn.QueryRow(ctx, "SELECT toString(status), error, num_files FROM system.backups WHERE id = ?", id).
			Scan(&st, &errMsg, &files)
		if err != nil {
			log.Printf("poll: %v", err)
			continue
		}
		switch st {
		case "BACKUP_CREATED":
			log.Printf("done: %s (%d files)", target, files)
			return
		case "BACKUP_FAILED":
			log.Fatalf("backup failed: %s", errMsg)
		default:
			log.Printf("status: %s", st)
		}
	}
}
