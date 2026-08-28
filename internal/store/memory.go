package store

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/samcm/pixel-steward/internal/domain"
)

type Memory struct {
	mu sync.RWMutex

	personas  map[string]domain.Persona
	leases    []domain.Lease
	events    []domain.Event
	frames    []domain.Frame
	inference map[string]domain.InferenceRequest
	schedules map[string]domain.Schedule
}

func NewMemory() *Memory {
	return &Memory{
		personas: make(map[string]domain.Persona), inference: make(map[string]domain.InferenceRequest),
		schedules: make(map[string]domain.Schedule),
	}
}

func (m *Memory) Close() error { return nil }

func (m *Memory) SyncPersonas(_ context.Context, personas []domain.Persona) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, persona := range personas {
		if previous, ok := m.personas[persona.ID]; ok {
			// Runtime enable/disable state wins over a config refresh until the
			// corresponding override is explicitly removed.
			persona.Enabled = previous.Enabled
		}
		m.personas[persona.ID] = persona
	}

	return nil
}

func (m *Memory) ListPersonas(_ context.Context) ([]domain.Persona, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	personas := make([]domain.Persona, 0, len(m.personas))
	for _, persona := range m.personas {
		personas = append(personas, persona)
	}
	slices.SortFunc(personas, func(a, b domain.Persona) int { return compare(a.ID, b.ID) })

	return personas, nil
}

func (m *Memory) SetPersonaEnabled(_ context.Context, id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	persona, ok := m.personas[id]
	if !ok {
		return errors.New("persona not found")
	}
	persona.Enabled = enabled
	persona.UpdatedAt = time.Now()
	m.personas[id] = persona

	return nil
}

func (m *Memory) CreateLease(_ context.Context, lease domain.Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.leases {
		if existing.Status == "active" {
			return errors.New("an active lease already exists")
		}
	}
	m.leases = append(m.leases, lease)

	return nil
}

func (m *Memory) ActiveLease(_ context.Context) (*domain.Lease, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := len(m.leases) - 1; i >= 0; i-- {
		if m.leases[i].Status == "active" {
			lease := m.leases[i]
			return &lease, nil
		}
	}

	return nil, nil
}

func (m *Memory) SetLeaseAgentTokenHash(_ context.Context, id, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.leases {
		if m.leases[index].ID == id && m.leases[index].Status == "active" {
			m.leases[index].AgentTokenHash = hash
			return nil
		}
	}
	return errors.New("active lease not found")
}

func (m *Memory) SetLeaseThinking(_ context.Context, id, thinking string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.leases {
		if m.leases[index].ID == id && m.leases[index].Status == "active" {
			m.leases[index].Thinking = thinking
			return nil
		}
	}
	return errors.New("active lease not found")
}

func (m *Memory) EndLease(_ context.Context, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.leases {
		if m.leases[i].ID == id {
			now := time.Now()
			m.leases[i].Status = status
			m.leases[i].EndedAt = &now
			return nil
		}
	}

	return errors.New("lease not found")
}

func (m *Memory) ListLeases(_ context.Context, limit int) ([]domain.Lease, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return tailReverse(m.leases, limit), nil
}

func (m *Memory) AppendEvent(_ context.Context, event domain.Event) (domain.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event.ID = int64(len(m.events) + 1)
	m.events = append(m.events, event)

	return event, nil
}

func (m *Memory) ListEvents(_ context.Context, limit int) ([]domain.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return tailReverse(m.events, limit), nil
}

func (m *Memory) AppendFrame(_ context.Context, frame domain.Frame) (domain.Frame, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	frame.ID = int64(len(m.frames) + 1)
	m.frames = append(m.frames, frame)

	return frame, nil
}

func (m *Memory) ListFrames(_ context.Context, leaseID string, limit int) ([]domain.Frame, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filtered := make([]domain.Frame, 0, len(m.frames))
	for _, frame := range m.frames {
		if leaseID == "" || frame.LeaseID == leaseID {
			filtered = append(filtered, frame)
		}
	}

	return tailReverse(filtered, limit), nil
}

func (m *Memory) UpsertInferenceRequest(_ context.Context, request domain.InferenceRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inference[request.ID] = request

	return nil
}

func (m *Memory) ListInferenceRequests(_ context.Context, leaseID string, limit int) ([]domain.InferenceRequest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	requests := make([]domain.InferenceRequest, 0, len(m.inference))
	for _, request := range m.inference {
		if leaseID == "" || request.LeaseID == leaseID {
			requests = append(requests, request)
		}
	}
	slices.SortFunc(requests, func(a, b domain.InferenceRequest) int { return b.StartedAt.Compare(a.StartedAt) })
	if limit > 0 && len(requests) > limit {
		requests = requests[:limit]
	}

	return requests, nil
}

func (m *Memory) CreateSchedule(_ context.Context, schedule domain.Schedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.schedules[schedule.ID]; exists {
		return errors.New("schedule already exists")
	}
	m.schedules[schedule.ID] = schedule
	return nil
}

func (m *Memory) ListSchedules(_ context.Context, leaseID string, dueAt *time.Time) ([]domain.Schedule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.Schedule, 0, len(m.schedules))
	for _, schedule := range m.schedules {
		if leaseID != "" && schedule.LeaseID != leaseID {
			continue
		}
		if dueAt != nil && (!schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(*dueAt)) {
			continue
		}
		result = append(result, schedule)
	}
	slices.SortFunc(result, func(a, b domain.Schedule) int { return a.RunAt.Compare(b.RunAt) })
	return result, nil
}

func (m *Memory) DeleteSchedule(_ context.Context, leaseID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	schedule, ok := m.schedules[id]
	if !ok || schedule.LeaseID != leaseID {
		return errors.New("schedule not found")
	}
	delete(m.schedules, id)
	return nil
}

func (m *Memory) MarkScheduleRun(_ context.Context, leaseID, id string, ranAt time.Time, next *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	schedule, ok := m.schedules[id]
	if !ok || schedule.LeaseID != leaseID {
		return errors.New("schedule not found")
	}
	schedule.LastRunAt = &ranAt
	schedule.NextRunAt = next
	schedule.Enabled = next != nil
	m.schedules[id] = schedule
	return nil
}

func (m *Memory) QueryHistory(_ context.Context, _ string, _ string) (domain.SQLResult, error) {
	return domain.SQLResult{}, errors.New("raw history SQL requires the postgres store")
}

func tailReverse[T any](values []T, limit int) []T {
	if limit <= 0 || limit > len(values) {
		limit = len(values)
	}
	result := make([]T, 0, limit)
	for i := len(values) - 1; i >= len(values)-limit; i-- {
		result = append(result, values[i])
	}

	return result
}

func compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
