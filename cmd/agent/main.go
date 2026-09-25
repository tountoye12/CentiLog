package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"centilog/internal/config"
	"centilog/internal/schema"
)

func main() {
	configPath := config.GetEnv("CENTILOG_AGENT_CONFIG", "configs/agent.yaml")
	agentConfig, err := LoadConfig(configPath)
	if err != nil {
		log.Print("agent configuration failed")
		os.Exit(1)
	}
	agent, err := NewAgent(agentConfig)
	if err != nil {
		log.Print("agent initialization failed")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sender := NewSender(agentConfig, agent.store)
	senderDone := make(chan struct{})
	go func() {
		defer close(senderDone)
		sender.Run(ctx)
	}()
	log.Printf("agent polling %d files and sending buffered batches", len(agentConfig.Files))
	runAgent(ctx, agent, agentConfig.FlushInterval.Value())
	stop()
	<-senderDone
}

func runAgent(ctx context.Context, agent *Agent, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	processAvailable(agent)
	for {
		select {
		case <-ctx.Done():
			logs, err := agent.Flush(time.Now().UTC())
			if err != nil {
				log.Print("agent shutdown flush failed; buffered data and offsets were preserved")
			}
			logBuffered(len(logs))
			logFilterCounts(agent.DrainFilterCounts())
			return
		case <-ticker.C:
			processAvailable(agent)
		}
	}
}

func processAvailable(agent *Agent) {
	logs, err := agent.Poll(time.Now().UTC())
	if err != nil {
		log.Print("agent file polling failed")
	}
	buffered, flushErr := agent.FlushBatch()
	if flushErr != nil {
		log.Print("agent batch flush failed; source offsets were not advanced")
	}
	logBuffered(len(logs) + len(buffered))
	logFilterCounts(agent.DrainFilterCounts())
}

func logBuffered(count int) {
	if count > 0 {
		log.Printf("durably buffered %d redacted log records", count)
	}
}

func logFilterCounts(counts FilterCounts) {
	if counts.Level > 0 || counts.Sampling > 0 {
		log.Printf("filtered logs: level=%d sampling=%d", counts.Level, counts.Sampling)
	}
}
