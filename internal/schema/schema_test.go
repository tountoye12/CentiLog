package schema

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNormalizeLevel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "TRACE", want: "trace"},
		{input: "DEBUG", want: "debug"},
		{input: "INFO", want: "info"},
		{input: "WARNING", want: "warn"},
		{input: "WARN", want: "warn"},
		{input: "ERR", want: "error"},
		{input: "ERROR", want: "error"},
		{input: "CRITICAL", want: "fatal"},
		{input: "panic", want: "fatal"},
		{input: "unrecognized", want: "info"},
		{input: "", want: "info"},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			if got := NormalizeLevel(test.input); got != test.want {
				t.Fatalf("NormalizeLevel(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestLevelRank(t *testing.T) {
	tests := []struct {
		level string
		want  int
	}{
		{level: "trace", want: 0},
		{level: "debug", want: 1},
		{level: "info", want: 2},
		{level: "warning", want: 3},
		{level: "error", want: 4},
		{level: "critical", want: 5},
		{level: "unknown", want: 2},
	}

	for _, test := range tests {
		t.Run(test.level, func(t *testing.T) {
			if got := LevelRank(test.level); got != test.want {
				t.Fatalf("LevelRank(%q) = %d, want %d", test.level, got, test.want)
			}
		})
	}
}

func TestNormalizeRequiresFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Log)
	}{
		{name: "service", mutate: func(log *Log) { log.Service = " " }},
		{name: "host", mutate: func(log *Log) { log.Host = "" }},
		{name: "env", mutate: func(log *Log) { log.Env = "" }},
		{name: "message", mutate: func(log *Log) { log.Message = "  " }},
		{name: "retention", mutate: func(log *Log) { log.RetentionDays = 0 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			log := validLog()
			test.mutate(&log)
			if _, err := NormalizeAt(log, time.Now()); err == nil {
				t.Fatal("NormalizeAt() error = nil, want validation error")
			}
		})
	}
}

func TestNormalizeTimestamps(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 678_000_000, time.UTC)
	zone := time.FixedZone("UTC+2", 2*60*60)
	tests := []struct {
		name           string
		timestamp      time.Time
		ingestedAt     time.Time
		wantTime       time.Time
		wantIngestedAt time.Time
		wantAttrs      map[string]string
	}{
		{
			name:           "converts to UTC and millisecond precision",
			timestamp:      time.Date(2026, time.January, 2, 5, 4, 5, 123_456_789, zone),
			ingestedAt:     time.Date(2026, time.January, 2, 5, 4, 5, 987_654_321, zone),
			wantTime:       time.Date(2026, time.January, 2, 3, 4, 5, 123_000_000, time.UTC),
			wantIngestedAt: time.Date(2026, time.January, 2, 3, 4, 5, 987_000_000, time.UTC),
			wantAttrs:      map[string]string{},
		},
		{
			name:     "fills missing timestamp and flags it",
			wantTime: now,
			wantAttrs: map[string]string{
				TimestampMissingAttribute: "true",
			},
		},
		{
			name:      "corrects future timestamp and records skew",
			timestamp: now.Add(6*time.Minute + 125*time.Millisecond),
			wantTime:  now,
			wantAttrs: map[string]string{
				TimestampCorrectedAttribute:  "true",
				TimestampSkewMillisAttribute: "360125",
			},
		},
		{
			name:      "does not correct timestamp at five minute boundary",
			timestamp: now.Add(MaxFutureTimestampSkew),
			wantTime:  now.Add(MaxFutureTimestampSkew),
			wantAttrs: map[string]string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			log := validLog()
			log.Timestamp = test.timestamp
			log.IngestedAt = test.ingestedAt
			got, err := NormalizeAt(log, now)
			if err != nil {
				t.Fatalf("NormalizeAt() error = %v", err)
			}
			if !got.Timestamp.Equal(test.wantTime) || got.Timestamp.Location() != time.UTC {
				t.Errorf("Timestamp = %s (%s), want %s (UTC)", got.Timestamp, got.Timestamp.Location(), test.wantTime)
			}
			wantIngestedAt := test.wantIngestedAt
			if wantIngestedAt.IsZero() {
				wantIngestedAt = now
			}
			if !got.IngestedAt.Equal(wantIngestedAt) || got.IngestedAt.Location() != time.UTC {
				t.Errorf("IngestedAt = %s, want %s (UTC)", got.IngestedAt, wantIngestedAt)
			}
			if len(got.Attributes) != len(test.wantAttrs) {
				t.Fatalf("Attributes = %#v, want %#v", got.Attributes, test.wantAttrs)
			}
			for key, want := range test.wantAttrs {
				if got.Attributes[key] != want {
					t.Errorf("Attributes[%q] = %q, want %q", key, got.Attributes[key], want)
				}
			}
		})
	}
}

func TestNormalizeTruncatesLongMessagesWithoutBreakingUTF8(t *testing.T) {
	log := validLog()
	log.Message = strings.Repeat("a", MaxMessageLength-1) + "é" + "tail"

	got, err := NormalizeAt(log, time.Now())
	if err != nil {
		t.Fatalf("NormalizeAt() error = %v", err)
	}
	if len(got.Message) > MaxMessageLength {
		t.Fatalf("message has %d bytes, maximum is %d", len(got.Message), MaxMessageLength)
	}
	if !utf8.ValidString(got.Message) {
		t.Fatal("truncated message is not valid UTF-8")
	}
}

func validLog() Log {
	return Log{
		Service:       "api",
		Host:          "host-1",
		Env:           "test",
		Level:         "warning",
		Message:       "request complete",
		RetentionDays: 30,
	}
}
