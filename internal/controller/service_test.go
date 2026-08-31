package controller

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samcm/pixel-steward/internal/agent"
	"github.com/samcm/pixel-steward/internal/config"
	"github.com/samcm/pixel-steward/internal/display"
	"github.com/samcm/pixel-steward/internal/executor"
	stewardframe "github.com/samcm/pixel-steward/internal/frame"
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

type changingFramebuffer struct {
	executor.Disabled
	mu    sync.Mutex
	value byte
}

func (e *changingFramebuffer) ReadFile(context.Context, string, string) (io.ReadCloser, string, error) {
	e.mu.Lock()
	e.value += 71
	value := e.value
	e.mu.Unlock()
	raw := bytes.Repeat([]byte{value, value / 2, 255 - value}, stewardframe.Width*stewardframe.Height)
	return io.NopCloser(bytes.NewReader(raw)), "application/octet-stream", nil
}

type restoringPanel struct {
	mu        sync.Mutex
	status    display.Status
	publishes [][]byte
}

func newRestoringPanel() *restoringPanel {
	return &restoringPanel{status: display.Status{Online: true, ScreenOn: true}}
}

func (p *restoringPanel) Publish(_ context.Context, asset []byte, _ string, _ time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishes = append(p.publishes, bytes.Clone(asset))
	p.status.ScreenOn = true
	p.status.Frames++
	at := time.Now().UTC()
	p.status.LastFrameAt = &at
	return nil
}

func (p *restoringPanel) SetScreen(_ context.Context, on bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.ScreenOn = on
	return nil
}

func (p *restoringPanel) Status(context.Context) (display.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := p.status
	result.CheckedAt = time.Now().UTC()
	return result, nil
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

func TestRendererPreviewDoesNotCommitPhysicalDisplay(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	panel := display.NewFake()
	service := testService(t, &now, agent.Disabled{}, panel)
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
	raw := make([]byte, stewardframe.RGBByteSize)

	preview, err := service.publish(context.Background(), token, "application/octet-stream", bytes.NewReader(raw), false, false)
	if err != nil {
		t.Fatal(err)
	}
	status, err := panel.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Published || status.Frames != 0 {
		t.Fatalf("preview touched physical display: frame=%+v status=%+v", preview, status)
	}

	committed, err := service.publish(context.Background(), token, "application/octet-stream", bytes.NewReader(raw), false, true)
	if err != nil {
		t.Fatal(err)
	}
	status, err = panel.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !committed.Published || status.Frames != 1 {
		t.Fatalf("explicit commit did not reach physical display: frame=%+v status=%+v", committed, status)
	}
}

func TestExplicitGIFCommitPreservesAnimationAsset(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	panel := display.NewFake()
	service := testService(t, &now, agent.Disabled{}, panel)
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

	palette := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	second.Pix[0] = 1
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	raw := bytes.Clone(animated.Bytes())

	committed, err := service.publish(context.Background(), token, "image/gif", bytes.NewReader(raw), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !committed.Published || !bytes.Equal(panel.LastPNG(), raw) {
		t.Fatal("explicit GIF commit was flattened before reaching the display")
	}
}

func TestExplicitGIFCommitIsFlattenedInStillOnlyMode(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	panel := display.NewFake()
	service := testService(t, &now, agent.Disabled{}, panel)
	service.config.Display.Live.ClipFrames = 1
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

	colors := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), colors)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), colors)
	second.Pix[0] = 1
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.publish(context.Background(), token, "image/gif", bytes.NewReader(animated.Bytes()), false, true); err != nil {
		t.Fatal(err)
	}
	if asset := panel.LastPNG(); !bytes.HasPrefix(asset, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("animated commit was not flattened: %x", asset[:min(8, len(asset))])
	}
}

