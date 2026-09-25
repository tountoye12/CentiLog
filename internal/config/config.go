package config

import "os"

const (
	RedisStreamName       = "centilog:logs"
	DeadLetterStreamName  = "centilog:logs:dead-letter"
	DefaultRedisURL       = "redis://centilog:centilog@localhost:6379/0"
	DefaultClickHouseDSN  = "clickhouse://centilog:centilog@localhost:9000/centilog"
	DefaultClickHouseHTTPURL = "http://localhost:8123"
	DefaultPostgresDSN    = "postgres://centilog:centilog@localhost:5432/centilog?sslmode=disable"
)

// GetEnv returns the environment variable value, or fallback when unset or empty.
func GetEnv(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func RedisURL() string {
	return GetEnv("CENTILOG_REDIS_URL", DefaultRedisURL)
}

func ClickHouseDSN() string {
	return GetEnv("CENTILOG_CLICKHOUSE_DSN", DefaultClickHouseDSN)
}

func ClickHouseHTTPURL() string {
	return GetEnv("CENTILOG_CLICKHOUSE_HTTP_URL", DefaultClickHouseHTTPURL)
}

func PostgresDSN() string {
	return GetEnv("CENTILOG_POSTGRES_DSN", DefaultPostgresDSN)
}
