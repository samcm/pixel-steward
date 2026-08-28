package controller

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samcm/pixel-steward/internal/agent"
	"github.com/samcm/pixel-steward/internal/config"
	"github.com/samcm/pixel-steward/internal/display"
	"github.com/samcm/pixel-steward/internal/executor"
	"github.com/samcm/pixel-steward/internal/objectstore"
	"github.com/samcm/pixel-steward/internal/store"
)

type blockingRunner struct {
	mu      sync.Mutex
	started int
	done    chan struct{}
}

type recordingExecutor struct {
	executor.Disabled
	suspends int
}

func (e *recordingExecutor) Suspend(context.Context, string) error {
	e.suspends++
	return nil
}

func (r *blockingRunner) Run(ctx context.Context, _ agent.Wake) error {
	r.mu.Lock()
	r.started++
	r.mu.Unlock()
	<-ctx.Done()
	close(r.done)
	return ctx.Err()
}

func TestBlackoutCancelsWakeAndTurnsScreenOff(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	runner := &blockingRunner{done: make(chan struct{})}
	panel := display.NewFake()
	service := testService(t, &now, runner, panel)

	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, err := service.store.ActiveLease(context.Background())
	if err != nil || lease == nil {
		t.Fatalf("active lease = %+v, error = %v", lease, err)
	}
	if got := lease.EndsAt.Sub(lease.StartedAt); got != 24*time.Hour {
		t.Fatalf("lease duration = %s", got)
	}

	now = time.Date(2026, 8, 28, 21, 0, 0, 0, location)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.done:
	case <-time.After(time.Second):
		t.Fatal("wake was not cancelled at blackout")
	}
	status, err := panel.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ScreenOn {
		t.Fatal("screen remained on during blackout")
	}
	service.mu.Lock()
	token := service.tokens[lease.ID]
	service.mu.Unlock()
	if _, err := service.PublishPath(context.Background(), token, "frame.png", true); err != ErrBlackout {
		t.Fatalf("publish error = %v, want blackout", err)
	}
}

func TestNoLeaseOrInferenceStartsDuringBlackout(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 22, 0, 0, 0, location)
	runner := &blockingRunner{done: make(chan struct{})}
	panel := display.NewFake()
	service := testService(t, &now, runner, panel)
	sandbox := &recordingExecutor{}
	service.executor = sandbox
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, err := service.store.ActiveLease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if lease != nil {
		t.Fatalf("lease started during blackout: %+v", lease)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.started != 0 {
		t.Fatalf("runner started %d times", runner.started)
	}
	if sandbox.suspends != 1 {
		t.Fatalf("sandbox suspended %d times, want once", sandbox.suspends)
	}
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sandbox.suspends != 1 {
		t.Fatalf("sandbox suspended %d times after repeated tick, want once", sandbox.suspends)
	}
}

func TestExpiringTestWindowTemporarilyOverridesBlackout(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 22, 0, 0, 0, location)
	runner := &blockingRunner{done: make(chan struct{})}
	panel := display.NewFake()
	service := testService(t, &now, runner, panel)
	service.testWindowUntil = now.Add(30 * time.Minute)

	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Blackout || !status.ScheduledBlackout || status.TestWindowUntil == nil {
		t.Fatalf("unexpected test-window status: %+v", status)
	}
	if status.Lease == nil || !status.Display.ScreenOn {
		t.Fatalf("test window did not activate lease and display: %+v", status)
	}
	events, err := service.store.ListEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	promptRecorded := false
	for _, event := range events {
		if event.Type == "agent.prompt" && strings.Contains(string(event.Payload), "You have temporary creative ownership") {
			promptRecorded = true
			break
		}
	}
	if !promptRecorded {
		t.Fatal("exact wake prompt was not recorded")
	}

	now = now.Add(31 * time.Minute)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.done:
	case <-time.After(time.Second):
		t.Fatal("wake was not cancelled when test window expired")
	}
	status, err = service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Blackout || status.TestWindowUntil != nil || status.Display.ScreenOn {
		t.Fatalf("blackout was not restored after test window: %+v", status)
	}
}

func testService(t *testing.T, now *time.Time, runner agent.Runner, panel display.Display) *Service {
	t.Helper()
	cost := 10.0
	cfg := config.Config{
		Version: 1, Timezone: "Australia/Brisbane",
		Display:   config.Display{Adapter: "fake", MaxFPS: 1, Blackout: config.TimeSpan{Start: "21:00", End: "09:00"}},
		Scheduler: config.Scheduler{DefaultLease: config.Duration(24 * time.Hour), DefaultCooldown: config.Duration(time.Hour), Selection: "weighted_random", AvoidImmediateRepeat: true},
		Inference: config.Inference{LeaseBudget: config.Budget{MaxInputTokens: 100000, MaxOutputTokens: 10000,
			MaxModelCalls: 8, MaxActiveRuntime: config.Duration(time.Hour), MaxCostUSD: &cost, MaxModelSceneCommits: 20},
			PerCall: config.CallLimit{MaxOutputTokens: 2048}},
		ModelProfiles: map[string]config.ModelProfile{"model": {Provider: "test", Model: "test", Thinking: config.Thinking{Default: "low", Allowed: []string{"low"}}}},
		Personas:      []config.Persona{{ID: "persona", DisplayName: "Persona", Enabled: true, Weight: 1, ModelProfile: "model", Thinking: "low"}},
	}
	objects, err := objectstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(cfg, store.NewMemory(), objects, panel, runner, executor.Disabled{}, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return service
}
