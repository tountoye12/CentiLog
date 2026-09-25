package schema

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxMessageLength            = 16 * 1024
	MaxFutureTimestampSkew      = 5 * time.Minute
	TimestampMissingAttribute   = "timestamp_missing"
	TimestampCorrectedAttribute = "timestamp_corrected"
	TimestampSkewMillisAttribute = "timestamp_skew_ms"
)

// Log is a normalized Centilog record. Timestamps are UTC at millisecond precision.
type Log struct {
	Timestamp     time.Time         `json:"timestamp"`
	Service       string            `json:"service"`
	Host          string            `json:"host"`
	Env           string            `json:"env"`
	Level         string            `json:"level"`
	Message       string            `json:"message"`
	TraceID       string            `json:"trace_id"`
	Attributes    map[string]string `json:"attributes"`
	Redacted      bool              `json:"redacted"`
	IngestedAt    time.Time         `json:"ingested_at"`
	RetentionDays uint16            `json:"retention_days"`
}

// NormalizeLevel maps common severity names to the Centilog levels.
func NormalizeLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace", "verbose", "finest":
		return "trace"
	case "debug", "dbg":
		return "debug"
	case "info", "informational", "notice":
		return "info"
	case "warn", "warning", "wrn":
		return "warn"
	case "error", "err", "exception":
		return "error"
	case "fatal", "critical", "crit", "panic", "emergency", "alert":
		return "fatal"
	default:
		return "info"
	}
}

// LevelRank returns the ascending severity rank used when filtering logs.
func LevelRank(level string) int {
	switch NormalizeLevel(level) {
	case "trace":
		return 0
	case "debug":
		return 1
	case "info":
		return 2
	case "warn":
		return 3
	case "error":
		return 4
	case "fatal":
		return 5
	default:
		return 2
	}
}

// Normalize validates and canonicalizes a log using the current time as its reference.
func Normalize(log Log) (Log, error) {
	return NormalizeAt(log, time.Now())
}

// NormalizeAt is Normalize with an explicit reference time, useful for deterministic callers and tests.
func NormalizeAt(log Log, now time.Time) (Log, error) {
	if strings.TrimSpace(log.Service) == "" {
		return log, fmt.Errorf("service is required")
	}
	if strings.TrimSpace(log.Host) == "" {
		return log, fmt.Errorf("host is required")
	}
	if strings.TrimSpace(log.Env) == "" {
		return log, fmt.Errorf("env is required")
	}
	if strings.TrimSpace(log.Message) == "" {
		return log, fmt.Errorf("message is required")
	}
	if log.RetentionDays == 0 {
		return log, fmt.Errorf("retention_days must be greater than zero")
	}
	return NormalizeFieldsAt(log, now), nil
}

// NormalizeFieldsAt canonicalizes a partial log without requiring ingest-owned fields.
func NormalizeFieldsAt(log Log, now time.Time) Log {
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowMillis := now.Truncate(time.Millisecond)
	log.Level = NormalizeLevel(log.Level)
	log.Attributes = cloneAttributes(log.Attributes)

	if log.Timestamp.IsZero() {
		log.Timestamp = nowMillis
		log.Attributes[TimestampMissingAttribute] = "true"
	} else {
		originalTimestamp := log.Timestamp.UTC()
		if originalTimestamp.After(now.Add(MaxFutureTimestampSkew)) {
			skew := originalTimestamp.Sub(now)
			log.Timestamp = nowMillis
			log.Attributes[TimestampCorrectedAttribute] = "true"
			log.Attributes[TimestampSkewMillisAttribute] = fmt.Sprintf("%d", skew.Milliseconds())
		} else {
			log.Timestamp = originalTimestamp.Truncate(time.Millisecond)
		}
	}

	if log.IngestedAt.IsZero() {
		log.IngestedAt = nowMillis
	} else {
		log.IngestedAt = log.IngestedAt.UTC().Truncate(time.Millisecond)
	}
	log.Message = truncateMessage(log.Message, MaxMessageLength)
	return log
}

func cloneAttributes(attributes map[string]string) map[string]string {
	clone := make(map[string]string, len(attributes)+2)
	for key, value := range attributes {
		clone[key] = value
	}
	return clone
}

func truncateMessage(message string, maxBytes int) string {
	if len(message) <= maxBytes {
		return message
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(message[end]) {
		end--
	}
	return message[:end]
}
