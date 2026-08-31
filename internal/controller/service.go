package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/samcm/pixel-steward/internal/agent"
	"github.com/samcm/pixel-steward/internal/budget"
	"github.com/samcm/pixel-steward/internal/config"
	"github.com/samcm/pixel-steward/internal/display"
	"github.com/samcm/pixel-steward/internal/domain"
	"github.com/samcm/pixel-steward/internal/executor"
	"github.com/samcm/pixel-steward/internal/frame"
	"github.com/samcm/pixel-steward/internal/objectstore"
	"github.com/samcm/pixel-steward/internal/policy"
	"github.com/samcm/pixel-steward/internal/prompt"
	"github.com/samcm/pixel-steward/internal/scheduler"
	"github.com/samcm/pixel-steward/internal/store"
)

var (
	ErrUnauthorized = errors.New("invalid or expired agent credential")
	ErrBlackout     = errors.New("display is in blackout")
	ErrLeaseExpired = errors.New("lease is not active")
)

type Clock func() time.Time

type Service struct {
	config          config.Config
	store           store.Store
	objects         objectstore.Store
	display         display.Display
	runner          agent.Runner
	executor        executor.Executor
	clock           Clock
	window          policy.DailyWindow
	testWindowUntil time.Time
	selectr         scheduler.Selector

	mu           sync.Mutex
	ledgers      map[string]*budget.Ledger
	tokens       map[string]string
	running      map[string]context.CancelFunc
	liveClips    map[string]*liveClip
	reconciled   map[string]bool
	sandboxState string
	screenState  *bool
	displayProbe time.Time
}

// Status separates the independent layers an operator must distinguish:
// controller policy (Blackout, TestWindowUntil), controller intent
// (DisplayArmed), proxy reachability (DisplayProbeError) and observed device
// state (Display). A failed probe never rewrites the device fields, so an
// unknown panel is reported as unknown rather than as off.
type Status struct {
	AsOf                time.Time        `json:"as_of"`
	Blackout            bool             `json:"blackout"`
	ScheduledBlackout   bool             `json:"scheduled_blackout"`
	TestWindowUntil     *time.Time       `json:"test_window_until,omitempty"`
	NextTransition      time.Time        `json:"next_transition"`
	Lease               *domain.Lease    `json:"lease,omitempty"`
	Budget              *budget.Snapshot `json:"budget,omitempty"`
	Display             display.Status   `json:"display"`
	DisplayArmed        bool             `json:"display_armed"`
	DisplayProbeError   string           `json:"display_probe_error,omitempty"`
	DisplayProbeErrorAt *time.Time       `json:"display_probe_error_at,omitempty"`
	AgentRunning        bool             `json:"agent_running"`
	Reasoning           *ReasoningStatus `json:"reasoning,omitempty"`
}

type ReasoningStatus struct {
	Effective   string   `json:"effective"`
	Source      string   `json:"source"`
	Allowed     []string `json:"allowed"`
	CacheImpact string   `json:"cache_impact"`
}

// PersonaDetail is the operator-facing, secret-safe view of one persona. The
// configuration contains credential environment variable names, never values.
type PersonaDetail struct {
	Persona       domain.Persona            `json:"persona"`
	Configuration map[string]any            `json:"configuration"`
	Leases        []domain.Lease            `json:"leases"`
	Events        []domain.Event            `json:"events"`
	Prompts       []domain.Event            `json:"prompts"`
	Transcript    []domain.Event            `json:"transcript"`
	Frames        []domain.Frame            `json:"frames"`
	Journal       []domain.JournalEntry     `json:"journal"`
	Inference     []domain.InferenceRequest `json:"inference"`
	Schedules     []domain.Schedule         `json:"schedules"`
	Truncated     bool                      `json:"truncated"`
}

