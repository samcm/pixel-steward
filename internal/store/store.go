package store

import (
	"context"
	"time"

	"github.com/samcm/pixel-steward/internal/domain"
)

type Store interface {
	Close() error
	SyncPersonas(context.Context, []domain.Persona) error
	ListPersonas(context.Context) ([]domain.Persona, error)
	SetPersonaEnabled(context.Context, string, bool) error

	CreateLease(context.Context, domain.Lease) error
	ActiveLease(context.Context) (*domain.Lease, error)
	SetLeaseAgentTokenHash(context.Context, string, string) error
	SetLeaseThinking(context.Context, string, string) error
	EndLease(context.Context, string, string) error
	ListLeases(context.Context, int) ([]domain.Lease, error)

	AppendEvent(context.Context, domain.Event) (domain.Event, error)
	ListEvents(context.Context, int) ([]domain.Event, error)
	ListJournalEntries(context.Context, string, int) ([]domain.JournalEntry, error)
	AppendFrame(context.Context, domain.Frame) (domain.Frame, error)
	ListFrames(context.Context, string, int) ([]domain.Frame, error)
	UpsertInferenceRequest(context.Context, domain.InferenceRequest) error
	ListInferenceRequests(context.Context, string, int) ([]domain.InferenceRequest, error)

	CreateSchedule(context.Context, domain.Schedule) error
	ListSchedules(context.Context, string, *time.Time) ([]domain.Schedule, error)
	MarkScheduleRun(context.Context, string, string, time.Time, *time.Time) error
	DeleteSchedule(context.Context, string, string) error
	QueryHistory(context.Context, string, string) (domain.SQLResult, error)
}
