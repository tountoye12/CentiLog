package main

import (
	"time"

	"centilog/internal/schema"
)

type Agent struct {
	config          Config
	tailer          *Tailer
	stacks          *StackAssembler
	processor       *LogProcessor
	store           *BatchStore
	batch           []schema.Log
	batchOffsets    map[string]int64
	pendingBatch    string
	filteredByLevel int
	filteredBySample int
}

type FilterCounts struct {
	Level    int
	Sampling int
}

func NewAgent(config Config) (*Agent, error) {
	tailer, err := NewTailer(config.Files, config.BufferDir)
	if err != nil {
		return nil, err
	}
	store, err := NewBatchStore(config.BufferDir, config.MaxBufferSizeMB)
	if err != nil {
		return nil, err
	}
	return &Agent{
		config:       config,
		tailer:       tailer,
		stacks:       NewStackAssembler(),
		processor:    NewLogProcessor(config),
		store:        store,
		batchOffsets: make(map[string]int64),
	}, nil
}

func (agent *Agent) Poll(now time.Time) ([]schema.Log, error) {
	lines, resetPaths, pollErr := agent.tailer.ReadNewLines()
	logs := make([]schema.Log, 0, len(lines))
	for _, path := range resetPaths {
		agent.batchOffsets[path] = 0
		if pending, ok := agent.stacks.Flush(path); ok {
			pending.EndOffset = 0
			persisted, err := agent.processLine(pending, now)
			logs = append(logs, persisted...)
			if err != nil && pollErr == nil {
				pollErr = err
			}
		}
	}
	for _, line := range lines {
		for _, completed := range agent.stacks.Add(line, now) {
			persisted, err := agent.processLine(completed, now)
			logs = append(logs, persisted...)
			if err != nil && pollErr == nil {
				pollErr = err
			}
		}
	}
	idleFor := agent.config.FlushInterval.Value()
	if idleFor <= 0 {
		idleFor = 5 * time.Second
	}
	for _, file := range agent.config.Files {
		if pending, ok := agent.stacks.FlushIdle(file.Path, now, idleFor); ok {
			persisted, err := agent.processLine(pending, now)
			logs = append(logs, persisted...)
			if err != nil && pollErr == nil {
				pollErr = err
			}
		}
	}
	if len(agent.batch) == 0 {
		if err := agent.commitOffsets(); err != nil && pollErr == nil {
			pollErr = err
		}
	}
	return logs, pollErr
}

func (agent *Agent) Flush(now time.Time) ([]schema.Log, error) {
	logs := make([]schema.Log, 0)
	for _, file := range agent.config.Files {
		if pending, ok := agent.stacks.Flush(file.Path); ok {
			persisted, err := agent.processLine(pending, now)
			logs = append(logs, persisted...)
			if err != nil {
				return logs, err
			}
		}
	}
	persisted, err := agent.flushBatch()
	return append(logs, persisted...), err
}

func (agent *Agent) FlushBatch() ([]schema.Log, error) {
	return agent.flushBatch()
}

func (agent *Agent) processLine(line FileLine, now time.Time) ([]schema.Log, error) {
	logRecord, keep, reason := agent.processor.ProcessDetailed(line, now)
	agent.batchOffsets[line.File.Path] = line.EndOffset
	if !keep {
		switch reason {
		case FilteredByLevel:
			agent.filteredByLevel++
		case FilteredBySampling:
			agent.filteredBySample++
		}
		return nil, nil
	}
	agent.batch = append(agent.batch, logRecord)
	if len(agent.batch) < agent.config.BatchSize {
		return nil, nil
	}
	return agent.flushBatch()
}

func (agent *Agent) flushBatch() ([]schema.Log, error) {
	if len(agent.batch) == 0 {
		return nil, agent.commitOffsets()
	}
	if agent.pendingBatch == "" {
		path, err := agent.store.WriteBatch(agent.batch)
		if err != nil {
			return nil, err
		}
		agent.pendingBatch = path
	}
	if err := agent.commitOffsets(); err != nil {
		return nil, err
	}
	persisted := append([]schema.Log(nil), agent.batch...)
	agent.batch = nil
	agent.pendingBatch = ""
	return persisted, nil
}

func (agent *Agent) commitOffsets() error {
	if len(agent.batchOffsets) == 0 {
		return nil
	}
	if err := agent.tailer.CommitOffsets(agent.batchOffsets); err != nil {
		return err
	}
	agent.batchOffsets = make(map[string]int64)
	return nil
}

func (agent *Agent) DrainFilterCounts() FilterCounts {
	counts := FilterCounts{Level: agent.filteredByLevel, Sampling: agent.filteredBySample}
	agent.filteredByLevel = 0
	agent.filteredBySample = 0
	return counts
}

func (agent *Agent) PendingBatchCount() (int, error) {
	return agent.store.PendingCount()
}
