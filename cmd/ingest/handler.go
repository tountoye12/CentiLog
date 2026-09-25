package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"centilog/internal/redact"
	"centilog/internal/schema"
)

const (
	maxRequestBodyBytes = 10 << 20
	maxBatchSize        = 10_000
	maxStreamEntries    = 1_000_000
	retryAfterSeconds   = "5"
)

type serviceIdentity struct {
	service       string
	retentionDays uint16
}

type apiKeyLookup interface {
	Lookup(apiKey string) (serviceIdentity, bool)
}

type logStream interface {
	Length(ctx context.Context) (int64, error)
	AddBatch(ctx context.Context, serializedLogs []string) (int, error)
}

type ingestHandler struct {
	apiKeys apiKeyLookup
	stream  logStream
	now     func() time.Time
}

type ingestResponse struct {
	Accepted int    `json:"accepted"`
	Rejected int    `json:"rejected"`
	Error    string `json:"error,omitempty"`
}

func newHandler(apiKeys apiKeyLookup, stream logStream) http.Handler {
	handler := &ingestHandler{apiKeys: apiKeys, stream: stream, now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handler.health)
	mux.HandleFunc("/v1/logs", handler.logs)
	return mux
}

func (h *ingestHandler) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (h *ingestHandler) logs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, ingestResponse{Error: "method not allowed"})
		return
	}

	identity, ok := h.apiKeys.Lookup(strings.TrimSpace(r.Header.Get("X-Api-Key")))
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ingestResponse{Error: "invalid API key"})
		return
	}

	length, err := h.stream.Length(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ingestResponse{Error: "queue unavailable"})
		return
	}
	if length > maxStreamEntries {
		w.Header().Set("Retry-After", retryAfterSeconds)
		writeJSON(w, http.StatusTooManyRequests, ingestResponse{Error: "ingest queue is full"})
		return
	}

	body, status := readRequestBody(w, r)
	if status != 0 {
		writeJSON(w, status, ingestResponse{Error: http.StatusText(status)})
		return
	}

	var rawLogs []json.RawMessage
	if err := json.Unmarshal(body, &rawLogs); err != nil || rawLogs == nil {
		writeJSON(w, http.StatusBadRequest, ingestResponse{Error: "request body must be a JSON array of logs"})
		return
	}
	if len(rawLogs) > maxBatchSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, ingestResponse{Error: "batch exceeds 10000 logs"})
		return
	}

	serializedLogs := make([]string, 0, len(rawLogs))
	rejected := 0
	for _, rawLog := range rawLogs {
		var log schema.Log
		if err := json.Unmarshal(rawLog, &log); err != nil {
			rejected++
			continue
		}
		log.Service = identity.service
		log.RetentionDays = identity.retentionDays

		normalized, err := schema.NormalizeAt(log, h.now())
		if err != nil {
			rejected++
			continue
		}
		normalized = redact.Redact(normalized)
		normalized.IngestedAt = h.now().UTC().Truncate(time.Millisecond)

		encoded, err := json.Marshal(normalized)
		if err != nil {
			rejected++
			continue
		}
		serializedLogs = append(serializedLogs, string(encoded))
	}

	accepted := 0
	if len(serializedLogs) > 0 {
		accepted, err = h.stream.AddBatch(r.Context(), serializedLogs)
		if err != nil || accepted != len(serializedLogs) {
			writeJSON(w, http.StatusServiceUnavailable, ingestResponse{
				Accepted: accepted,
				Rejected: rejected,
				Error:    "queue write failed",
			})
			return
		}
	}

	writeJSON(w, http.StatusAccepted, ingestResponse{Accepted: accepted, Rejected: rejected})
}

func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, int) {
	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" && encoding != "gzip" {
		return nil, http.StatusUnsupportedMediaType
	}

	compressed := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var reader io.Reader = compressed
	var gzipReader *gzip.Reader
	if encoding == "gzip" {
		var err error
		gzipReader, err = gzip.NewReader(compressed)
		if err != nil {
			return nil, http.StatusBadRequest
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxRequestBodyBytes+1))
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return nil, http.StatusRequestEntityTooLarge
		}
		return nil, http.StatusBadRequest
	}
	if len(body) > maxRequestBodyBytes {
		return nil, http.StatusRequestEntityTooLarge
	}
	return body, 0
}

func writeJSON(w http.ResponseWriter, status int, response ingestResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
