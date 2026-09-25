package main

import (
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"centilog/internal/schema"
	"gopkg.in/yaml.v3"
	_ "time/tzdata"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar")
	}
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration")
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

type Config struct {
	ServerURL       string             `yaml:"server_url"`
	APIKey          string             `yaml:"api_key"`
	Host            string             `yaml:"host"`
	Env             string             `yaml:"env"`
	BufferDir       string             `yaml:"buffer_dir"`
	MaxBufferSizeMB int                `yaml:"max_buffer_size_mb"`
	BatchSize       int                `yaml:"batch_size"`
	FlushInterval   Duration           `yaml:"flush_interval"`
	MinLevel        string             `yaml:"min_level"`
	Sampling        map[string]float64 `yaml:"sampling"`
	Files           []FileConfig       `yaml:"files"`
}

type FileConfig struct {
	Path     string `yaml:"path"`
	Format   string `yaml:"format"`
	TimeZone string `yaml:"time_zone"`
	location *time.Location
}

func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open agent config: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode agent config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("agent config must contain one YAML document")
		}
		return Config{}, fmt.Errorf("decode agent config: %w", err)
	}

	if err := config.applyDefaults(); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) applyDefaults() error {
	if config.Host == "" {
		host, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("get machine hostname: %w", err)
		}
		config.Host = host
	}
	if config.Env == "" {
		config.Env = "development"
	}
	if config.BufferDir == "" {
		config.BufferDir = filepath.Join("data", "agent-buffer")
	}
	if config.MaxBufferSizeMB == 0 {
		config.MaxBufferSizeMB = 512
	}
	if config.BatchSize == 0 {
		config.BatchSize = 500
	}
	if config.FlushInterval == 0 {
		config.FlushInterval = Duration(5 * time.Second)
	}
	if config.MinLevel == "" {
		config.MinLevel = "trace"
	}
	if config.Sampling == nil {
		config.Sampling = make(map[string]float64)
	}
	if len(config.Files) == 0 {
		return fmt.Errorf("at least one watched file is required")
	}
	return nil
}

func (config *Config) Validate() error {
	parsedURL, err := url.ParseRequestURI(config.ServerURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return fmt.Errorf("server_url must be an HTTP or HTTPS URL")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("api_key is required")
	}
	if config.MaxBufferSizeMB < 1 {
		return fmt.Errorf("max_buffer_size_mb must be positive")
	}
	if config.BatchSize < 1 {
		return fmt.Errorf("batch_size must be positive")
	}
	if config.FlushInterval.Value() <= 0 {
		return fmt.Errorf("flush_interval must be positive")
	}

	var valid bool
	config.MinLevel, valid = normalizeConfiguredLevel(config.MinLevel)
	if !valid {
		return fmt.Errorf("min_level is not supported")
	}
	normalizedSampling := make(map[string]float64, len(config.Sampling))
	for level, percentage := range config.Sampling {
		normalizedLevel, valid := normalizeConfiguredLevel(level)
		if !valid || math.IsNaN(percentage) || math.IsInf(percentage, 0) || percentage < 0 || percentage > 100 {
			return fmt.Errorf("sampling levels must be valid and percentages must be from 0 to 100")
		}
		if _, exists := normalizedSampling[normalizedLevel]; exists {
			return fmt.Errorf("sampling level is configured more than once")
		}
		normalizedSampling[normalizedLevel] = percentage
	}
	config.Sampling = normalizedSampling

	seenPaths := make(map[string]struct{}, len(config.Files))
	for i := range config.Files {
		file := &config.Files[i]
		if strings.TrimSpace(file.Path) == "" {
			return fmt.Errorf("each watched file must have a path")
		}
		file.Format = strings.ToLower(strings.TrimSpace(file.Format))
		if file.Format == "" {
			file.Format = "auto"
		}
		if file.Format != "auto" && file.Format != "json" && file.Format != "syslog" && file.Format != "text" {
			return fmt.Errorf("file format must be auto, json, syslog, or text")
		}
		cleanPath := filepath.Clean(file.Path)
		if _, exists := seenPaths[cleanPath]; exists {
			return fmt.Errorf("watched file paths must be unique")
		}
		seenPaths[cleanPath] = struct{}{}

		file.TimeZone = strings.TrimSpace(file.TimeZone)
		if file.TimeZone == "" || strings.EqualFold(file.TimeZone, "local time") {
			file.TimeZone = "Local"
		}
		if strings.EqualFold(file.TimeZone, "local") {
			file.location = time.Local
			continue
		}
		location, err := time.LoadLocation(file.TimeZone)
		if err != nil {
			return fmt.Errorf("unknown time zone for watched file")
		}
		file.location = location
	}
	return nil
}

func (config Config) SamplingPercentage(level string) float64 {
	if percentage, ok := config.Sampling[schema.NormalizeLevel(level)]; ok {
		return percentage
	}
	return 100
}

func normalizeConfiguredLevel(level string) (string, bool) {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "trace", "debug", "info", "warn", "warning", "error", "err", "fatal", "critical":
		return schema.NormalizeLevel(level), true
	default:
		return "", false
	}
}

func fileLocation(file FileConfig) *time.Location {
	if file.location != nil {
		return file.location
	}
	if file.TimeZone == "" || strings.EqualFold(file.TimeZone, "local") || strings.EqualFold(file.TimeZone, "local time") {
		return time.Local
	}
	location, err := time.LoadLocation(file.TimeZone)
	if err != nil {
		return time.Local
	}
	return location
}