func TestRendererCommitsCompleteResidentClipInsteadOfIndividualFrames(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	panel := display.NewFake()
	service := testService(t, &now, agent.Disabled{}, panel)
	service.executor = &changingFramebuffer{}
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, err := service.store.ActiveLease(context.Background())
	if err != nil || lease == nil {
		t.Fatalf("active lease = %+v, error = %v", lease, err)
	}
	deadline := time.Now().Add(time.Second)
	for service.isRunning(lease.ID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if service.isRunning(lease.ID) {
		t.Fatal("initial disabled agent wake did not finish")
	}
	service.mu.Lock()
	token := service.tokens[lease.ID]
	service.mu.Unlock()
	if _, err := service.WatchRenderer(context.Background(), token, RendererOptions{Path: "frame.png", FPS: 1,
		ClipFrames: 3, FrameDelay: time.Second, RefreshInterval: 5 * time.Minute}); err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 2; index++ {
		now = now.Add(time.Second)
		if err := service.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		status, _ := panel.Status(context.Background())
		if status.Frames != 0 {
			t.Fatalf("renderer pushed an incomplete clip after %d samples: %+v", index+1, status)
		}
	}
	now = now.Add(time.Second)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, _ := panel.Status(context.Background())
	if status.Frames != 1 {
		t.Fatalf("complete clip pushes = %d, want 1", status.Frames)
	}
	resident, err := gif.DecodeAll(bytes.NewReader(panel.LastPNG()))
	if err != nil {
		t.Fatal(err)
	}
	if len(resident.Image) != 3 || len(resident.Delay) != 3 || resident.Delay[0] != 100 {
		t.Fatalf("resident GIF = %d frames delays=%v", len(resident.Image), resident.Delay)
	}

	now = now.Add(time.Second)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, _ = panel.Status(context.Background())
	if status.Frames != 1 {
		t.Fatalf("clip refreshed before the policy interval: %+v", status)
	}
}

func TestRendererCommitsPNGWhenConfiguredForOneFrame(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	panel := display.NewFake()
	service := testService(t, &now, agent.Disabled{}, panel)
	service.config.Display.Live.ClipFrames = 1
	service.executor = &changingFramebuffer{}
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, err := service.store.ActiveLease(context.Background())
	if err != nil || lease == nil {
		t.Fatalf("active lease = %+v, error = %v", lease, err)
	}
	deadline := time.Now().Add(time.Second)
	for service.isRunning(lease.ID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	service.mu.Lock()
	token := service.tokens[lease.ID]
	service.mu.Unlock()
	if _, err := service.WatchRenderer(context.Background(), token, RendererOptions{Path: "frame.png", FPS: 1,
		ClipFrames: 1, FrameDelay: time.Second, RefreshInterval: 5 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, _ := panel.Status(context.Background())
	if status.Frames != 1 {
		t.Fatalf("still commits = %d, want 1", status.Frames)
	}
	if asset := panel.LastPNG(); !bytes.HasPrefix(asset, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("physical asset is not a PNG: %x", asset[:min(8, len(asset))])
	}
}

func TestLiveRendererSupersedesPreviousWatch(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	service := testService(t, &now, agent.Disabled{}, display.NewFake())
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, _ := service.store.ActiveLease(context.Background())
	service.mu.Lock()
	token := service.tokens[lease.ID]
	service.mu.Unlock()
	first, err := service.WatchRenderer(context.Background(), token, RendererOptions{Path: "first.png", FPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.WatchRenderer(context.Background(), token, RendererOptions{Path: "second.png", FPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	schedules, err := service.store.ListSchedules(context.Background(), lease.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, schedule := range schedules {
		if schedule.ID == first.ID && schedule.Enabled {
			t.Fatal("superseded renderer remains enabled")
		}
	}
	if !second.Enabled {
		t.Fatal("replacement renderer is not enabled")
	}
	if _, err := service.WatchRenderer(context.Background(), token, RendererOptions{Path: "/workspace/frame.png", FPS: 1}); err == nil {
		t.Fatal("absolute renderer path was accepted")
	}
	if _, err := service.WatchRenderer(context.Background(), token, RendererOptions{Path: "frame.png", FPS: 1, ClipFrames: 9}); err == nil {
		t.Fatal("unsafe renderer clip size was accepted")
	}
}

func TestDarkPanelRestoresLastDurableAnimation(t *testing.T) {
	location, _ := time.LoadLocation("Australia/Brisbane")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, location)
	panel := newRestoringPanel()
	service := testService(t, &now, agent.Disabled{}, panel)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, _ := service.store.ActiveLease(context.Background())
	service.mu.Lock()
	token := service.tokens[lease.ID]
	service.mu.Unlock()

	colors := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), colors)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), colors)
	second.Pix[0] = 1
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	asset := bytes.Clone(animated.Bytes())
	if _, err := service.publish(context.Background(), token, "image/gif", bytes.NewReader(asset), false, true); err != nil {
		t.Fatal(err)
	}
	if err := panel.SetScreen(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Second)
	if err := service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if len(panel.publishes) != 2 || !bytes.Equal(panel.publishes[1], asset) || !panel.status.ScreenOn {
		t.Fatalf("restored publishes=%d screen_on=%v", len(panel.publishes), panel.status.ScreenOn)
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
		Display: config.Display{Adapter: "fake", MaxFPS: 1,
			Live: config.Live{ClipFrames: 8, FrameDelay: config.Duration(2 * time.Second), RefreshInterval: config.Duration(30 * time.Minute),
				MinimumRefresh: config.Duration(5 * time.Minute), RestorePollInterval: config.Duration(10 * time.Second)},
			Blackout: config.TimeSpan{Start: "21:00", End: "09:00"}},
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
