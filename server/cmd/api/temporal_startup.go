package main

import (
	"context"
	"log/slog"
	"time"

	tclient "go.temporal.io/sdk/client"
)

const (
	temporalHealthCheckTimeout = 5 * time.Second
	temporalStartupMaxAttempts = 30 // about 30 minutes at the 1 minute backoff cap
)

// temporalHealthChecker is the slice of tclient.Client the startup helper needs.
type temporalHealthChecker interface {
	CheckHealth(ctx context.Context, request *tclient.CheckHealthRequest) (*tclient.CheckHealthResponse, error)
}

// temporalStartupStep is an idempotent "ensure this workflow exists" action.
type temporalStartupStep struct {
	name string
	run  func(ctx context.Context) error
}

// waitForTemporal blocks until the Temporal frontend answers a health check,
// backing off from initial up to maxDelay between attempts. It returns false when
// ctx is cancelled first.
func waitForTemporal(ctx context.Context, client temporalHealthChecker, initial, maxDelay time.Duration) bool {
	delay := initial
	for {
		checkCtx, cancel := context.WithTimeout(ctx, temporalHealthCheckTimeout)
		_, err := client.CheckHealth(checkCtx, &tclient.CheckHealthRequest{})
		cancel()
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		slog.Warn("Temporal not reachable yet, will retry", "retry_in", delay, "error", err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(delay):
		}
		delay = min(delay*2, maxDelay)
	}
}

// runTemporalStartupSteps waits for Temporal, then runs each step. Steps that
// fail are retried (with backoff) a bounded number of times; the API never
// blocks or fails startup on Temporal being down, because the client is lazy
// and every other Temporal call reconnects on first use.
func runTemporalStartupSteps(ctx context.Context, client temporalHealthChecker, steps []temporalStartupStep, initial, maxDelay time.Duration) {
	if !waitForTemporal(ctx, client, initial, maxDelay) {
		return
	}
	slog.Info("Temporal reachable, ensuring scheduled workflows")
	pending := steps
	delay := initial
	for attempt := 1; len(pending) > 0; attempt++ {
		var failed []temporalStartupStep
		for _, step := range pending {
			if err := step.run(ctx); err != nil {
				slog.Error("failed to ensure "+step.name, "attempt", attempt, "error", err)
				failed = append(failed, step)
			}
		}
		pending = failed
		if len(pending) == 0 || attempt >= temporalStartupMaxAttempts {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, maxDelay)
	}
}
