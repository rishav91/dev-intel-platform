// Package config centralizes environment-variable parsing for the services.
// Phase 0 had each service carry its own getenv/splitEnv helpers; this collapses
// them into one place so defaults and parsing rules don't drift between services.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// String returns the env var named key, or def if unset/empty.
func String(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// List returns a comma-separated env var as a trimmed slice (e.g. KAFKA_BROKERS).
func List(key, def string) []string {
	parts := strings.Split(String(key, def), ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// Int returns key parsed as an int, or def if unset or unparseable.
func Int(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Duration returns key parsed as a Go duration (e.g. "500ms", "5s"), or def.
func Duration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