// ModelProfileDetail is the secret-safe operator view of an inference route.
// CredentialEnv names a required environment variable but never exposes its
// value.
type ModelProfileDetail struct {
	Name          string          `json:"name"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	Endpoint      string          `json:"endpoint,omitempty"`
	CredentialEnv string          `json:"credential_env,omitempty"`
	Thinking      config.Thinking `json:"thinking"`
	Billing       config.Billing  `json:"billing"`
	Selected      bool            `json:"selected"`
}

func New(cfg config.Config, database store.Store, objects objectstore.Store, panel display.Display, runner agent.Runner, sandbox executor.Executor, clock Clock) (*Service, error) {
	if clock == nil {
		clock = time.Now
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	window, err := policy.NewDailyWindow(cfg.Display.Blackout.Start, cfg.Display.Blackout.End, location)
	if err != nil {
		return nil, err
	}
	var testWindowUntil time.Time
	if cfg.Operator.TestWindowUntil != "" {
		testWindowUntil, err = time.Parse(time.RFC3339, cfg.Operator.TestWindowUntil)
		if err != nil {
			return nil, fmt.Errorf("parse operator test window: %w", err)
		}
	}
	if runner == nil {
		runner = agent.Disabled{}
	}
	if sandbox == nil {
		sandbox = executor.Disabled{}
	}
	service := &Service{
		config: cfg, store: database, objects: objects, display: panel, runner: runner, executor: sandbox, clock: clock, window: window,
		testWindowUntil: testWindowUntil,
		selectr:         scheduler.Selector{AvoidImmediateRepeat: cfg.Scheduler.AvoidImmediateRepeat},
		ledgers:         make(map[string]*budget.Ledger), tokens: make(map[string]string), running: make(map[string]context.CancelFunc),
		liveClips: make(map[string]*liveClip), reconciled: make(map[string]bool),
	}
	if err := service.syncPersonas(context.Background()); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.Tick(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			return ctx.Err()
		case <-ticker.C:
			if err := s.Tick(ctx); err != nil {
				s.event(ctx, domain.Event{At: s.clock(), Actor: "controller", Type: "controller.tick.error", Payload: jsonValue(map[string]string{"error": err.Error()})})
			}
		}
	}
}

func (s *Service) Tick(ctx context.Context) error {
	now := s.clock()
	blackout := s.inBlackout(now)
	if err := s.enforceScreen(ctx, !blackout); err != nil {
		return err
	}
	lease, err := s.store.ActiveLease(ctx)
	if err != nil {
		return err
	}
	if lease != nil {
		if _, _, personaErr := s.persona(lease.PersonaID); personaErr != nil {
			s.stop(lease.ID)
			if err := s.store.EndLease(ctx, lease.ID, "revoked"); err != nil {
				return err
			}
			_ = s.executor.Destroy(ctx, lease.ID)
			s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "lease.ended", Payload: jsonValue(map[string]string{"reason": "persona_removed"})})
			lease = nil
		}
	}
	if lease != nil && !now.Before(lease.EndsAt) {
		s.stop(lease.ID)
		if err := s.store.EndLease(ctx, lease.ID, "complete"); err != nil {
			return err
		}
		_ = s.executor.Destroy(ctx, lease.ID)
		s.mu.Lock()
		s.sandboxState = ""
		s.mu.Unlock()
		s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "lease.ended", Payload: jsonValue(map[string]string{"reason": "deadline"})})
		lease = nil
	}
	if blackout {
		sandboxID := "controller"
		if lease != nil {
			s.stop(lease.ID)
			sandboxID = lease.ID
		}
		return s.setSandbox(ctx, sandboxID, false)
	}
	if lease == nil {
		lease, err = s.createLease(ctx, now)
		if errors.Is(err, scheduler.ErrNoEligiblePersona) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	_ = s.setSandbox(ctx, lease.ID, true)
	if err := s.ensureLeaseState(ctx, lease); err != nil {
		return err
	}
	if err := s.reconcileRendererSchedules(ctx, *lease, now); err != nil {
		return err
	}
	// Display recovery must never block the agent scheduler. A proxy or panel
	// outage is recorded and retried independently while inference, local
	// rendering, and archival continue.
	if err := s.ensureDisplayActive(ctx, *lease, now); err != nil {
		s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "display.restore.error", Payload: jsonValue(map[string]string{"error": err.Error()})})
	}
	if s.isRunning(lease.ID) {
		return nil
	}

	requests, err := s.store.ListInferenceRequests(ctx, lease.ID, 1)
	if err != nil {
		return err
	}
	if len(requests) == 0 {
		events, err := s.store.ListEvents(ctx, 10000)
		if err != nil {
			return err
		}
		attempted := false
		for _, event := range events {
			if event.LeaseID == lease.ID && event.Type == "agent.wake.started" {
				attempted = true
				break
			}
		}
		if !attempted {
			return s.startWake(ctx, *lease, "initial", nil)
		}
	}
	due, err := s.store.ListSchedules(ctx, lease.ID, &now)
	if err != nil {
		return err
	}
	if len(due) == 0 {
		return nil
	}
	schedule := due[0]
	if schedule.Kind == "renderer" {
		return s.runRenderer(ctx, *lease, schedule, now)
	}
	if schedule.MissedPolicy == "skip" && schedule.NextRunAt != nil && (s.inBlackout(*schedule.NextRunAt) || now.Sub(*schedule.NextRunAt) > 30*time.Second) {
		var next *time.Time
		if schedule.Interval > 0 {
			value := *schedule.NextRunAt
			for !value.After(now) {
				value = value.Add(schedule.Interval)
			}
			if value.Before(lease.EndsAt) {
				next = &value
			}
		}
		s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "schedule.skipped", Payload: jsonValue(schedule)})
		return s.store.MarkScheduleRun(ctx, lease.ID, schedule.ID, now, next)
	}
	var next *time.Time
	if schedule.Interval > 0 {
		value := now.Add(schedule.Interval)
		next = &value
	}
	if err := s.store.MarkScheduleRun(ctx, lease.ID, schedule.ID, now, next); err != nil {
		return err
	}
	return s.startWake(ctx, *lease, "schedule:"+schedule.ID, schedule.Payload)
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	now := s.clock()
	lease, err := s.store.ActiveLease(ctx)
	if err != nil {
		return Status{}, err
	}
	// A display probe failure degrades the display fields only. The operator
	// surface must keep rendering lease, budget and policy state instead of
	// collapsing into a single error.
	panel, panelErr := s.display.Status(ctx)
	status := Status{AsOf: now, Blackout: s.inBlackout(now), ScheduledBlackout: s.window.Contains(now),
		NextTransition: s.nextTransition(now), Lease: lease, Display: panel}
	if panelErr != nil {
		status.DisplayProbeError = panelErr.Error()
		status.DisplayProbeErrorAt = &now
	}
	s.mu.Lock()
	status.DisplayArmed = s.screenState != nil && *s.screenState
	s.mu.Unlock()
	if s.testWindowActive(now) {
		until := s.testWindowUntil
		status.TestWindowUntil = &until
	}
	if lease != nil {
		if err := s.ensureLeaseState(ctx, lease); err != nil {
			return Status{}, err
		}
		s.mu.Lock()
		ledger := s.ledgers[lease.ID]
		_, status.AgentRunning = s.running[lease.ID]
		s.mu.Unlock()
		snapshot := ledger.Snapshot(now)
		status.Budget = &snapshot
		profile := s.config.ModelProfiles[lease.ModelProfile]
		status.Reasoning = &ReasoningStatus{Effective: lease.Thinking, Source: "controller_config", Allowed: profile.Thinking.Allowed, CacheImpact: profile.Thinking.CacheImpact}
	}
	return status, nil
}

func (s *Service) Budget(ctx context.Context, token string) (budget.Snapshot, domain.Lease, error) {
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return budget.Snapshot{}, domain.Lease{}, err
	}
	s.mu.Lock()
	ledger := s.ledgers[lease.ID]
	s.mu.Unlock()
	return ledger.Snapshot(s.clock()), lease, nil
}

func (s *Service) Blackout() bool { return s.inBlackout(s.clock()) }

func (s *Service) testWindowActive(at time.Time) bool {
	return !s.testWindowUntil.IsZero() && at.Before(s.testWindowUntil)
}

func (s *Service) inBlackout(at time.Time) bool {
	return s.window.Contains(at) && !s.testWindowActive(at)
}

func (s *Service) nextTransition(at time.Time) time.Time {
	next := s.window.NextTransition(at)
	if s.window.Contains(at) && s.testWindowActive(at) && s.testWindowUntil.Before(next) {
		return s.testWindowUntil
	}
	return next
}

func (s *Service) QueryHistory(ctx context.Context, token, query string) (domain.SQLResult, error) {
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return domain.SQLResult{}, err
	}
	return s.store.QueryHistory(ctx, lease.ID, query)
}

// WriteJournal records the agent's own concise account of its work. It is
// deliberately separate from runtime telemetry so future agents can recover
// narrative context without reverse-engineering raw event payloads.
func (s *Service) WriteJournal(ctx context.Context, token, value string) (domain.JournalEntry, error) {
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return domain.JournalEntry{}, err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return domain.JournalEntry{}, errors.New("journal entry must not be empty")
	}
	if utf8.RuneCountInString(value) > 1200 {
		return domain.JournalEntry{}, errors.New("journal entry must be at most 1200 characters")
	}
	event, err := s.store.AppendEvent(ctx, domain.Event{At: s.clock(), LeaseID: lease.ID, PersonaID: lease.PersonaID,
		Actor: "agent", Type: "journal.entry", Payload: jsonValue(map[string]string{"entry": value})})
	if err != nil {
		return domain.JournalEntry{}, err
	}
	return domain.JournalEntry{ID: event.ID, At: event.At, LeaseID: event.LeaseID, PersonaID: event.PersonaID, Entry: value}, nil
}

func (s *Service) Journal(ctx context.Context, personaID string, limit int) ([]domain.JournalEntry, error) {
	return s.store.ListJournalEntries(ctx, personaID, limit)
}

func (s *Service) Exec(ctx context.Context, token, command string, timeout time.Duration) (executor.Result, error) {
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return executor.Result{}, err
	}
	if s.inBlackout(s.clock()) {
		return executor.Result{}, ErrBlackout
	}
	result, err := s.executor.Exec(ctx, lease.ID, command, timeout)
	s.event(ctx, domain.Event{At: s.clock(), LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "agent", Type: "sandbox.exec", Payload: jsonValue(map[string]any{
		"command": command, "timeout_ms": timeout.Milliseconds(), "result": result, "error": errorString(err),
	})})
	return result, err
}

func (s *Service) PublishPath(ctx context.Context, token, path string, modelDriven bool) (domain.Frame, error) {
	return s.publishPath(ctx, token, path, modelDriven, true)
}

func (s *Service) publishPath(ctx context.Context, token, path string, modelDriven, commit bool) (domain.Frame, error) {
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return domain.Frame{}, err
	}
	if s.inBlackout(s.clock()) {
		return domain.Frame{}, ErrBlackout
	}
	source, contentType, err := s.executor.ReadFile(ctx, lease.ID, path)
	if err != nil {
		return domain.Frame{}, err
	}
	defer source.Close()
	return s.publish(ctx, token, contentType, source, modelDriven, commit)
}

func (s *Service) Publish(ctx context.Context, token, contentType string, source io.Reader, modelDriven bool) (domain.Frame, error) {
	return s.publish(ctx, token, contentType, source, modelDriven, true)
}

func (s *Service) publish(ctx context.Context, token, contentType string, source io.Reader, modelDriven, commit bool) (domain.Frame, error) {
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return domain.Frame{}, err
	}
	now := s.clock()
	if s.inBlackout(now) {
		return domain.Frame{}, ErrBlackout
	}
	data, err := io.ReadAll(io.LimitReader(source, frame.MaxInput+1))
	if err != nil {
		return domain.Frame{}, err
	}
	if len(data) > frame.MaxInput {
		return domain.Frame{}, fmt.Errorf("frame exceeds %d-byte input limit", frame.MaxInput)
	}
	frames, err := s.store.ListFrames(ctx, lease.ID, 1)
	if err != nil {
		return domain.Frame{}, err
	}
	processed, err := frame.Process(bytes.NewReader(data))
	if err != nil {
		s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "agent", Type: "frame.rejected", Payload: jsonValue(map[string]string{"error": err.Error()})})
		return domain.Frame{}, err
	}
	displayAsset := processed.PNG
	displayContentType := "image/png"
	digest := processed.SHA256
	if bytes.HasPrefix(data, []byte("GIF8")) && s.config.Display.Live.ClipFrames > 1 {
		displayAsset = data
		displayContentType = "image/gif"
		digest = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	if len(frames) > 0 && frames[0].SHA256 == digest && (!commit || frames[0].Published) {
		s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "frame.duplicate_skipped", Payload: jsonValue(map[string]string{"sha256": digest})})
		return frames[0], nil
	}
	sequence := int64(1)
	if len(frames) > 0 {
		sequence = frames[0].Sequence + 1
	}
	prefix := fmt.Sprintf("leases/%s/frames/%012d", lease.ID, sequence)
	sourceObject, err := s.objects.Put(ctx, prefix+".source", contentType, bytes.NewReader(data))
	if err != nil {
		return domain.Frame{}, err
	}
	if modelDriven {
		s.mu.Lock()
		ledger := s.ledgers[lease.ID]
		s.mu.Unlock()
		if err := ledger.CommitScene(); err != nil {
			return domain.Frame{}, err
		}
	}
	finalObject, err := s.objects.Put(ctx, prefix+".png", "image/png", bytes.NewReader(processed.PNG))
	if err != nil {
		return domain.Frame{}, err
	}
	record := domain.Frame{LeaseID: lease.ID, PersonaID: lease.PersonaID, Sequence: sequence, CreatedAt: now,
		SourceObject: sourceObject.Key, FinalObject: finalObject.Key, SHA256: digest, Width: processed.Width, Height: processed.Height}
	// A zero hold asks stateful display adapters to retain this frame until the
	// next explicit scene commit or blackout. Renderer previews never reach the
	// adapter.
	var publishErr error
	if commit {
		publishErr = s.display.Publish(ctx, displayAsset, displayContentType, 0)
		record.Published = publishErr == nil
		if publishErr != nil {
			record.PublishError = publishErr.Error()
		}
	}
	record, storeErr := s.store.AppendFrame(ctx, record)
	if storeErr != nil {
		return domain.Frame{}, storeErr
	}
	s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "agent", Type: "frame.submitted", Payload: jsonValue(record)})
	return record, publishErr
}

func (s *Service) CreateSchedule(ctx context.Context, token string, schedule domain.Schedule) (domain.Schedule, error) {
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return domain.Schedule{}, err
	}
	if schedule.RunAt.Before(s.clock()) || !schedule.RunAt.Before(lease.EndsAt) {
		return domain.Schedule{}, errors.New("schedule must run in the future before lease expiry")
	}
	if schedule.MissedPolicy != "skip" && schedule.MissedPolicy != "defer" {
		return domain.Schedule{}, errors.New("missed_policy must be skip or defer")
	}
	schedule.ID = newID("schedule")
	schedule.LeaseID = lease.ID
	schedule.PersonaID = lease.PersonaID
	schedule.Enabled = true
	schedule.NextRunAt = &schedule.RunAt
	if len(schedule.Payload) == 0 {
		schedule.Payload = json.RawMessage(`{}`)
	}
	if err := s.store.CreateSchedule(ctx, schedule); err != nil {
		return domain.Schedule{}, err
	}
	s.event(ctx, domain.Event{At: s.clock(), LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "agent", Type: "schedule.created", Payload: jsonValue(schedule)})
	return schedule, nil
}

func (s *Service) SetPersonaEnabled(ctx context.Context, id string, enabled bool) error {
	if err := s.store.SetPersonaEnabled(ctx, id, enabled); err != nil {
		return err
	}
	s.event(ctx, domain.Event{At: s.clock(), PersonaID: id, Actor: "operator", Type: "persona.enabled_override", Payload: jsonValue(map[string]bool{"enabled": enabled})})
	if !enabled {
		lease, err := s.store.ActiveLease(ctx)
		if err == nil && lease != nil && lease.PersonaID == id {
			s.stop(lease.ID)
			_ = s.store.EndLease(ctx, lease.ID, "revoked")
			_ = s.executor.Destroy(ctx, lease.ID)
		}
	}
	return nil
}

// Personas returns only identities present in desired configuration. Historic
// rows remain in durable storage so leases, frames, and transcripts keep their
// foreign-key history, but removed identities must not reappear as selectable
// personas in the operator UI.
func (s *Service) Personas(ctx context.Context) ([]domain.Persona, error) {
	stored, err := s.store.ListPersonas(ctx)
	if err != nil {
		return nil, err
	}
	configured := make(map[string]struct{}, len(s.config.Personas))
	for _, persona := range s.config.Personas {
		configured[persona.ID] = struct{}{}
	}
	result := make([]domain.Persona, 0, len(configured))
	for _, persona := range stored {
		if _, ok := configured[persona.ID]; ok {
			result = append(result, persona)
		}
	}
	return result, nil
}

func (s *Service) ModelProfiles() []ModelProfileDetail {
	names := make([]string, 0, len(s.config.ModelProfiles))
	for name := range s.config.ModelProfiles {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]ModelProfileDetail, 0, len(names))
	for _, name := range names {
		profile := s.config.ModelProfiles[name]
		result = append(result, ModelProfileDetail{
			Name: name, Provider: profile.Provider, Model: profile.Model, Endpoint: profile.Endpoint,
			CredentialEnv: profile.CredentialEnv, Thinking: profile.Thinking, Billing: profile.Billing,
			Selected: name == s.config.Inference.ModelProfile,
		})
	}
	return result
}

func (s *Service) PersonaDetail(ctx context.Context, id string) (PersonaDetail, error) {
	_, configured, err := s.persona(id)
	if err != nil {
		return PersonaDetail{}, err
	}
	personas, err := s.store.ListPersonas(ctx)
	if err != nil {
		return PersonaDetail{}, err
	}
	var persona domain.Persona
	found := false
	for _, candidate := range personas {
		if candidate.ID == id {
			persona, found = candidate, true
			break
		}
	}
	if !found {
		return PersonaDetail{}, errors.New("persona not found")
	}

	leases, err := s.store.ListLeases(ctx, 1000)
	if err != nil {
		return PersonaDetail{}, err
	}
	events, err := s.store.ListEvents(ctx, 1000)
	if err != nil {
		return PersonaDetail{}, err
	}
	frames, err := s.store.ListFrames(ctx, "", 1000)
	if err != nil {
		return PersonaDetail{}, err
	}
	journal, err := s.store.ListJournalEntries(ctx, id, 1000)
	if err != nil {
		return PersonaDetail{}, err
	}
	// Keep the operator API's collection contract stable. Some stores return a
	// nil slice when no journal rows exist, which encoding/json would expose as
	// null and force every UI consumer to special-case an otherwise empty list.
	if journal == nil {
		journal = make([]domain.JournalEntry, 0)
	}
	inference, err := s.store.ListInferenceRequests(ctx, "", 1000)
	if err != nil {
		return PersonaDetail{}, err
	}
	schedules, err := s.store.ListSchedules(ctx, "", nil)
	if err != nil {
		return PersonaDetail{}, err
	}

	detail := PersonaDetail{Persona: persona,
		Leases: filterLeases(leases, id), Events: filterEvents(events, id),
		Frames: filterFrames(frames, id), Journal: journal, Inference: filterInference(inference, id),
		Schedules: filterSchedules(schedules, id), Prompts: make([]domain.Event, 0), Transcript: make([]domain.Event, 0),
		Truncated: len(leases) == 1000 || len(events) == 1000 || len(frames) == 1000 || len(inference) == 1000,
	}
	for _, event := range detail.Events {
		if event.Type == "agent.prompt" {
			detail.Prompts = append(detail.Prompts, event)
		}
		if strings.HasPrefix(event.Type, "runtime.") {
			detail.Transcript = append(detail.Transcript, event)
		}
	}
	soul, soulErr := os.ReadFile(filepath.Clean(configured.Soul))
	if soulErr != nil {
		soul = []byte(configured.DisplayName)
	}
	budgetLimit := s.config.Inference.LeaseBudget
	if configured.BudgetOverride != nil {
		budgetLimit = *configured.BudgetOverride
	}
	detail.Configuration = map[string]any{
		"character_brief": string(soul),
		"runtime": map[string]any{
			"driver": s.config.Runtime.Driver, "persona_memory": s.config.Runtime.Driver == "hermes",
		},
		"persona": map[string]any{
			"id": configured.ID, "display_name": configured.DisplayName, "enabled_default": configured.Enabled,
			"enabled_effective": persona.Enabled, "weight": configured.Weight, "cooldown": persona.Cooldown.String(),
			"lease": persona.Lease.String(), "toolsets": configured.Toolsets,
		},
		"budget": map[string]any{
			"max_input_tokens": budgetLimit.MaxInputTokens, "max_output_tokens": budgetLimit.MaxOutputTokens,
			"max_model_calls": budgetLimit.MaxModelCalls, "max_active_runtime": budgetLimit.MaxActiveRuntime.Duration().String(),
			"max_cost_usd": budgetLimit.MaxCostUSD, "max_model_scene_commits": budgetLimit.MaxModelSceneCommits,
			"per_call_max_output_tokens": s.config.Inference.PerCall.MaxOutputTokens,
		},
	}
	return detail, nil
}

func filterLeases(values []domain.Lease, id string) []domain.Lease {
	result := make([]domain.Lease, 0)
	for _, value := range values {
		if value.PersonaID == id {
			result = append(result, value)
		}
	}
	return result
}

func filterEvents(values []domain.Event, id string) []domain.Event {
	result := make([]domain.Event, 0)
	for _, value := range values {
		if value.PersonaID == id {
			result = append(result, value)
		}
	}
	return result
}

func filterFrames(values []domain.Frame, id string) []domain.Frame {
	result := make([]domain.Frame, 0)
	for _, value := range values {
		if value.PersonaID == id {
			result = append(result, value)
		}
	}
	return result
}

func filterInference(values []domain.InferenceRequest, id string) []domain.InferenceRequest {
	result := make([]domain.InferenceRequest, 0)
	for _, value := range values {
		if value.PersonaID == id {
			result = append(result, value)
		}
	}
	return result
}

func filterSchedules(values []domain.Schedule, id string) []domain.Schedule {
	result := make([]domain.Schedule, 0)
	for _, value := range values {
		if value.PersonaID == id {
			result = append(result, value)
		}
	}
	return result
}

func (s *Service) SetThinking(ctx context.Context, value string) error {
	lease, err := s.store.ActiveLease(ctx)
	if err != nil {
		return err
	}
	if lease == nil {
		return ErrLeaseExpired
	}
	profile := s.config.ModelProfiles[lease.ModelProfile]
	if !slices.Contains(profile.Thinking.Allowed, value) {
		return fmt.Errorf("reasoning value %q is not allowed by model profile %q", value, lease.ModelProfile)
	}
	if value == lease.Thinking {
		return nil
	}
	s.stop(lease.ID)
	if err := s.store.SetLeaseThinking(ctx, lease.ID, value); err != nil {
		return err
	}
	s.event(ctx, domain.Event{At: s.clock(), LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "operator", Type: "lease.reasoning_override", Payload: jsonValue(map[string]string{
		"previous": lease.Thinking, "effective": value, "cache_impact": profile.Thinking.CacheImpact,
	})})
	return nil
}

func (s *Service) Revoke(ctx context.Context) error {
	lease, err := s.store.ActiveLease(ctx)
	if err != nil || lease == nil {
		return err
	}
	s.stop(lease.ID)
	if err := s.store.EndLease(ctx, lease.ID, "revoked"); err != nil {
		return err
	}
	_ = s.executor.Destroy(ctx, lease.ID)
	s.event(ctx, domain.Event{At: s.clock(), LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "operator", Type: "lease.revoked", Payload: json.RawMessage(`{}`)})
	return nil
}

func (s *Service) Store() store.Store         { return s.store }
func (s *Service) Objects() objectstore.Store { return s.objects }

func (s *Service) createLease(ctx context.Context, now time.Time) (*domain.Lease, error) {
	personas, err := s.store.ListPersonas(ctx)
	if err != nil {
		return nil, err
	}
	leases, err := s.store.ListLeases(ctx, 10000)
	if err != nil {
		return nil, err
	}
	seedBytes := make([]byte, 8)
	if _, err := rand.Read(seedBytes); err != nil {
		return nil, err
	}
	var seed int64
	for _, value := range seedBytes {
		seed = seed<<8 | int64(value)
	}
	decision, err := s.selectr.Select(now, seed, personas, leases)
	if err != nil {
		return nil, err
	}
	var persona domain.Persona
	for _, candidate := range personas {
		if candidate.ID == decision.SelectedID {
			persona = candidate
			break
		}
	}
	duration := persona.Lease
	if duration <= 0 {
		duration = s.config.Scheduler.DefaultLease.Duration()
	}
	lease := domain.Lease{ID: newID("lease"), PersonaID: persona.ID, ModelProfile: s.config.Inference.ModelProfile,
		Thinking: s.config.Inference.DefaultThinking, StartedAt: now, EndsAt: now.Add(duration), Status: "active"}
	token, hash, err := newToken()
	if err != nil {
		return nil, err
	}
	lease.AgentTokenHash = hash
	if err := s.store.CreateLease(ctx, lease); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.tokens[lease.ID] = token
	s.mu.Unlock()
	s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "lease.selected", Payload: jsonValue(decision)})
	return &lease, nil
}

func (s *Service) startWake(parent context.Context, lease domain.Lease, reason string, payload json.RawMessage) error {
	persona, cfgPersona, err := s.persona(lease.PersonaID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	ledger := s.ledgers[lease.ID]
	token := s.tokens[lease.ID]
	if _, exists := s.running[lease.ID]; exists {
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	s.running[lease.ID] = cancel
	s.mu.Unlock()

	soul, err := os.ReadFile(filepath.Clean(cfgPersona.Soul))
	if err != nil {
		soul = []byte(cfgPersona.DisplayName)
	}
	leasePrompt := prompt.Build(prompt.Context{Lease: lease, Persona: persona, Soul: string(soul), Now: s.clock(),
		Timezone: s.config.Timezone, BlackoutFrom: s.config.Display.Blackout.Start, BlackoutTo: s.config.Display.Blackout.End,
		Budget: ledger.Snapshot(s.clock()), StillOnly: s.config.Display.Live.ClipFrames == 1})
	if len(payload) > 0 && string(payload) != "{}" {
		leasePrompt += "\n\nScheduled wake context:\n" + string(payload)
	}
	profile := s.config.ModelProfiles[lease.ModelProfile]
	s.event(parent, domain.Event{At: s.clock(), LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "agent.prompt", Payload: jsonValue(map[string]string{
		"reason": reason, "prompt": leasePrompt,
	})})
	s.event(parent, domain.Event{At: s.clock(), LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "agent.wake.started", Payload: jsonValue(map[string]string{"reason": reason})})
	go func() {
		err := s.runner.Run(ctx, agent.Wake{Lease: lease, Persona: persona, Profile: profile, Prompt: leasePrompt, AgentToken: token, Budget: ledger})
		s.mu.Lock()
		delete(s.running, lease.ID)
		s.mu.Unlock()
		kind := "agent.wake.completed"
		body := map[string]string{"reason": reason}
		if err != nil && !errors.Is(err, context.Canceled) {
			kind = "agent.wake.failed"
			body["error"] = err.Error()
		}
		s.event(context.Background(), domain.Event{At: s.clock(), LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: kind, Payload: jsonValue(body)})
	}()
	return nil
}

func (s *Service) ensureLeaseState(ctx context.Context, lease *domain.Lease) error {
	s.mu.Lock()
	_, hasLedger := s.ledgers[lease.ID]
	_, hasToken := s.tokens[lease.ID]
	s.mu.Unlock()
	if !hasLedger {
		_, cfgPersona, err := s.persona(lease.PersonaID)
		if err != nil {
			return err
		}
		limits := s.config.Inference.LeaseBudget
		if cfgPersona.BudgetOverride != nil {
			limits = *cfgPersona.BudgetOverride
		}
		ledger, err := budget.New(toLimits(limits, s.config.Inference.PerCall))
		if err != nil {
			return err
		}
		requests, err := s.store.ListInferenceRequests(ctx, lease.ID, 10000)
		if err != nil {
			return err
		}
		for _, request := range requests {
			if request.Status == "running" || request.EndedAt == nil {
				continue
			}
			_ = ledger.Restore(budget.Actual{Tokens: budget.TokenUsage{Input: request.PromptTokens, Output: request.CompletionTokens,
				Reasoning: request.ReasoningTokens, CacheRead: request.CacheReadTokens, CacheWrite: request.CacheWriteTokens},
				Cost: budget.CostUsage{EstimatedMeteredMicros: request.EstimatedMeteredMicros,
					ProviderReportedMicros: request.ProviderReportedMicros, ActualBilledMicros: request.ActualBilledMicros},
				ActiveRuntime: request.EndedAt.Sub(request.StartedAt), ModelCalls: request.ModelCalls})
		}
		s.mu.Lock()
		s.ledgers[lease.ID] = ledger
		s.mu.Unlock()
	}
	if !hasToken {
		token, hash, err := newToken()
		if err != nil {
			return err
		}
		if err := s.store.SetLeaseAgentTokenHash(ctx, lease.ID, hash); err != nil {
			return err
		}
		lease.AgentTokenHash = hash
		s.mu.Lock()
		s.tokens[lease.ID] = token
		s.mu.Unlock()
	}
	return nil
}

func (s *Service) authorize(ctx context.Context, token string) (domain.Lease, error) {
	lease, err := s.store.ActiveLease(ctx)
	if err != nil {
		return domain.Lease{}, err
	}
	if lease == nil || !s.clock().Before(lease.EndsAt) {
		return domain.Lease{}, ErrLeaseExpired
	}
	digest := sha256.Sum256([]byte(token))
	want, err := hex.DecodeString(lease.AgentTokenHash)
	if err != nil || len(want) != len(digest) || subtle.ConstantTimeCompare(digest[:], want) != 1 {
		return domain.Lease{}, ErrUnauthorized
	}
	return *lease, nil
}

func (s *Service) persona(id string) (domain.Persona, config.Persona, error) {
	for _, value := range s.config.Personas {
		if value.ID == id {
			lease := value.Lease.Duration()
			if lease <= 0 {
				lease = s.config.Scheduler.DefaultLease.Duration()
			}
			cooldown := value.Cooldown.Duration()
			if cooldown <= 0 {
				cooldown = s.config.Scheduler.DefaultCooldown.Duration()
			}
			return domain.Persona{ID: value.ID, DisplayName: value.DisplayName, Enabled: value.Enabled, Weight: value.Weight,
				Cooldown: cooldown, Lease: lease, UpdatedAt: s.clock()}, value, nil
		}
	}
	return domain.Persona{}, config.Persona{}, errors.New("persona not found in configuration")
}

func (s *Service) syncPersonas(ctx context.Context) error {
	personas := make([]domain.Persona, 0, len(s.config.Personas))
	for _, value := range s.config.Personas {
		persona, _, err := s.persona(value.ID)
		if err != nil {
			return err
		}
		personas = append(personas, persona)
	}
	return s.store.SyncPersonas(ctx, personas)
}

func (s *Service) enforceScreen(ctx context.Context, on bool) error {
	s.mu.Lock()
	if s.screenState != nil && *s.screenState == on {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	// Daylight arms the display but does not wake an adapter's autonomous
	// content loop. Publishing the first steward frame turns the panel on. This
	// keeps a slow-thinking agent from causing unrelated fallback frames and
	// gives the steward exclusive ownership from its first frame until blackout.
	if on {
		s.mu.Lock()
		s.screenState = new(bool)
		*s.screenState = true
		s.mu.Unlock()
		s.event(ctx, domain.Event{At: s.clock(), Actor: "controller", Type: "display.armed", Payload: json.RawMessage(`{}`)})
		return nil
	}
	if err := s.display.SetScreen(ctx, on); err != nil {
		return err
	}
	s.mu.Lock()
	s.screenState = new(bool)
	*s.screenState = on
	s.mu.Unlock()
	s.event(ctx, domain.Event{At: s.clock(), Actor: "controller", Type: "display.screen", Payload: jsonValue(map[string]bool{"on": on})})
	return nil
}

func (s *Service) setSandbox(ctx context.Context, leaseID string, running bool) error {
	want := "suspended"
	if running {
		want = "running"
	}
	s.mu.Lock()
	if s.sandboxState == want {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	var err error
	if running {
		err = s.executor.Resume(ctx, leaseID)
	} else {
		err = s.executor.Suspend(ctx, leaseID)
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sandboxState = want
	s.mu.Unlock()
	return nil
}

func (s *Service) stop(id string) {
	s.mu.Lock()
	cancel := s.running[id]
	delete(s.running, id)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) stopAll() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.running))
	for _, cancel := range s.running {
		cancels = append(cancels, cancel)
	}
	s.running = make(map[string]context.CancelFunc)
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Service) isRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}

func (s *Service) event(ctx context.Context, event domain.Event) {
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	_, _ = s.store.AppendEvent(ctx, event)
}

func toLimits(value config.Budget, perCall config.CallLimit) budget.Limits {
	var cost *int64
	if value.MaxCostUSD != nil {
		micros := int64(*value.MaxCostUSD * 1_000_000)
		cost = &micros
	}
	return budget.Limits{InputTokens: value.MaxInputTokens, OutputTokens: value.MaxOutputTokens,
		ModelCalls: value.MaxModelCalls, ActiveRuntime: value.MaxActiveRuntime.Duration(), SceneCommits: value.MaxModelSceneCommits,
		CostMicros: cost, PerCallOutput: perCall.MaxOutputTokens}
}

func newToken() (string, string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(value)
	digest := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(digest[:]), nil
}

func newID(prefix string) string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return prefix + "_" + hex.EncodeToString(value)
}

func jsonValue(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
