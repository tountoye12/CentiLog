package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"centilog/internal/schema"
)

var (
	syslog5424Pattern = regexp.MustCompile(`^<([0-9]{1,3})>([0-9]+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)(\s+(.*))?$`)
	syslogPattern = regexp.MustCompile(`^\s*(<([0-9]{1,3})>)?([A-Z][a-z]{2}\s+[0-9]{1,2}\s+[0-9]{2}:[0-9]{2}:[0-9]{2})\s+(\S+)\s+([^:]+):\s?(.*)$`)
	textPattern   = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})?)\s+\[?([A-Za-z]+)\]?\s+(.*)$`)
	levelPattern  = regexp.MustCompile(`(?i)\b(trace|debug|info|warning|warn|error|err|critical|fatal|panic)\b`)
)

func ParseLine(line string, file FileConfig, now time.Time) schema.Log {
	location := fileLocation(file)
	format := strings.ToLower(strings.TrimSpace(file.Format))
	if format == "" {
		format = "auto"
	}

	var parsed schema.Log
	var ok bool
	switch format {
	case "json":
		parsed, ok = parseJSONLine(line, location)
	case "syslog":
		parsed, ok = parseSyslogLine(line, location, now)
	case "text":
		parsed, ok = parseTextLine(line, location)
	default:
		parsed, ok = parseJSONLine(line, location)
		if !ok {
			parsed, ok = parseSyslogLine(line, location, now)
		}
		if !ok {
			parsed, ok = parseTextLine(line, location)
		}
	}
	if !ok {
		parsed = schema.Log{Message: line, Level: guessLevel(line)}
	}
	if parsed.Message == "" {
		parsed.Message = line
	}
	if parsed.Level == "" {
		parsed.Level = guessLevel(parsed.Message)
	}
	parsed.Level = schema.NormalizeLevel(parsed.Level)
	return parsed
}

func parseJSONLine(line string, location *time.Location) (schema.Log, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil || fields == nil {
		return schema.Log{}, false
	}

	log := schema.Log{Attributes: make(map[string]string)}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		raw := fields[key]
		normalizedKey := normalizeFieldName(key)
		switch normalizedKey {
		case "timestamp", "time", "ts", "datetime", "date", "eventtime":
			if timestamp, ok := parseJSONTimestamp(raw, location); ok {
				log.Timestamp = timestamp
			} else {
				log.Attributes[key] = rawValue(raw)
			}
		case "level", "severity", "loglevel":
			log.Level = rawValue(raw)
		case "priority":
			priority, err := strconv.Atoi(rawValue(raw))
			if err == nil && priority >= 0 && priority <= 191 {
				log.Level = syslogLevel(priority % 8)
			} else {
				log.Level = rawValue(raw)
			}
		case "message", "msg", "log", "body", "event":
			log.Message = rawValue(raw)
		case "traceid":
			log.TraceID = rawValue(raw)
		case "service", "servicename", "application", "app":
			log.Service = rawValue(raw)
		case "host", "hostname", "sourcehost":
			log.Host = rawValue(raw)
		case "env", "environment", "environmentname":
			log.Env = rawValue(raw)
		case "attributes":
			var attributes map[string]json.RawMessage
			if json.Unmarshal(raw, &attributes) == nil {
				for attributeKey, value := range attributes {
					log.Attributes[attributeKey] = rawValue(value)
				}
			}
		case "redacted":
			_ = json.Unmarshal(raw, &log.Redacted)
		case "ingestedat":
			if ingestedAt, ok := parseJSONTimestamp(raw, location); ok {
				log.IngestedAt = ingestedAt
			}
		case "retentiondays":
			if retentionDays, err := strconv.ParseUint(rawValue(raw), 10, 16); err == nil {
				log.RetentionDays = uint16(retentionDays)
			}
		default:
			log.Attributes[key] = rawValue(raw)
		}
	}
	return log, true
}

func parseJSONTimestamp(raw json.RawMessage, location *time.Location) (time.Time, bool) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return time.Time{}, false
	}
	switch typed := value.(type) {
	case json.Number:
		return parseEpoch(typed.String())
	case string:
		if timestamp, ok := parseEpoch(typed); ok {
			return timestamp, true
		}
		return parseTimestampString(typed, location)
	default:
		return time.Time{}, false
	}
}

func parseEpoch(value string) (time.Time, bool) {
	epoch, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(epoch) || math.IsInf(epoch, 0) {
		return time.Time{}, false
	}
	if math.Abs(epoch) >= 100_000_000_000 {
		epoch /= 1000
	}
	seconds := int64(math.Floor(epoch))
	nanoseconds := int64((epoch - float64(seconds)) * float64(time.Second))
	return time.Unix(seconds, nanoseconds).UTC(), true
}

func parseTimestampString(value string, location *time.Location) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00"} {
		if timestamp, err := time.Parse(layout, value); err == nil {
			return timestamp.UTC(), true
		}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if timestamp, err := time.ParseInLocation(layout, value, location); err == nil {
			return timestamp.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseSyslogLine(line string, location *time.Location, now time.Time) (schema.Log, bool) {
	if matches := syslog5424Pattern.FindStringSubmatch(line); len(matches) == 11 {
		priority, err := strconv.Atoi(matches[1])
		if err != nil || priority > 191 {
			return schema.Log{}, false
		}
		var timestamp time.Time
		if matches[3] != "-" {
			parsed, ok := parseTimestampString(matches[3], location)
			if !ok {
				return schema.Log{}, false
			}
			timestamp = parsed
		}
		attributes := map[string]string{
			"syslog_program":          matches[5],
			"syslog_procid":           matches[6],
			"syslog_msgid":            matches[7],
			"syslog_structured_data":   matches[8],
		}
		return schema.Log{
			Timestamp:  timestamp,
			Host:       matches[4],
			Level:      syslogLevel(priority % 8),
			Message:    matches[10],
			Attributes: attributes,
		}, true
	}
	matches := syslogPattern.FindStringSubmatch(line)
	if len(matches) != 7 {
		return schema.Log{}, false
	}
	dateWithYear := fmt.Sprintf("%s %d", matches[3], now.In(location).Year())
	timestamp, err := time.ParseInLocation("Jan _2 15:04:05 2006", dateWithYear, location)
	if err != nil {
		return schema.Log{}, false
	}

	level := guessLevel(matches[6])
	if matches[2] != "" {
		priority, parseErr := strconv.Atoi(matches[2])
		if parseErr != nil || priority > 191 {
			return schema.Log{}, false
		}
		level = syslogLevel(priority % 8)
	}
	attributes := map[string]string{"syslog_program": matches[5]}
	return schema.Log{
		Timestamp:  timestamp.UTC(),
		Host:       matches[4],
		Level:      level,
		Message:    matches[6],
		Attributes: attributes,
	}, true
}

func syslogLevel(severity int) string {
	switch severity {
	case 0, 1, 2:
		return "fatal"
	case 3:
		return "error"
	case 4:
		return "warn"
	case 5, 6:
		return "info"
	case 7:
		return "debug"
	default:
		return "info"
	}
}

func parseTextLine(line string, location *time.Location) (schema.Log, bool) {
	matches := textPattern.FindStringSubmatch(line)
	if len(matches) != 6 {
		return schema.Log{}, false
	}
	timestamp, ok := parseTimestampString(matches[1], location)
	if !ok {
		return schema.Log{}, false
	}
	return schema.Log{Timestamp: timestamp, Level: matches[4], Message: matches[5]}, true
}

func guessLevel(message string) string {
	matches := levelPattern.FindStringSubmatch(message)
	if len(matches) == 2 {
		return schema.NormalizeLevel(matches[1])
	}
	return "info"
}

func normalizeFieldName(name string) string {
	name = strings.ToLower(name)
	return strings.NewReplacer("_", "", "-", "", ".", "", "@", "").Replace(name)
}

func rawValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return strings.TrimSpace(string(raw))
}
