package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type FileLine struct {
	File      FileConfig
	Text      string
	EndOffset int64
}

type Tailer struct {
	files       []FileConfig
	offsetsPath string
	offsets     map[string]int64
	readOffsets map[string]int64
}

func NewTailer(files []FileConfig, bufferDir string) (*Tailer, error) {
	if err := os.MkdirAll(bufferDir, 0o700); err != nil {
		return nil, errors.New("create agent buffer directory")
	}
	tailer := &Tailer{
		files:       append([]FileConfig(nil), files...),
		offsetsPath: filepath.Join(bufferDir, "offsets.json"),
		offsets:     make(map[string]int64),
		readOffsets: make(map[string]int64),
	}
	data, err := os.ReadFile(tailer.offsetsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("read agent offsets")
	}
	if err == nil {
		if err := json.Unmarshal(data, &tailer.offsets); err != nil || tailer.offsets == nil {
			return nil, errors.New("decode agent offsets")
		}
		for _, offset := range tailer.offsets {
			if offset < 0 {
				return nil, errors.New("agent offsets cannot be negative")
			}
		}
	}
	tailer.readOffsets = cloneOffsets(tailer.offsets)
	return tailer, nil
}

// ReadNewLines stages complete lines without persisting their offsets.
func (tailer *Tailer) ReadNewLines() ([]FileLine, []string, error) {
	lines := make([]FileLine, 0)
	resetPaths := make([]string, 0)
	var firstErr error

	for _, fileConfig := range tailer.files {
		file, err := os.Open(fileConfig.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if firstErr == nil {
				firstErr = errors.New("open watched file")
			}
			continue
		}

		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			if firstErr == nil {
				firstErr = errors.New("stat watched file")
			}
			continue
		}
		offset := tailer.readOffsets[fileConfig.Path]
		if info.Size() < offset {
			offset = 0
			tailer.readOffsets[fileConfig.Path] = 0
			resetPaths = append(resetPaths, fileConfig.Path)
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			_ = file.Close()
			if firstErr == nil {
				firstErr = errors.New("seek watched file")
			}
			continue
		}

		reader := bufio.NewReader(file)
		position := offset
		for {
			line, readErr := reader.ReadString('\n')
			if readErr == nil {
				position += int64(len(line))
				line = strings.TrimSuffix(line, "\n")
				line = strings.TrimSuffix(line, "\r")
				lines = append(lines, FileLine{File: fileConfig, Text: line, EndOffset: position})
				continue
			}
			if !errors.Is(readErr, io.EOF) && firstErr == nil {
				firstErr = errors.New("read watched file")
			}
			break
		}
		_ = file.Close()
		tailer.readOffsets[fileConfig.Path] = position
	}
	return lines, resetPaths, firstErr
}

func (tailer *Tailer) CommitOffsets(safeOffsets map[string]int64) error {
	candidate := cloneOffsets(tailer.offsets)
	for path, safeOffset := range safeOffsets {
		readOffset, read := tailer.readOffsets[path]
		if !read || safeOffset < 0 || safeOffset > readOffset {
			return errors.New("invalid safe file offset")
		}
		if readOffset < tailer.offsets[path] {
			candidate[path] = safeOffset
		} else if safeOffset > candidate[path] {
			candidate[path] = safeOffset
		}
	}
	if err := tailer.saveOffsets(candidate); err != nil {
		return err
	}
	tailer.offsets = candidate
	return nil
}

func (tailer *Tailer) saveOffsets(offsets map[string]int64) error {
	data, err := json.Marshal(offsets)
	if err != nil {
		return errors.New("encode agent offsets")
	}
	temporary, err := os.CreateTemp(filepath.Dir(tailer.offsetsPath), ".offsets-*")
	if err != nil {
		return errors.New("create temporary offsets file")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return errors.New("secure temporary offsets file")
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return errors.New("write agent offsets")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errors.New("sync agent offsets")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close agent offsets")
	}
	if runtime.GOOS == "windows" {
		backupPath := tailer.offsetsPath + ".bak"
		_ = os.Remove(backupPath)
		if err := os.Rename(tailer.offsetsPath, backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("backup agent offsets")
		}
		if err := os.Rename(temporaryPath, tailer.offsetsPath); err != nil {
			_ = os.Rename(backupPath, tailer.offsetsPath)
			return errors.New("replace agent offsets")
		}
		_ = os.Remove(backupPath)
		return nil
	}
	if err := os.Rename(temporaryPath, tailer.offsetsPath); err != nil {
		return errors.New("replace agent offsets")
	}
	return nil
}

func cloneOffsets(offsets map[string]int64) map[string]int64 {
	clone := make(map[string]int64, len(offsets))
	for path, offset := range offsets {
		clone[path] = offset
	}
	return clone
}

type StackAssembler struct {
	pending   map[string]FileLine
	updatedAt map[string]time.Time
}

func NewStackAssembler() *StackAssembler {
	return &StackAssembler{
		pending:   make(map[string]FileLine),
		updatedAt: make(map[string]time.Time),
	}
}

func (assembler *StackAssembler) Add(line FileLine, now time.Time) []FileLine {
	if isStackContinuation(line.Text) {
		if previous, ok := assembler.pending[line.File.Path]; ok {
			previous.Text += "\n" + line.Text
			previous.EndOffset = line.EndOffset
			assembler.pending[line.File.Path] = previous
			assembler.updatedAt[line.File.Path] = now
			return nil
		}
		return []FileLine{line}
	}

	completed := make([]FileLine, 0, 1)
	if previous, ok := assembler.pending[line.File.Path]; ok {
		completed = append(completed, previous)
	}
	assembler.pending[line.File.Path] = line
	assembler.updatedAt[line.File.Path] = now
	return completed
}

func (assembler *StackAssembler) Flush(path string) (FileLine, bool) {
	line, ok := assembler.pending[path]
	delete(assembler.pending, path)
	delete(assembler.updatedAt, path)
	return line, ok
}

func (assembler *StackAssembler) FlushIdle(path string, now time.Time, idleFor time.Duration) (FileLine, bool) {
	if _, ok := assembler.pending[path]; !ok {
		return FileLine{}, false
	}
	if now.Sub(assembler.updatedAt[path]) < idleFor {
		return FileLine{}, false
	}
	return assembler.Flush(path)
}

func isStackContinuation(line string) bool {
	if line == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(line)
	if unicode.IsSpace(first) {
		return true
	}
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "Caused by:") || strings.HasPrefix(trimmed, "...")
}
