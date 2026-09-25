package config

import "testing"

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		set       bool
		fallback  string
		want      string
	}{
		{name: "configured value", value: "custom", set: true, fallback: "default", want: "custom"},
		{name: "empty value uses default", value: "", set: true, fallback: "default", want: "default"},
		{name: "unset value uses default", fallback: "default", want: "default"},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := "CENTILOG_CONFIG_TEST_" + string(rune('A'+i))
			if test.set {
				t.Setenv(key, test.value)
			}
			if got := GetEnv(key, test.fallback); got != test.want {
				t.Fatalf("GetEnv() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConnectionStringDefaults(t *testing.T) {
	tests := []struct {
		name string
		key  string
		get  func() string
		want string
	}{
		{name: "redis", key: "CENTILOG_REDIS_URL", get: RedisURL, want: DefaultRedisURL},
		{name: "clickhouse", key: "CENTILOG_CLICKHOUSE_DSN", get: ClickHouseDSN, want: DefaultClickHouseDSN},
		{name: "postgres", key: "CENTILOG_POSTGRES_DSN", get: PostgresDSN, want: DefaultPostgresDSN},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.key, "")
			if got := test.get(); got != test.want {
				t.Fatalf("connection string = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStreamNames(t *testing.T) {
	if RedisStreamName == "" || DeadLetterStreamName == "" || RedisStreamName == DeadLetterStreamName {
		t.Fatalf("stream names must be non-empty and distinct: %q, %q", RedisStreamName, DeadLetterStreamName)
	}
}
