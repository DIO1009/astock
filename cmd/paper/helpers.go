package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"astock_trade/rotation"
)
func checkMark(ok bool) string {
	if ok {
		return "✅"
	}
	return "❌"
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return def
	}
}

func rotationEnabledFromEnv() bool {
	return envBool("ASTOCK_ROTATION_ENABLED", false)
}

func envString(key string, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func rotationPolicyForStartup(enabled bool, cfg rotation.Config) *rotation.Policy {
	if !enabled {
		return nil
	}
	return rotation.New(cfg)
}