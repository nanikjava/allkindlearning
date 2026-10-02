// Package chconn builds a clickhouse-go connection from flags/env shared by all commands.
package chconn

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type Config struct {
	Addrs    string
	User     string
	Password string
	Database string
	TLS      bool
}

// RegisterFlags binds connection flags. Defaults come from CH_ADDRS, CH_USER, CH_PASSWORD, CH_DB.
func RegisterFlags(fs *flag.FlagSet) *Config {
	c := &Config{}
	fs.StringVar(&c.Addrs, "addrs", env("CH_ADDRS", "localhost:9000"), "comma-separated host:port list (native protocol)")
	fs.StringVar(&c.User, "user", env("CH_USER", "default"), "user")
	fs.StringVar(&c.Password, "password", env("CH_PASSWORD", "learn"), "password")
	fs.StringVar(&c.Database, "db", env("CH_DB", "shop"), "database")
	fs.BoolVar(&c.TLS, "tls", false, "use TLS (port 9440 in production)")
	return c
}

// Open returns a pooled connection. With several addrs, clickhouse-go round-robins new
// connections and fails over to the next address when one is down.
func Open(ctx context.Context, c *Config) (driver.Conn, error) {
	opts := &clickhouse.Options{
		Addr: strings.Split(c.Addrs, ","),
		Auth: clickhouse.Auth{Database: c.Database, Username: c.User, Password: c.Password},
		ConnOpenStrategy: clickhouse.ConnOpenRoundRobin,
		DialTimeout:      5 * time.Second,
		ReadTimeout:      60 * time.Second,
		MaxOpenConns:     10,
		MaxIdleConns:     5,
		ConnMaxLifetime:  10 * time.Minute,
		Compression:      &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
		ClientInfo: clickhouse.ClientInfo{
			Products: []struct{ Name, Version string }{{Name: "ch-tutorial", Version: "0.1"}},
		},
	}
	if c.TLS {
		opts.TLS = &tls.Config{}
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		if ex, ok := err.(*clickhouse.Exception); ok {
			return nil, fmt.Errorf("ping: [%d] %s", ex.Code, ex.Message)
		}
		return nil, fmt.Errorf("ping: %w", err)
	}
	return conn, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
