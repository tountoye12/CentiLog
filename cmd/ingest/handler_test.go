package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"centilog/internal/schema"
)

type testAPIKeys map[string]serviceIdentity

func (keys testAPIKeys) Lookup(apiKey string) (serviceIdentity, bool) {
	identity, ok := keys[apiKey]
	return identity, ok
}

type testStream struct {
	length       int64
	entries      []string
	lengthErr    error
	addErr       error
	lengthCalls  int
	addBatchCalls int
}

func (stream *testStream) Length(context.Context) (int64, error) {
	stream.lengthCalls++
	return stream.length, stream.lengthErr
}

func (stream *testStream) AddBatch(_ context.Context, logs []string) (int, error) {
	stream.addBatchCalls++
	if stream.addErr != nil {
		return 0, stream.addErr
	}
	stream.entries = append(stream.entries, logs...)
	return len(logs), nil
}

func TestLogsAcceptsNormalizesRedactsAndUsesAuthorizedService(t *testing.T) {
	stream := &testStream{}
	keys := testAPIKeys{"valid-key": {service: "demo-service", retentionDays: 30}}
	handler := newHandler(keys, stream)
	body := `[{"service":"forged-service","host":"host-1","env":"dev","level":"WARNING","message":"password=hunter2","timestamp":"2020-01-02T03:04:05.123456+05:30","trace_id":"trace-1","retention_days":1}]`
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(body))
	request.Header.Set("X-Api-Key", "valid-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
	var counts ingestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &counts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if counts.Accepted != 1 || counts.Rejected != 0 {
		t.Fatalf("counts = %+v, want accepted 1 and rejected 0", counts)
	}
	if len(stream.entries) != 1 {
		t.Fatalf("queued entries = %d, want 1", len(stream.entries))
	}
	var got schema.Log
	if err := json.Unmarshal([]byte(stream.entries[0]), &got); err != nil {
		t.Fatalf("decode queued log: %v", err)
	}
	if got.Service != "demo-service" || got.RetentionDays != 30 {
		t.Errorf("service identity was not taken from the API key: service=%q retention=%d", got.Service, got.RetentionDays)
	}
	if got.Level != "warn" || got.Message != "password=[REDACTED]" || !got.Redacted {
		t.Errorf("log was not normalized and redacted")
	}
	if got.Timestamp.Location() != time.UTC || got.Timestamp.Nanosecond() != 123_000_000 {
		t.Errorf("timestamp = %s, want UTC millisecond precision", got.Timestamp)
	}
	if got.IngestedAt.IsZero() || got.IngestedAt.Location() != time.UTC || got.IngestedAt.Nanosecond()%1_000_000 != 0 {
		t.Errorf("ingested_at = %s, want non-zero UTC millisecond precision", got.IngestedAt)
	}
}

func TestLogsAcceptsGzipBody(t *testing.T) {
	body := []byte(`[{"host":"host-1","env":"dev","level":"info","message":"hello"}]`)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(body); err != nil {
		t.Fatal("gzip write failed")
	}
	if err := writer.Close(); err != nil {
		t.Fatal("gzip close failed")
	}
	stream := &testStream{}
	handler := newHandler(testAPIKeys{"valid-key": {service: "demo-service", retentionDays: 30}}, stream)
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", &compressed)
	request.Header.Set("X-Api-Key", "valid-key")
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || len(stream.entries) != 1 {
		t.Fatalf("status=%d queued=%d, want accepted gzip log", response.Code, len(stream.entries))
	}
}

func TestLogsCountsInvalidRecordsWithoutRejectingBatch(t *testing.T) {
	stream := &testStream{}
	handler := newHandler(testAPIKeys{"valid-key": {service: "demo-service", retentionDays: 30}}, stream)
	body := `[{"host":"host-1","env":"dev","message":"valid"},{"host":7,"env":"dev","message":"invalid"}]`
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(body))
	request.Header.Set("X-Api-Key", "valid-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var counts ingestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &counts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != http.StatusAccepted || counts.Accepted != 1 || counts.Rejected != 1 {
		t.Fatalf("status=%d counts=%+v, want 202 accepted=1 rejected=1", response.Code, counts)
	}
}

func TestLogsRejectsMissingAndWrongAPIKeys(t *testing.T) {
	for _, test := range []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "wrong", key: "wrong-key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := &testStream{}
			handler := newHandler(testAPIKeys{"valid-key": {service: "demo-service", retentionDays: 30}}, stream)
			request := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader("[]"))
			if test.key != "" {
				request.Header.Set("X-Api-Key", test.key)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
			if stream.lengthCalls != 0 || stream.addBatchCalls != 0 {
				t.Fatal("unauthorized request reached the queue")
			}
		})
	}
}

func TestLogsAppliesBackpressureAboveLimit(t *testing.T) {
	stream := &testStream{length: maxStreamEntries + 1}
	handler := newHandler(testAPIKeys{"valid-key": {service: "demo-service", retentionDays: 30}}, stream)
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader("[]"))
	request.Header.Set("X-Api-Key", "valid-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != retryAfterSeconds {
		t.Fatalf("status=%d Retry-After=%q, want 429 and %s", response.Code, response.Header().Get("Retry-After"), retryAfterSeconds)
	}
	if stream.addBatchCalls != 0 {
		t.Fatal("backpressured request was added to the queue")
	}
}

func TestLogsEnforcesBodyAndBatchLimits(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "body limit", body: strings.Repeat(" ", maxRequestBodyBytes+1), want: http.StatusRequestEntityTooLarge},
		{name: "batch limit", body: "[" + strings.Repeat("null,", maxBatchSize) + "null]", want: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := &testStream{}
			handler := newHandler(testAPIKeys{"valid-key": {service: "demo-service", retentionDays: 30}}, stream)
			request := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(test.body))
			request.Header.Set("X-Api-Key", "valid-key")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	handler := newHandler(testAPIKeys{}, &testStream{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q, want 200 ok", response.Code, response.Body.String())
	}
}

func TestLogsReturnsServiceUnavailableWhenQueueIsUnavailable(t *testing.T) {
	stream := &testStream{lengthErr: fmt.Errorf("unavailable")}
	handler := newHandler(testAPIKeys{"valid-key": {service: "demo-service", retentionDays: 30}}, stream)
	request := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader("[]"))
	request.Header.Set("X-Api-Key", "valid-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
