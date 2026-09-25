package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSampleAgentConfig(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "agent.yaml")
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.ServerURL != "http://localhost:8080" || config.APIKey != "demo-key-123" {
		t.Fatalf("unexpected server configuration")
	}
	if config.Host == "" {
		t.Fatal("default host was empty")
	}
	if config.FlushInterval.Value() != 5*time.Second || config.SamplingPercentage("debug") != 20 {
		t.Fatalf("unexpected flush interval or debug sampling")
	}
	if len(config.Files) != 2 || config.Files[0].Format != "auto" || config.Files[0].TimeZone != "Local" || config.Files[1].TimeZone != "Asia/Kolkata" {
		t.Fatalf("unexpected watched file configuration")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	contents := "server_url: http://localhost:8080\napi_key: test-key\nfiles:\n  - path: app.log\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal("write config failed")
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.Env != "development" || config.BufferDir != filepath.Join("data", "agent-buffer") || config.MaxBufferSizeMB != 512 || config.BatchSize != 500 || config.MinLevel != "trace" {
		t.Fatalf("unexpected defaults")
	}
	if config.SamplingPercentage("info") != 100 || config.Files[0].Format != "auto" {
		t.Fatalf("unexpected sampling or format defaults")
	}
}

func TestLoadConfigRejectsUnknownTimeZone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	contents := "server_url: http://localhost:8080\napi_key: test-key\nfiles:\n  - path: app.log\n    time_zone: Mars/Olympus\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal("write config failed")
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig() error = nil, want invalid time zone")
	}
}
