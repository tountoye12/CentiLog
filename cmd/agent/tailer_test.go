package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTailerReadsCompleteLinesAndPersistsOffsets(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "app.log")
	bufferPath := filepath.Join(directory, "buffer")
	if err := os.WriteFile(logPath, []byte("first\npartial"), 0o600); err != nil {
		t.Fatal("write watched file failed")
	}
	file := FileConfig{Path: logPath, Format: "text", TimeZone: "Local"}
	tailer, err := NewTailer([]FileConfig{file}, bufferPath)
	if err != nil {
		t.Fatalf("NewTailer() error = %v", err)
	}

	lines, _, err := tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(lines) != 1 || lines[0].Text != "first" {
		t.Fatalf("first poll lines = %#v, want only complete first line", lines)
	}
	uncommitted, err := NewTailer([]FileConfig{file}, bufferPath)
	if err != nil {
		t.Fatalf("create tailer before commit: %v", err)
	}
	replayed, _, err := uncommitted.ReadNewLines()
	if err != nil || len(replayed) != 1 || replayed[0].Text != "first" {
		t.Fatalf("uncommitted lines were not replayed: %#v, error=%v", replayed, err)
	}
	if err := tailer.CommitOffsets(map[string]int64{logPath: lines[0].EndOffset}); err != nil {
		t.Fatalf("commit first line offset: %v", err)
	}

	appendFile(t, logPath, "\nsecond\n")
	lines, _, err = tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(lines) != 2 || lines[0].Text != "partial" || lines[1].Text != "second" {
		t.Fatalf("second poll lines = %#v, want completed partial and second line", lines)
	}
	if err := tailer.CommitOffsets(map[string]int64{logPath: lines[1].EndOffset}); err != nil {
		t.Fatalf("commit appended lines: %v", err)
	}

	reloaded, err := NewTailer([]FileConfig{file}, bufferPath)
	if err != nil {
		t.Fatalf("reload tailer: %v", err)
	}
	lines, _, err = reloaded.ReadNewLines()
	if err != nil || len(lines) != 0 {
		t.Fatalf("reloaded tailer repeated lines: %#v, error=%v", lines, err)
	}
}

func TestTailerRestartsWhenFileShrinks(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "app.log")
	if err := os.WriteFile(logPath, []byte("old line\nanother old line\n"), 0o600); err != nil {
		t.Fatal("write watched file failed")
	}
	tailer, err := NewTailer([]FileConfig{{Path: logPath}}, filepath.Join(directory, "buffer"))
	if err != nil {
		t.Fatalf("NewTailer() error = %v", err)
	}
	lines, _, err := tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("initial Poll() error = %v", err)
	}
	if err := tailer.CommitOffsets(map[string]int64{logPath: lines[len(lines)-1].EndOffset}); err != nil {
		t.Fatalf("commit initial file offsets: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("new line\n"), 0o600); err != nil {
		t.Fatal("truncate watched file failed")
	}

	lines, resetPaths, err := tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(lines) != 1 || lines[0].Text != "new line" || len(resetPaths) != 1 || resetPaths[0] != logPath {
		t.Fatalf("poll after shrink = lines %#v, resets %#v", lines, resetPaths)
	}
}

func TestStackAssemblerAppendsContinuationLines(t *testing.T) {
	file := FileConfig{Path: "app.log", Format: "text"}
	assembler := NewStackAssembler()
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	if completed := assembler.Add(FileLine{File: file, Text: "ERROR request failed"}, now); len(completed) != 0 {
		t.Fatalf("first Add() returned %#v, want no completed line", completed)
	}
	for _, continuation := range []string{"    at handler.go:42", "Caused by: disk error", "... 3 more"} {
		if completed := assembler.Add(FileLine{File: file, Text: continuation}, now); len(completed) != 0 {
			t.Fatalf("continuation Add() returned %#v, want no completed line", completed)
		}
	}
	completed := assembler.Add(FileLine{File: file, Text: "INFO next request"}, now)
	if len(completed) != 1 || completed[0].Text != "ERROR request failed\n    at handler.go:42\nCaused by: disk error\n... 3 more" {
		t.Fatalf("assembled lines = %#v", completed)
	}
	if _, ok := assembler.Flush(file.Path); !ok {
		t.Fatal("pending final line was not flushable")
	}
}

func TestStackAssemblerFlushesAfterIdleGrace(t *testing.T) {
	file := FileConfig{Path: "app.log"}
	assembler := NewStackAssembler()
	start := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	assembler.Add(FileLine{File: file, Text: "INFO complete"}, start)
	if _, ok := assembler.FlushIdle(file.Path, start.Add(4*time.Second), 5*time.Second); ok {
		t.Fatal("line flushed before idle grace elapsed")
	}
	line, ok := assembler.FlushIdle(file.Path, start.Add(5*time.Second), 5*time.Second)
	if !ok || line.Text != "INFO complete" {
		t.Fatal("line was not flushed when idle grace elapsed")
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal("open watched file for append failed")
	}
	if _, err := file.WriteString(content); err != nil {
		t.Fatal("append watched file failed")
	}
	if err := file.Close(); err != nil {
		t.Fatal("close watched file failed")
	}
}
