package main

import (
	"context"
	"errors"
	"testing"
	"time"

	tclient "go.temporal.io/sdk/client"
)

type fakeHealthChecker struct {
	failures int
	calls    int
}

func (f *fakeHealthChecker) CheckHealth(context.Context, *tclient.CheckHealthRequest) (*tclient.CheckHealthResponse, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, errors.New("connection refused")
	}
	return &tclient.CheckHealthResponse{}, nil
}

func TestRunTemporalStartupStepsWaitsForTemporalThenRunsSteps(t *testing.T) {
	checker := &fakeHealthChecker{failures: 3}
	ran := 0
	runTemporalStartupSteps(context.Background(), checker, []temporalStartupStep{
		{"step", func(context.Context) error { ran++; return nil }},
	}, time.Millisecond, 2*time.Millisecond)
	if checker.calls != 4 {
		t.Fatalf("CheckHealth calls = %d, want 4 (3 failures then success)", checker.calls)
	}
	if ran != 1 {
		t.Fatalf("step ran %d times, want 1", ran)
	}
}

func TestRunTemporalStartupStepsRetriesOnlyFailedSteps(t *testing.T) {
	checker := &fakeHealthChecker{}
	okRuns, flakyRuns := 0, 0
	runTemporalStartupSteps(context.Background(), checker, []temporalStartupStep{
		{"ok", func(context.Context) error { okRuns++; return nil }},
		{"flaky", func(context.Context) error {
			flakyRuns++
			if flakyRuns < 3 {
				return errors.New("transient")
			}
			return nil
		}},
	}, time.Millisecond, 2*time.Millisecond)
	if okRuns != 1 || flakyRuns != 3 {
		t.Fatalf("okRuns=%d flakyRuns=%d, want 1 and 3", okRuns, flakyRuns)
	}
}

func TestRunTemporalStartupStepsGivesUpAfterMaxAttempts(t *testing.T) {
	runs := 0
	runTemporalStartupSteps(context.Background(), &fakeHealthChecker{}, []temporalStartupStep{
		{"broken", func(context.Context) error { runs++; return errors.New("permanent") }},
	}, time.Millisecond, 2*time.Millisecond)
	if runs != temporalStartupMaxAttempts {
		t.Fatalf("step ran %d times, want %d", runs, temporalStartupMaxAttempts)
	}
}

func TestWaitForTemporalStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	checker := &fakeHealthChecker{failures: 1 << 30}
	done := make(chan bool, 1)
	go func() { done <- waitForTemporal(ctx, checker, time.Millisecond, 2*time.Millisecond) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("waitForTemporal returned true for an unreachable server")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForTemporal did not return after context cancellation")
	}
}

// A lazy client must construct without a reachable server; this is the seam
// that fixes the API staying without Temporal after starting first.
func TestNewLazyClientDoesNotRequireReachableServer(t *testing.T) {
	client, err := tclient.NewLazyClient(tclient.Options{HostPort: "127.0.0.1:1", Namespace: "default"})
	if err != nil {
		t.Fatalf("NewLazyClient() error = %v, want nil with an unreachable server", err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := client.CheckHealth(ctx, &tclient.CheckHealthRequest{}); err == nil {
		t.Fatal("CheckHealth() succeeded against a closed port")
	}
}
