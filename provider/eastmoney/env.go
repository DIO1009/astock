package eastmoney

import (
	"os"
	"strconv"
	"time"
)

var (
	minInterval            = durationFromEnvMs("ASTOCK_EM_MIN_INTERVAL_MS", 1200)
	maxRealtimeConcurrency = intFromEnv("ASTOCK_EM_MAX_CONCURRENCY", 1)
)

func durationFromEnvMs(key string, defaultMs int) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return time.Duration(defaultMs) * time.Millisecond
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms < 0 {
		return time.Duration(defaultMs) * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}

func intFromEnv(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultVal
	}
	return n
}
