package main

import (
	"context"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	initialRetryDelay = time.Second
	maximumRetryDelay = time.Minute
	senderPollDelay   = time.Second
)

type sendDisposition uint8

const (
	sendSucceeded sendDisposition = iota
	sendRetry
	sendRateLimited
	sendFailed
)

type sendResult struct {
	disposition sendDisposition
	retryAfter  time.Duration
	hasRetry    bool
}

type Sender struct {
	store             *BatchStore
	apiKey            string
	endpoint          string
	client            *http.Client
	now               func() time.Time
	lastClockWarning  time.Time
	warn              func(string, ...any)
}

func NewSender(config Config, store *BatchStore) *Sender {
	return &Sender{
		store:    store,
		apiKey:   config.APIKey,
		endpoint: strings.TrimRight(config.ServerURL, "/") + "/v1/logs",
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now:  time.Now,
		warn: log.Printf,
	}
}

func (sender *Sender) Run(ctx context.Context) {
	delay := initialRetryDelay
	for ctx.Err() == nil {
		claimedPath, err := sender.store.ClaimOldest()
		if err != nil {
			sender.warn("could not claim buffered batch")
			if !waitContext(ctx, senderPollDelay) {
				return
			}
			continue
		}
		if claimedPath == "" {
			if !waitContext(ctx, senderPollDelay) {
				return
			}
			continue
		}

		result := sender.sendBatch(ctx, claimedPath)
		switch result.disposition {
		case sendSucceeded:
			if err := sender.store.DeleteClaim(claimedPath); err != nil {
				sender.warn("could not delete delivered batch")
				_ = sender.store.Unclaim(claimedPath)
				if !waitContext(ctx, delay) {
					return
				}
				delay = nextRetryDelay(delay)
				continue
			}
			delay = initialRetryDelay
		case sendFailed:
			if err := sender.store.MoveToFailed(claimedPath); err != nil {
				sender.warn("could not quarantine rejected batch")
				_ = sender.store.Unclaim(claimedPath)
				if !waitContext(ctx, delay) {
					return
				}
				delay = nextRetryDelay(delay)
			}
		case sendRateLimited:
			_ = sender.store.Unclaim(claimedPath)
			if result.hasRetry {
				if !waitContext(ctx, result.retryAfter) {
					return
				}
			} else {
				if !waitContext(ctx, delay) {
					return
				}
				delay = nextRetryDelay(delay)
			}
		case sendRetry:
			_ = sender.store.Unclaim(claimedPath)
			sender.warn("ingest request failed; retrying in %s", delay)
			if !waitContext(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
		}
	}
}

func (sender *Sender) sendBatch(ctx context.Context, path string) sendResult {
	file, err := os.Open(path)
	if err != nil {
		return sendResult{disposition: sendRetry}
	}
	defer file.Close()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sender.endpoint, file)
	if err != nil {
		return sendResult{disposition: sendRetry}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("X-Api-Key", sender.apiKey)

	response, err := sender.client.Do(request)
	if err != nil {
		return sendResult{disposition: sendRetry}
	}
	defer response.Body.Close()
	sender.checkServerClock(response.Header.Get("Date"))

	switch response.StatusCode {
	case http.StatusAccepted:
		_, _ = io.Copy(io.Discard, response.Body)
		return sendResult{disposition: sendSucceeded}
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return sendResult{disposition: sendFailed}
	case http.StatusTooManyRequests:
		delay, ok := parseRetryAfter(response.Header.Get("Retry-After"), sender.now())
		return sendResult{disposition: sendRateLimited, retryAfter: delay, hasRetry: ok}
	default:
		return sendResult{disposition: sendRetry}
	}
}

func (sender *Sender) checkServerClock(dateHeader string) {
	if dateHeader == "" {
		return
	}
	serverTime, err := http.ParseTime(dateHeader)
	if err != nil {
		return
	}
	now := sender.now()
	difference := now.Sub(serverTime)
	if difference < 0 {
		difference = -difference
	}
	if difference <= 3*time.Second || (!sender.lastClockWarning.IsZero() && now.Sub(sender.lastClockWarning) < time.Minute) {
		return
	}
	sender.lastClockWarning = now
	sender.warn("server clock differs from local clock by %s; check NTP", difference.Round(time.Millisecond))
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 {
		if seconds > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64), true
		}
		return time.Duration(seconds) * time.Second, true
	}
	if retryTime, err := http.ParseTime(value); err == nil {
		delay := retryTime.Sub(now)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}

func nextRetryDelay(delay time.Duration) time.Duration {
	if delay >= maximumRetryDelay/2 {
		return maximumRetryDelay
	}
	return delay * 2
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
