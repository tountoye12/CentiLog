package main

import (
	"testing"
	"time"
)

func TestAPIKeyRefreshInterval(t *testing.T) {
	if apiKeyRefreshInterval != 30*time.Second {
		t.Fatalf("API key refresh interval = %s, want 30s", apiKeyRefreshInterval)
	}
}
