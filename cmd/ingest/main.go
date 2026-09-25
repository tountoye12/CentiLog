package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"centilog/internal/config"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

const apiKeyRefreshInterval = 30 * time.Second

type apiKeyCache struct {
	mu      sync.RWMutex
	entries map[string]serviceIdentity
}

func newAPIKeyCache() *apiKeyCache {
	return &apiKeyCache{entries: make(map[string]serviceIdentity)}
}

func (c *apiKeyCache) Lookup(apiKey string) (serviceIdentity, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	identity, ok := c.entries[apiKey]
	return identity, ok
}

func (c *apiKeyCache) Refresh(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT name, api_key, retention_days FROM services`)
	if err != nil {
		return errors.New("failed to query API keys")
	}
	defer rows.Close()

	entries := make(map[string]serviceIdentity)
	for rows.Next() {
		var serviceName string
		var apiKey string
		var retentionDays int
		if err := rows.Scan(&serviceName, &apiKey, &retentionDays); err != nil {
			return errors.New("failed to read API key row")
		}
		if serviceName == "" || apiKey == "" || retentionDays < 1 || retentionDays > 65_535 {
			return errors.New("invalid service API key configuration")
		}
		entries[apiKey] = serviceIdentity{service: serviceName, retentionDays: uint16(retentionDays)}
	}
	if err := rows.Err(); err != nil {
		return errors.New("failed while reading API keys")
	}

	c.mu.Lock()
	c.entries = entries
	c.mu.Unlock()
	return nil
}

type redisStream struct {
	client *redis.Client
}

func (s redisStream) Length(ctx context.Context) (int64, error) {
	return s.client.XLen(ctx, config.RedisStreamName).Result()
}

func (s redisStream) AddBatch(ctx context.Context, serializedLogs []string) (int, error) {
	if len(serializedLogs) == 0 {
		return 0, nil
	}
	pipeline := s.client.Pipeline()
	commands := make([]*redis.StringCmd, 0, len(serializedLogs))
	for _, serializedLog := range serializedLogs {
		commands = append(commands, pipeline.XAdd(ctx, &redis.XAddArgs{
			Stream: config.RedisStreamName,
			Values: map[string]any{"log": serializedLog},
		}))
	}
	_, pipelineErr := pipeline.Exec(ctx)
	accepted := 0
	for _, command := range commands {
		if command.Err() == nil {
			accepted++
		}
	}
	if pipelineErr != nil || accepted != len(serializedLogs) {
		return accepted, errors.New("redis stream write failed")
	}
	return accepted, nil
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", config.PostgresDSN())
	if err != nil {
		return errors.New("failed to open PostgreSQL")
	}
	defer db.Close()
	db.SetMaxOpenConns(5)

	startupCtx, cancelStartup := context.WithTimeout(ctx, 5*time.Second)
	if err := db.PingContext(startupCtx); err != nil {
		cancelStartup()
		return errors.New("PostgreSQL is unavailable")
	}
	cache := newAPIKeyCache()
	if err := cache.Refresh(startupCtx, db); err != nil {
		cancelStartup()
		return errors.New("failed to load service API keys")
	}

	redisOptions, err := redis.ParseURL(config.RedisURL())
	if err != nil {
		cancelStartup()
		return errors.New("invalid Redis connection URL")
	}
	redisClient := redis.NewClient(redisOptions)
	if err := redisClient.Ping(startupCtx).Err(); err != nil {
		cancelStartup()
		_ = redisClient.Close()
		return errors.New("Redis is unavailable")
	}
	cancelStartup()
	defer redisClient.Close()

	go refreshAPIKeys(ctx, cache, db)
	server := &http.Server{
		Addr:              config.GetEnv("CENTILOG_INGEST_ADDR", ":8080"),
		Handler:           newHandler(cache, redisStream{client: redisClient}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- errors.New("HTTP server failed")
			return
		}
		serverErrors <- nil
	}()
	log.Printf("ingest API listening on %s", server.Addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return errors.New("graceful shutdown failed")
		}
		return nil
	case err := <-serverErrors:
		return err
	}
}

func refreshAPIKeys(ctx context.Context, cache *apiKeyCache, db *sql.DB) {
	ticker := time.NewTicker(apiKeyRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := cache.Refresh(ctx, db); err != nil {
				log.Print("API key cache refresh failed")
			}
		}
	}
}
