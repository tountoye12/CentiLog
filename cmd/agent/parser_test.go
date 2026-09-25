package main

import (
	"testing"
	"time"

	"centilog/internal/schema"
)

func TestParseJSONLines(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name      string
		line      string
		wantLevel string
		wantTime  time.Time
		wantTrace string
		wantAttr  string
	}{
		{
			name:      "common names and attributes",
			line:      `{"@timestamp":"2024-02-03T04:05:06.789+05:30","severity":"WARNING","msg":"slow request","traceId":"trace-1","request_id":"req-1"}`,
			wantLevel: "warn",
			wantTime:  time.Date(2024, time.February, 2, 22, 35, 6, 789_000_000, time.UTC),
			wantTrace: "trace-1",
			wantAttr:  "req-1",
		},
		{
			name:      "epoch seconds",
			line:      `{"time":1700000000,"level":"ERR","message":"failed"}`,
			wantLevel: "error",
			wantTime:  time.Unix(1_700_000_000, 0).UTC(),
		},
		{
			name:      "epoch milliseconds",
			line:      `{"timestamp":1700000000123,"level":"critical","message":"stopped"}`,
			wantLevel: "fatal",
			wantTime:  time.Unix(1_700_000_000, 123_000_000).UTC(),
		},
		{
			name:      "local timestamp uses file time zone",
			line:      `{"timestamp":"2024-01-02T12:00:00","level":"info","message":"local"}`,
			wantLevel: "info",
			wantTime:  time.Date(2024, time.January, 2, 6, 30, 0, 0, time.UTC),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := FileConfig{Format: "json", TimeZone: "Asia/Kolkata"}
			got := ParseLine(test.line, file, now)
			if got.Level != test.wantLevel {
				t.Errorf("level = %q, want %q", got.Level, test.wantLevel)
			}
			if !got.Timestamp.Equal(test.wantTime) || got.Timestamp.Location() != time.UTC {
				t.Errorf("timestamp = %s, want %s UTC", got.Timestamp, test.wantTime)
			}
			if test.wantTrace != "" && got.TraceID != test.wantTrace {
				t.Errorf("trace_id = %q, want %q", got.TraceID, test.wantTrace)
			}
			if test.wantAttr != "" && got.Attributes["request_id"] != test.wantAttr {
				t.Errorf("request_id attribute = %q, want %q", got.Attributes["request_id"], test.wantAttr)
			}
		})
	}
}

func TestParseSyslogLines(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name      string
		line      string
		wantLevel string
		wantTime  time.Time
		wantMsg   string
		wantHost  string
	}{
		{
			name:      "priority maps severity",
			line:      `<34>Jan  2 08:00:00 app-host sshd[123]: connection denied`,
			wantLevel: "fatal",
			wantTime:  time.Date(2026, time.January, 2, 2, 30, 0, 0, time.UTC),
			wantMsg:   "connection denied",
			wantHost:  "app-host",
		},
		{
			name:      "without priority guesses level",
			line:      `Jan  2 08:00:00 app-host app[9]: WARNING disk almost full`,
			wantLevel: "warn",
			wantTime:  time.Date(2026, time.January, 2, 2, 30, 0, 0, time.UTC),
			wantMsg:   "WARNING disk almost full",
			wantHost:  "app-host",
		},
		{
			name:      "RFC5424 priority and timestamp",
			line:      `<34>1 2024-02-03T04:05:06.789+05:30 server-a app 123 ID47 - connection denied`,
			wantLevel: "fatal",
			wantTime:  time.Date(2024, time.February, 2, 22, 35, 6, 789_000_000, time.UTC),
			wantMsg:   "connection denied",
			wantHost:  "server-a",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ParseLine(test.line, FileConfig{Format: "syslog", TimeZone: "Asia/Kolkata"}, now)
			if got.Level != test.wantLevel || got.Message != test.wantMsg {
				t.Errorf("parsed level/message = %q/%q", got.Level, got.Message)
			}
			if !got.Timestamp.Equal(test.wantTime) {
				t.Errorf("timestamp = %s, want %s", got.Timestamp, test.wantTime)
			}
			if got.Host != test.wantHost || got.Attributes["syslog_program"] == "" {
				t.Errorf("syslog host/program were not parsed")
			}
		})
	}
}

func TestParseJSONPriority(t *testing.T) {
	got := ParseLine(`{"priority":13,"message":"disk warning"}`, FileConfig{Format: "json"}, time.Now())
	if got.Level != "warn" {
		t.Fatalf("level = %q, want warn", got.Level)
	}
}

func TestParseTextLines(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		zone      string
		wantLevel string
		wantTime  time.Time
		wantMsg   string
	}{
		{
			name:      "timestamp with offset",
			line:      "2024-02-03T04:05:06.123+05:30 ERROR request failed",
			zone:      "Local",
			wantLevel: "error",
			wantTime:  time.Date(2024, time.February, 2, 22, 35, 6, 123_000_000, time.UTC),
			wantMsg:   "request failed",
		},
		{
			name:      "timestamp without offset uses file zone",
			line:      "2024-02-03 04:05:06 [INFO] started",
			zone:      "Asia/Kolkata",
			wantLevel: "info",
			wantTime:  time.Date(2024, time.February, 2, 22, 35, 6, 0, time.UTC),
			wantMsg:   "started",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ParseLine(test.line, FileConfig{Format: "text", TimeZone: test.zone}, time.Now())
			if got.Level != test.wantLevel || got.Message != test.wantMsg {
				t.Errorf("parsed level/message = %q/%q", got.Level, got.Message)
			}
			if !got.Timestamp.Equal(test.wantTime) || got.Timestamp.Location() != time.UTC {
				t.Errorf("timestamp = %s, want %s UTC", got.Timestamp, test.wantTime)
			}
		})
	}
}

func TestAutoDetectsEachLineAndFallsBackToGuessedLevel(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name      string
		line      string
		wantLevel string
		wantMsg   string
	}{
		{name: "JSON", line: `{"level":"DEBUG","message":"json event"}`, wantLevel: "debug", wantMsg: "json event"},
		{name: "text", line: "2024-02-03T04:05:06Z ERROR text event", wantLevel: "error", wantMsg: "text event"},
		{name: "unrecognized guessed", line: "worker ERROR while saving", wantLevel: "error", wantMsg: "worker ERROR while saving"},
		{name: "unrecognized default", line: "ordinary text", wantLevel: "info", wantMsg: "ordinary text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ParseLine(test.line, FileConfig{Format: "auto", TimeZone: "Local"}, now)
			if got.Level != test.wantLevel || got.Message != test.wantMsg {
				t.Errorf("parsed level/message = %q/%q", got.Level, got.Message)
			}
		})
	}
}

func TestNormalizeParsedLevel(t *testing.T) {
	if got := schema.NormalizeLevel(ParseLine("x CRITICAL", FileConfig{Format: "auto"}, time.Now()).Level); got != "fatal" {
		t.Fatalf("level = %q, want fatal", got)
	}
}
