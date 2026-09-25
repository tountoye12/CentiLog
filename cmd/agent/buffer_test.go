package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"centilog/internal/schema"
)

func TestWriteBatchCreatesReadableGzipFile(t *testing.T) {
	store, err := NewBatchStore(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("NewBatchStore() error = %v", err)
	}
	want := []schema.Log{{Service: "demo-service", Message: "token=[REDACTED]", Redacted: true}}
	path, err := store.WriteBatch(want)
	if err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if !strings.HasSuffix(path, ".json.gz") {
		t.Fatalf("batch path = %q, want .json.gz suffix", path)
	}
	got, err := decodeBatch(path)
	if err != nil {
		t.Fatalf("decodeBatch() error = %v", err)
	}
	if len(got) != 1 || got[0].Message != want[0].Message || !got[0].Redacted {
		t.Fatalf("decoded batch = %#v, want redacted test record", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read batch directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary batch file was left behind: %q", entry.Name())
		}
	}
}

func TestRecoverClaimedBatchAfterRestart(t *testing.T) {
	directory := t.TempDir()
	store, err := NewBatchStore(directory, 10)
	if err != nil {
		t.Fatalf("NewBatchStore() error = %v", err)
	}
	path, err := store.WriteBatch([]schema.Log{{Message: "recover me"}})
	if err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	claimed, err := store.ClaimOldest()
	if err != nil || claimed != path+".sending" {
		t.Fatalf("ClaimOldest() = %q, error=%v", claimed, err)
	}

	restarted, err := NewBatchStore(directory, 10)
	if err != nil {
		t.Fatalf("restart NewBatchStore() error = %v", err)
	}
	count, err := restarted.PendingCount()
	if err != nil || count != 1 {
		t.Fatalf("PendingCount() = %d, error=%v, want 1", count, err)
	}
	recovered, err := restarted.ClaimOldest()
	if err != nil || filepath.Ext(recovered) != ".sending" {
		t.Fatalf("recovered claim = %q, error=%v", recovered, err)
	}
	if err := restarted.Unclaim(recovered); err != nil {
		t.Fatalf("Unclaim() error = %v", err)
	}
}

func TestBatchStoreEvictsOldestBatchWhenOverLimit(t *testing.T) {
	store, err := NewBatchStore(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("NewBatchStore() error = %v", err)
	}
	logRecord := []schema.Log{{Message: strings.Repeat("payload", 100)}}
	oldest, err := store.WriteBatch(logRecord)
	if err != nil {
		t.Fatalf("write oldest batch: %v", err)
	}
	newer, err := store.WriteBatch(logRecord)
	if err != nil {
		t.Fatalf("write newer batch: %v", err)
	}
	oldestInfo, err := os.Stat(oldest)
	if err != nil {
		t.Fatalf("stat oldest batch: %v", err)
	}
	store.maxBytes = oldestInfo.Size() + 1
	store.enforceLimitLocked()

	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatalf("oldest batch still exists after eviction: %v", err)
	}
	if _, err := os.Stat(newer); err != nil {
		t.Fatalf("newer batch was evicted: %v", err)
	}
}

func TestReadCompressedBatchAsJSONArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manual.json.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	writer := gzip.NewWriter(file)
	if _, err := io.WriteString(writer, `[{"message":"ok"}]`); err != nil {
		t.Fatalf("write gzip batch: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close batch: %v", err)
	}
	batch, err := decodeBatch(path)
	if err != nil {
		t.Fatalf("decodeBatch() error = %v", err)
	}
	if len(batch) != 1 || batch[0].Message != "ok" {
		t.Fatalf("decoded batch = %#v, want one record with message ok", batch)
	}
}
