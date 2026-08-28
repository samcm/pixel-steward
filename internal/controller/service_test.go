package controller

import (
	"context"
	"errors"
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

func TestRemovedPersonaEndsActiveLease(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	service := testService(t, &now, agent.Disabled{}, display.NewFake())
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	original, err := service.store.ActiveLease(context.Background())
	if err != nil || original == nil {
		t.Fatalf("original active lease = %+v, error = %v", original, err)
	}
	service.config.Personas = []config.Persona{{ID: "replacement", DisplayName: "Replacement", Enabled: true, Weight: 1}}
	if err := service.syncPersonas(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	active, err := service.store.ActiveLease(context.Background())
	if err != nil || active == nil || active.PersonaID != "replacement" {
		t.Fatalf("replacement active lease = %+v, error = %v", active, err)
	}
	leases, err := service.store.ListLeases(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, lease := range leases {
		if lease.ID == original.ID && lease.Status != "revoked" {
			t.Fatalf("removed persona lease status = %q", lease.Status)
		}
	}
}

func TestAgentWritesCuratedJournalEntry(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	service := testService(t, &now, agent.Disabled{}, display.NewFake())
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, err := service.store.ActiveLease(context.Background())
	if err != nil || lease == nil {
		t.Fatalf("active lease = %+v, error = %v", lease, err)
	}
	service.mu.Lock()
	token := service.tokens[lease.ID]
	service.mu.Unlock()

	entry, err := service.WriteJournal(context.Background(), token, "I displayed a tiny orbital diagram. Future agents can continue the astronomy thread.")
	if err != nil {
		t.Fatal(err)
	}
	if entry.PersonaID != lease.PersonaID || !strings.Contains(entry.Entry, "orbital") {
		t.Fatalf("journal entry = %+v", entry)
	}
	entries, err := service.Journal(context.Background(), lease.PersonaID, 10)
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID {
		t.Fatalf("journal = %+v, error = %v", entries, err)
	}
	if _, err := service.WriteJournal(context.Background(), token, "   "); err == nil {
		t.Fatal("empty journal entry was accepted")
	}
}

func TestPersonaDetailReturnsEmptyCollectionsAsArrays(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	service := testService(t, &now, agent.Disabled{}, display.NewFake())

	detail, err := service.PersonaDetail(context.Background(), "persona")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Leases == nil || detail.Events == nil || detail.Prompts == nil || detail.Transcript == nil ||
		detail.Frames == nil || detail.Journal == nil || detail.Inference == nil || detail.Schedules == nil {
		t.Fatalf("persona detail contains a nil collection: %+v", detail)
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
		Version: 2, Timezone: "Australia/Brisbane",
		Display:   config.Display{Adapter: "fake", MaxFPS: 1, Blackout: config.TimeSpan{Start: "21:00", End: "09:00"}},
		Scheduler: config.Scheduler{DefaultLease: config.Duration(24 * time.Hour), DefaultCooldown: config.Duration(time.Hour), Selection: "weighted_random", AvoidImmediateRepeat: true},
		Inference: config.Inference{ModelProfile: "model", DefaultThinking: "low", LeaseBudget: config.Budget{MaxInputTokens: 100000, MaxOutputTokens: 10000,
			MaxModelCalls: 8, MaxActiveRuntime: config.Duration(time.Hour), MaxCostUSD: &cost, MaxModelSceneCommits: 20},
			PerCall: config.CallLimit{MaxOutputTokens: 2048}},
		ModelProfiles: map[string]config.ModelProfile{"model": {Provider: "test", Model: "test", Thinking: config.Thinking{Default: "low", Allowed: []string{"low"}}}},
		Personas:      []config.Persona{{ID: "persona", DisplayName: "Persona", Enabled: true, Weight: 1}},
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

type failingPanel struct {
	*display.Fake
	err error
}

func (p failingPanel) Status(ctx context.Context) (display.Status, error) {
	if p.err != nil {
		return display.Status{}, p.err
	}
	return p.Fake.Status(ctx)
}

// A display proxy outage must degrade only the display fields. The operator
// surface still needs lease, policy and budget state, and it must be able to
// tell "we could not ask the panel" apart from "the panel is off".
func TestStatusSurvivesDisplayProbeFailure(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	panel := failingPanel{Fake: display.NewFake(), err: errors.New("dial tcp: connection refused")}
	service := testService(t, &now, &blockingRunner{done: make(chan struct{})}, panel)

	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("status must not fail when the display probe fails: %v", err)
	}
	if status.DisplayProbeError == "" || status.DisplayProbeErrorAt == nil {
		t.Fatalf("probe failure was not reported: %+v", status)
	}
	if status.Lease == nil || status.Budget == nil {
		t.Fatalf("lease and budget were dropped by a display failure: %+v", status)
	}
	if !status.DisplayArmed {
		t.Fatalf("daylight should report the panel as armed: %+v", status)
	}
}

// The panel reports an error string without a time. The adapter timestamps the
// first sighting so the UI can age it instead of implying a live outage.
func TestFakeStatusCarriesProbeTime(t *testing.T) {
	panel := display.NewFake()
	status, err := panel.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.CheckedAt.IsZero() {
		t.Fatal("display status must record when it was observed")
	}
}
