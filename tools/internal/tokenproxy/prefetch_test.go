package tokenproxy

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrefetch(t *testing.T) {
	s := time.Second
	tests := []struct {
		name        string
		failures    int // fetches that fail before one succeeds; -1 = always fail
		cancelAfter int // cancel the context inside the Nth sleep; 0 = never
		wantFetches int
		wantSleeps  []time.Duration
		wantErr     bool
	}{
		{name: "succeeds first try", failures: 0, wantFetches: 1},
		{name: "fails twice then succeeds", failures: 2, wantFetches: 3, wantSleeps: []time.Duration{1 * s, 2 * s}},
		{
			name: "backoff caps at 30s", failures: -1, cancelAfter: 9, wantFetches: 9, wantErr: true,
			wantSleeps: []time.Duration{1 * s, 2 * s, 4 * s, 8 * s, 16 * s, 30 * s, 30 * s, 30 * s, 30 * s},
		},
		{name: "context cancelled stops it", failures: -1, cancelAfter: 1, wantFetches: 1, wantErr: true, wantSleeps: []time.Duration{1 * s}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var fetches atomic.Int32
			ts := tokenFunc(func() (string, error) {
				n := int(fetches.Add(1))
				if tt.failures < 0 || n <= tt.failures {
					return "", errors.New("unexpected status 503")
				}
				return "tok", nil
			})
			var sleeps []time.Duration
			sleep := func(ctx context.Context, d time.Duration) error {
				sleeps = append(sleeps, d)
				if tt.cancelAfter > 0 && len(sleeps) == tt.cancelAfter {
					cancel()
					return ctx.Err()
				}
				return nil
			}

			err := Prefetch(ctx, ts, quiet, sleep)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", err)
			}
			if int(fetches.Load()) != tt.wantFetches {
				t.Errorf("fetches = %d, want %d", fetches.Load(), tt.wantFetches)
			}
			if !slices.Equal(sleeps, tt.wantSleeps) {
				t.Errorf("sleeps = %v, want %v", sleeps, tt.wantSleeps)
			}
		})
	}
}

func TestSleepContext(t *testing.T) {
	if err := SleepContext(context.Background(), time.Millisecond); err != nil {
		t.Errorf("uncancelled sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled sleep: %v", err)
	}
}
