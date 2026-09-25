package main

import (
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"centilog/internal/schema"
)

const bytesPerMegabyte int64 = 1024 * 1024

type BatchStore struct {
	directory string
	failedDir string
	maxBytes  int64
	mu        sync.Mutex
}

func NewBatchStore(directory string, maxBufferSizeMB int) (*BatchStore, error) {
	if maxBufferSizeMB < 1 || int64(maxBufferSizeMB) > int64(^uint64(0)>>1)/bytesPerMegabyte {
		return nil, errors.New("invalid maximum buffer size")
	}
	failedDir := filepath.Join(directory, "failed")
	if err := os.MkdirAll(failedDir, 0o700); err != nil {
		return nil, errors.New("create failed batch directory")
	}
	store := &BatchStore{
		directory: directory,
		failedDir: failedDir,
		maxBytes:  int64(maxBufferSizeMB) * bytesPerMegabyte,
	}
	if err := store.recoverClaims(); err != nil {
		return nil, err
	}
	return store, nil
}

// WriteBatch durably creates a gzip JSON array before returning its final filename.
func (store *BatchStore) WriteBatch(logs []schema.Log) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	if logs == nil {
		logs = []schema.Log{}
	}
	temporary, err := os.CreateTemp(store.directory, ".batch-*.tmp")
	if err != nil {
		return "", errors.New("create temporary batch")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", errors.New("secure temporary batch")
	}
	compressed := gzip.NewWriter(temporary)
	if err := json.NewEncoder(compressed).Encode(logs); err != nil {
		_ = compressed.Close()
		_ = temporary.Close()
		return "", errors.New("encode buffered logs")
	}
	if err := compressed.Close(); err != nil {
		_ = temporary.Close()
		return "", errors.New("compress buffered logs")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", errors.New("sync buffered logs")
	}
	if err := temporary.Close(); err != nil {
		return "", errors.New("close temporary batch")
	}

	name, err := newBatchName(time.Now().UTC())
	if err != nil {
		return "", err
	}
	batchPath := filepath.Join(store.directory, name)
	if err := os.Rename(temporaryPath, batchPath); err != nil {
		return "", errors.New("publish buffered batch")
	}
	store.syncDirectory()
	store.enforceLimitLocked()
	return batchPath, nil
}

func (store *BatchStore) ClaimOldest() (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	batches, err := store.pendingBatchesLocked()
	if err != nil {
		return "", err
	}
	for _, batch := range batches {
		claimedPath := batch.path + ".sending"
		if err := os.Rename(batch.path, claimedPath); err != nil {
			continue
		}
		return claimedPath, nil
	}
	return "", nil
}

func (store *BatchStore) Unclaim(claimedPath string) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	if !strings.HasSuffix(claimedPath, ".json.gz.sending") {
		return errors.New("invalid claimed batch path")
	}
	pendingPath := strings.TrimSuffix(claimedPath, ".sending")
	if err := os.Rename(claimedPath, pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("return batch to queue")
	}
	store.enforceLimitLocked()
	return nil
}

func (store *BatchStore) DeleteClaim(claimedPath string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := os.Remove(claimedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("delete delivered batch")
	}
	return nil
}

func (store *BatchStore) MoveToFailed(claimedPath string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	name := filepath.Base(strings.TrimSuffix(claimedPath, ".sending"))
	if !strings.HasSuffix(name, ".json.gz") {
		return errors.New("invalid claimed batch path")
	}
	if err := os.Rename(claimedPath, filepath.Join(store.failedDir, name)); err != nil {
		return errors.New("move rejected batch to failed directory")
	}
	return nil
}

func (store *BatchStore) PendingCount() (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	batches, err := store.pendingBatchesLocked()
	return len(batches), err
}

func (store *BatchStore) recoverClaims() error {
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return errors.New("read agent buffer directory")
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json.gz.sending") {
			continue
		}
		claimedPath := filepath.Join(store.directory, name)
		pendingName := strings.TrimSuffix(name, ".sending")
		pendingPath := filepath.Join(store.directory, pendingName)
		if _, err := os.Stat(pendingPath); err == nil {
			pendingName = strings.TrimSuffix(pendingName, ".json.gz") + "-recovered-" + fmt.Sprint(time.Now().UnixNano()) + ".json.gz"
			pendingPath = filepath.Join(store.directory, pendingName)
		}
		if err := os.Rename(claimedPath, pendingPath); err != nil {
			return errors.New("recover in-flight batch")
		}
	}
	return nil
}

type storedBatch struct {
	path string
	size int64
}

func (store *BatchStore) pendingBatchesLocked() ([]storedBatch, error) {
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return nil, errors.New("read agent buffer directory")
	}
	batches := make([]storedBatch, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, errors.New("stat buffered batch")
		}
		batches = append(batches, storedBatch{
			path: filepath.Join(store.directory, entry.Name()),
			size: info.Size(),
		})
	}
	sort.Slice(batches, func(i, j int) bool {
		return filepath.Base(batches[i].path) < filepath.Base(batches[j].path)
	})
	return batches, nil
}

func (store *BatchStore) enforceLimitLocked() {
	batches, err := store.pendingBatchesLocked()
	if err != nil {
		log.Print("could not inspect agent buffer size")
		return
	}
	var total int64
	for _, batch := range batches {
		total += batch.size
	}
	deleted := 0
	for _, batch := range batches {
		if total <= store.maxBytes {
			break
		}
		if err := os.Remove(batch.path); err != nil {
			continue
		}
		total -= batch.size
		deleted++
	}
	if deleted > 0 {
		log.Printf("agent buffer limit exceeded; deleted %d oldest batches", deleted)
	}
}

func (store *BatchStore) syncDirectory() {
	directory, err := os.Open(store.directory)
	if err != nil {
		return
	}
	_ = directory.Sync()
	_ = directory.Close()
}

func newBatchName(now time.Time) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("create batch identifier")
	}
	return now.UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random[:]) + ".json.gz", nil
}

func decodeBatch(path string) ([]schema.Log, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var logs []schema.Log
	if err := json.NewDecoder(io.LimitReader(reader, 64<<20)).Decode(&logs); err != nil {
		return nil, err
	}
	return logs, nil
}
