package tokenproxy

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	prefetchInitialBackoff = time.Second
	prefetchMaxBackoff     = 30 * time.Second
)

// Prefetch fetches a token from ts until one succeeds, so /healthz can turn
// healthy before any request arrives. Failures are logged (the error text
// never contains the secret or token) and retried with exponential backoff
// from 1s, doubling, capped at 30s. sleep waits for d or returns the
// context's error when ctx ends; Prefetch then returns that error.
func Prefetch(ctx context.Context, ts TokenSource, log *slog.Logger, sleep func(context.Context, time.Duration) error) error {
	backoff := prefetchInitialBackoff
	for {
		_, err := ts.Token(ctx)
		if err == nil {
			log.Info("first token fetched")
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("prefetch token: %w", ctx.Err())
		}
		log.Error("prefetch token", "error", err, "retry_in", backoff.String())
		if err := sleep(ctx, backoff); err != nil {
			return fmt.Errorf("wait to retry token fetch: %w", err)
		}
		backoff = min(backoff*2, prefetchMaxBackoff)
	}
}

// SleepContext waits for d or until ctx ends, returning the context's error
// in that case.
func SleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
