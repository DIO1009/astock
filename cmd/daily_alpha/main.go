// daily_alpha is a standalone command that runs the daily alpha screening
// pipeline once and exits.  The same logic is also embedded inside the
// paper-trading process (auto-scheduled at 09:00 every morning).
//
// # Usage
//
//	go run ./cmd/daily_alpha/
//
// # Environment variables
//
//	PG_DSN         PostgreSQL DSN  (default: postgres://postgres:password@localhost:5432/astock?sslmode=disable)
//	TOP_LAYER1     Layer-1 top-N   (default: 200)
//	TOP_LAYER2     Layer-2 top-N   (default: 50)
//	SCAN_TIMEOUT   HTTP timeout s  (default: 300)
package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"astock_trade/alpha/daily"
	"astock_trade/store"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)
	log.Printf("[daily_alpha] 启动 — %s", time.Now().Format("2006-01-02 15:04:05"))

	dsn := envOrDefault("PG_DSN", "postgres://postgres:password@localhost:5432/astock?sslmode=disable")
	cfg := daily.Config{
		TopLayer1:       envInt("TOP_LAYER1", 200),
		TopLayer2:       envInt("TOP_LAYER2", 50),
		ScanTimeoutSecs: envInt("SCAN_TIMEOUT", 300),
	}

	ctx := context.Background()
	st, err := store.Open(ctx, store.Config{DSN: dsn, MaxConns: 3})
	if err != nil {
		log.Fatalf("[daily_alpha] 无法连接 PostgreSQL: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("[daily_alpha] 建表失败: %v", err)
	}

	res, err := daily.Run(ctx, st, cfg)
	if err != nil {
		log.Fatalf("[daily_alpha] 失败: %v", err)
	}
	log.Printf("[daily_alpha] 完成 — 总共 %d 只，写入 %d 只，耗时 %dms",
		res.Total, res.Layer2, res.ElapsedMs)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
