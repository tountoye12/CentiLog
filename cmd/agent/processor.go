package main

import (
	"math/rand"
	"time"

	"centilog/internal/redact"
	"centilog/internal/schema"
)

type LogProcessor struct {
	config     Config
	randomUnit func() float64
}

type FilterReason string

const (
	FilteredByLevel    FilterReason = "level"
	FilteredBySampling FilterReason = "sampling"
)

func NewLogProcessor(config Config) *LogProcessor {
	return &LogProcessor{config: config, randomUnit: rand.Float64}
}

// Process parses, normalizes, filters, samples, and finally redacts a complete line.
func (processor *LogProcessor) Process(line FileLine, now time.Time) (schema.Log, bool) {
	log, keep, _ := processor.ProcessDetailed(line, now)
	return log, keep
}

func (processor *LogProcessor) ProcessDetailed(line FileLine, now time.Time) (schema.Log, bool, FilterReason) {
	log := ParseLine(line.Text, line.File, now)
	log.Host = processor.config.Host
	log.Env = processor.config.Env
	log = schema.NormalizeFieldsAt(log, now)

	if schema.LevelRank(log.Level) < schema.LevelRank(processor.config.MinLevel) {
		return schema.Log{}, false, FilteredByLevel
	}
	percentage := processor.config.SamplingPercentage(log.Level)
	if percentage <= 0 || (percentage < 100 && processor.randomUnit()*100 >= percentage) {
		return schema.Log{}, false, FilteredBySampling
	}
	return redact.Redact(log), true, ""
}
