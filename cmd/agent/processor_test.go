package main

import (
	"testing"
	"time"
)

func TestProcessorFiltersSamplesThenRedacts(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name        string
		minLevel    string
		sampling    map[string]float64
		random      float64
		line        string
		wantKeep    bool
		wantRedact  bool
	}{
		{
			name:     "below minimum level is dropped",
			minLevel: "warn",
			sampling: map[string]float64{"info": 100},
			random:   0,
			line:     `{"level":"info","message":"password=hunter2"}`,
			wantKeep: false,
		},
		{
			name:       "sampling drops outside percentage",
			minLevel:   "trace",
			sampling:   map[string]float64{"debug": 20},
			random:     0.21,
			line:       `{"level":"debug","message":"ordinary"}`,
			wantKeep:   false,
		},
		{
			name:       "kept record is redacted after filtering",
			minLevel:   "warn",
			sampling:   map[string]float64{"warn": 100},
			random:     0.9,
			line:       `{"level":"WARNING","message":"password=hunter2","host":"spoofed-host","env":"spoofed-env"}`,
			wantKeep:   true,
			wantRedact: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := Config{Host: "agent-host", Env: "dev", MinLevel: test.minLevel, Sampling: test.sampling}
			processor := NewLogProcessor(config)
			processor.randomUnit = func() float64 { return test.random }
			log, keep := processor.Process(FileLine{
				File: FileConfig{Path: "app.log", Format: "json", TimeZone: "Local"},
				Text: test.line,
			}, now)
			if keep != test.wantKeep {
				t.Fatalf("keep = %t, want %t", keep, test.wantKeep)
			}
			if !keep {
				return
			}
			if log.Redacted != test.wantRedact {
				t.Errorf("Redacted = %t, want %t", log.Redacted, test.wantRedact)
			}
			if log.Host != "agent-host" || log.Env != "dev" {
				t.Errorf("host/env = %q/%q, want configured defaults", log.Host, log.Env)
			}
			if test.wantRedact && log.Message != "password=[REDACTED]" {
				t.Error("password was not redacted")
			}
			if log.Attributes["timestamp_missing"] != "true" {
				t.Error("missing timestamp was not flagged")
			}
		})
	}
}
