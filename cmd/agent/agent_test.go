package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentPollAssemblesFiltersAndRedacts(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "app.log")
	contents := "{\"level\":\"INFO\",\"message\":\"below threshold\"}\n" +
		"{\"level\":\"ERROR\",\"message\":\"password=hunter2\"}\n"
	if err := os.WriteFile(logPath, []byte(contents), 0o600); err != nil {
		t.Fatal("write log file failed")
	}
	config := Config{
		Host:            "agent-host",
		Env:             "test",
		BufferDir:       filepath.Join(directory, "buffer"),
		MaxBufferSizeMB: 512,
		BatchSize:       500,
		FlushInterval:   Duration(time.Minute),
		MinLevel:        "warn",
		Sampling:        map[string]float64{"error": 100},
		Files:           []FileConfig{{Path: logPath, Format: "json", TimeZone: "Local"}},
	}
	agent, err := NewAgent(config)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	logs, err := agent.Poll(now)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("Poll() returned %d logs, want final line held for possible stack trace", len(logs))
	}
	logs, err = agent.Flush(now)
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Flush() returned %d logs, want one", len(logs))
	}
	if logs[0].Message != "password=[REDACTED]" || !logs[0].Redacted || logs[0].Level != "error" {
		t.Error("agent did not redact and normalize the retained log")
	}
	if logs[0].Host != "agent-host" || logs[0].Env != "test" || logs[0].Attributes["timestamp_missing"] != "true" {
		t.Error("agent defaults or missing timestamp flag were not applied")
	}
	if count, err := agent.PendingBatchCount(); err != nil || count != 1 {
		t.Fatalf("pending batch count = %d, error=%v, want 1", count, err)
	}
	restartedTailer, err := NewTailer(config.Files, config.BufferDir)
	if err != nil {
		t.Fatalf("reload committed offsets: %v", err)
	}
	remaining, _, err := restartedTailer.ReadNewLines()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("committed lines were reread: %#v, error=%v", remaining, err)
	}
}
