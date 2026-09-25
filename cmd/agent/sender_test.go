package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"centilog/internal/schema"
)

func TestSenderSuccessDeletesDeliveredBatch(t *testing.T) {
	requestReceived := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Api-Key") != "test-key" || r.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("unexpected ingest request method or headers")
		}
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("open gzip request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var logs []schema.Log
		if err := json.NewDecoder(reader).Decode(&logs); err != nil || len(logs) != 1 {
			t.Errorf("decode ingest body: logs=%d error=%v", len(logs), err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requestReceived <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	store, err := NewBatchStore(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("NewBatchStore() error = %v", err)
	}
	if _, err := store.WriteBatch([]schema.Log{{Message: "token=[REDACTED]", Redacted: true}}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	claimed, err := store.ClaimOldest()
	if err != nil {
		t.Fatalf("ClaimOldest() error = %v", err)
	}
	sender := NewSender(Config{ServerURL: server.URL, APIKey: "test-key"}, store)
	sender.client = server.Client()
	sender.warn = func(string, ...any) {}
	if result := sender.sendBatch(context.Background(), claimed); result.disposition != sendSucceeded {
		t.Fatalf("send disposition = %v, want success", result.disposition)
	}
	<-requestReceived
	if err := store.DeleteClaim(claimed); err != nil {
		t.Fatalf("DeleteClaim() error = %v", err)
	}
	count, err := store.PendingCount()
	if err != nil || count != 0 {
		t.Fatalf("PendingCount() = %d, error=%v, want 0", count, err)
	}
}

func TestSenderClassifiesResponses(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		retryAfter    string
		want          sendDisposition
		wantDelay     time.Duration
		wantHasRetry  bool
	}{
		{name: "unauthorized is retried", status: http.StatusUnauthorized, want: sendRetry},
		{name: "server error is retried", status: http.StatusInternalServerError, want: sendRetry},
		{name: "bad request is quarantined", status: http.StatusBadRequest, want: sendFailed},
		{name: "too large is quarantined", status: http.StatusRequestEntityTooLarge, want: sendFailed},
		{name: "rate limit honors seconds", status: http.StatusTooManyRequests, retryAfter: "7", want: sendRateLimited, wantDelay: 7 * time.Second, wantHasRetry: true},
		{name: "rate limit without header backs off", status: http.StatusTooManyRequests, want: sendRateLimited},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			store, err := NewBatchStore(t.TempDir(), 10)
			if err != nil {
				t.Fatalf("NewBatchStore() error = %v", err)
			}
			path, err := store.WriteBatch([]schema.Log{{Message: "test"}})
			if err != nil {
				t.Fatalf("WriteBatch() error = %v", err)
			}
			claimed, err := store.ClaimOldest()
			if err != nil {
				t.Fatalf("ClaimOldest() error = %v", err)
			}
			sender := NewSender(Config{ServerURL: server.URL}, store)
			sender.client = server.Client()
			sender.now = func() time.Time { return time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC) }
			sender.warn = func(string, ...any) {}
			got := sender.sendBatch(context.Background(), claimed)
			if got.disposition != test.want {
				t.Fatalf("disposition = %v, want %v", got.disposition, test.want)
			}
			if got.hasRetry != test.wantHasRetry || got.retryAfter != test.wantDelay {
				t.Errorf("retry result = (%s, %t), want (%s, %t)", got.retryAfter, got.hasRetry, test.wantDelay, test.wantHasRetry)
			}
			if test.want == sendFailed {
				if err := store.MoveToFailed(claimed); err != nil {
					t.Fatalf("MoveToFailed() error = %v", err)
				}
				if _, err := os.Stat(filepath.Join(store.failedDir, filepath.Base(path))); err != nil {
					t.Fatalf("failed batch not moved: %v", err)
				}
			} else {
				if err := store.Unclaim(claimed); err != nil {
					t.Fatalf("Unclaim() error = %v", err)
				}
			}
		})
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	value := now.Add(12 * time.Second).Format(http.TimeFormat)
	got, ok := parseRetryAfter(value, now)
	if !ok || got != 12*time.Second {
		t.Fatalf("parseRetryAfter() = %s, %t, want 12s, true", got, ok)
	}
}

func TestRetryBackoffCapsAtOneMinute(t *testing.T) {
	delay := initialRetryDelay
	for range 8 {
		delay = nextRetryDelay(delay)
	}
	if delay != maximumRetryDelay {
		t.Fatalf("retry delay = %s, want %s", delay, maximumRetryDelay)
	}
}

func TestClockWarningIsRateLimited(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	warnings := 0
	sender := &Sender{
		now:  func() time.Time { return now },
		warn: func(string, ...any) { warnings++ },
	}
	serverDate := now.Add(-10 * time.Second).Format(http.TimeFormat)
	sender.checkServerClock(serverDate)
	now = now.Add(30 * time.Second)
	sender.checkServerClock(serverDate)
	if warnings != 1 {
		t.Fatalf("clock warnings = %d, want 1 within a minute", warnings)
	}
	now = now.Add(31 * time.Second)
	sender.checkServerClock(serverDate)
	if warnings != 2 {
		t.Fatalf("clock warnings = %d, want 2 after more than a minute", warnings)
	}
}

func TestBufferBatchJSONIsNotLogged(t *testing.T) {
	message := "token=" + strings.Repeat("x", 12)
	store, err := NewBatchStore(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("NewBatchStore() error = %v", err)
	}
	path, err := store.WriteBatch([]schema.Log{{Message: message, Redacted: true}})
	if err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open batch: %v", err)
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("open gzip batch: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || strings.Contains(string(data), "token=xxxxxxxx") {
		t.Fatalf("batch contains unredacted content or could not be read: error=%v", err)
	}
	_ = file.Close()
}
