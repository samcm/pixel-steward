package budget

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrExhausted          = errors.New("inference budget exhausted")
	ErrUnknownReservation = errors.New("unknown inference reservation")
)

type Limits struct {
	InputTokens   int64
	OutputTokens  int64
	ModelCalls    int64
	ActiveRuntime time.Duration
	SceneCommits  int64
	CostMicros    *int64
	PerCallOutput int64
}

type Estimate struct {
	InputTokens       int64
	MaxOutputTokens   int64
	MeteredCostMicros int64
}

type TokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning,omitempty"`
	CacheRead  int64 `json:"cache_read,omitempty"`
	CacheWrite int64 `json:"cache_write,omitempty"`
	Image      int64 `json:"image,omitempty"`
	Audio      int64 `json:"audio,omitempty"`
}

type CostUsage struct {
	EstimatedMeteredMicros      int64  `json:"estimated_metered_micros"`
	ProviderReportedMicros      *int64 `json:"provider_reported_micros"`
	ActualBilledMicros          *int64 `json:"actual_billed_micros"`
	AllocatedSubscriptionMicros *int64 `json:"allocated_subscription_micros"`
}

type Actual struct {
	Tokens        TokenUsage
	Cost          CostUsage
	ActiveRuntime time.Duration
	ModelCalls    int64
}

type Reservation struct {
	ID                uint64    `json:"id"`
	InputTokens       int64     `json:"input_tokens"`
	MaxOutputTokens   int64     `json:"max_output_tokens"`
	MeteredCostMicros int64     `json:"metered_cost_micros"`
	StartedAt         time.Time `json:"started_at"`
}

type Amount struct {
	Used      int64 `json:"used"`
	Reserved  int64 `json:"reserved"`
	Limit     int64 `json:"limit"`
	Remaining int64 `json:"remaining"`
}

type DurationAmount struct {
	UsedSeconds      int64 `json:"used_seconds"`
	ReservedSeconds  int64 `json:"reserved_seconds"`
	LimitSeconds     int64 `json:"limit_seconds"`
	RemainingSeconds int64 `json:"remaining_seconds"`
}

type CostSnapshot struct {
	UsedMicros      int64  `json:"used_micros"`
	ReservedMicros  int64  `json:"reserved_micros"`
	LimitMicros     *int64 `json:"limit_micros"`
	RemainingMicros *int64 `json:"remaining_micros"`
}

type Snapshot struct {
	AsOf               time.Time      `json:"as_of"`
	Status             string         `json:"status"`
	InputTokens        Amount         `json:"input_tokens"`
	OutputTokens       Amount         `json:"output_tokens"`
	Calls              Amount         `json:"calls"`
	ActiveRuntime      DurationAmount `json:"active_runtime"`
	SceneCommits       Amount         `json:"scene_commits"`
	Cost               CostSnapshot   `json:"estimated_metered_cost"`
	Observed           TokenUsage     `json:"observed_tokens"`
	PerCallOutputLimit int64          `json:"per_call_output_limit"`
}

type Ledger struct {
	mu sync.Mutex

	limits       Limits
	nextID       uint64
	reservations map[uint64]Reservation
	usedTokens   TokenUsage
	usedCalls    int64
	usedRuntime  time.Duration
	usedScenes   int64
	usedCost     int64
}

func New(limits Limits) (*Ledger, error) {
	if limits.InputTokens <= 0 || limits.OutputTokens <= 0 || limits.ModelCalls <= 0 || limits.ActiveRuntime <= 0 || limits.SceneCommits <= 0 || limits.PerCallOutput <= 0 {
		return nil, fmt.Errorf("all non-cost limits must be greater than zero")
	}
	if limits.CostMicros != nil && *limits.CostMicros <= 0 {
		return nil, fmt.Errorf("cost limit must be greater than zero when set")
	}

	return &Ledger{limits: limits, reservations: make(map[uint64]Reservation)}, nil
}

func (l *Ledger) Reserve(now time.Time, estimate Estimate) (Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if estimate.InputTokens < 0 || estimate.MaxOutputTokens <= 0 || estimate.MeteredCostMicros < 0 {
		return Reservation{}, fmt.Errorf("invalid inference estimate")
	}
	if estimate.MaxOutputTokens > l.limits.PerCallOutput {
		return Reservation{}, fmt.Errorf("maximum output %d exceeds per-call limit %d", estimate.MaxOutputTokens, l.limits.PerCallOutput)
	}

	reservedInput, reservedOutput, reservedCalls, reservedCost := l.reservedLocked()
	if l.usedTokens.Input+reservedInput+estimate.InputTokens > l.limits.InputTokens ||
		l.usedTokens.Output+reservedOutput+estimate.MaxOutputTokens > l.limits.OutputTokens ||
		l.usedCalls+reservedCalls+1 > l.limits.ModelCalls {
		return Reservation{}, ErrExhausted
	}
	if l.limits.CostMicros != nil && l.usedCost+reservedCost+estimate.MeteredCostMicros > *l.limits.CostMicros {
		return Reservation{}, ErrExhausted
	}
	if l.usedRuntime >= l.limits.ActiveRuntime {
		return Reservation{}, ErrExhausted
	}

	l.nextID++
	reservation := Reservation{
		ID:                l.nextID,
		InputTokens:       estimate.InputTokens,
		MaxOutputTokens:   estimate.MaxOutputTokens,
		MeteredCostMicros: estimate.MeteredCostMicros,
		StartedAt:         now,
	}
	l.reservations[reservation.ID] = reservation

	return reservation, nil
}

func (l *Ledger) Complete(id uint64, actual Actual, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	reservation, ok := l.reservations[id]
	if !ok {
		return ErrUnknownReservation
	}
	delete(l.reservations, id)

	if actual.Tokens.Input < 0 || actual.Tokens.Output < 0 || actual.ActiveRuntime < 0 || actual.Cost.EstimatedMeteredMicros < 0 {
		return fmt.Errorf("actual usage cannot be negative")
	}

	runtime := actual.ActiveRuntime
	if runtime == 0 && !now.Before(reservation.StartedAt) {
		runtime = now.Sub(reservation.StartedAt)
	}

	l.usedTokens.Input += actual.Tokens.Input
	l.usedTokens.Output += actual.Tokens.Output
	l.usedTokens.Reasoning += actual.Tokens.Reasoning
	l.usedTokens.CacheRead += actual.Tokens.CacheRead
	l.usedTokens.CacheWrite += actual.Tokens.CacheWrite
	l.usedTokens.Image += actual.Tokens.Image
	l.usedTokens.Audio += actual.Tokens.Audio
	modelCalls := actual.ModelCalls
	if modelCalls <= 0 {
		modelCalls = 1
	}
	l.usedCalls += modelCalls
	l.usedRuntime += runtime
	l.usedCost += actual.Cost.EstimatedMeteredMicros

	return nil
}

func (l *Ledger) Cancel(id uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.reservations[id]; !ok {
		return ErrUnknownReservation
	}
	delete(l.reservations, id)

	return nil
}

// Restore reapplies already-persisted usage after a controller restart. It does
// not represent a new provider call and therefore performs no reservation.
func (l *Ledger) Restore(actual Actual) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if actual.Tokens.Input < 0 || actual.Tokens.Output < 0 || actual.ActiveRuntime < 0 || actual.Cost.EstimatedMeteredMicros < 0 {
		return fmt.Errorf("actual usage cannot be negative")
	}
	l.usedTokens.Input += actual.Tokens.Input
	l.usedTokens.Output += actual.Tokens.Output
	l.usedTokens.Reasoning += actual.Tokens.Reasoning
	l.usedTokens.CacheRead += actual.Tokens.CacheRead
	l.usedTokens.CacheWrite += actual.Tokens.CacheWrite
	l.usedTokens.Image += actual.Tokens.Image
	l.usedTokens.Audio += actual.Tokens.Audio
	modelCalls := actual.ModelCalls
	if modelCalls <= 0 {
		modelCalls = 1
	}
	l.usedCalls += modelCalls
	l.usedRuntime += actual.ActiveRuntime
	l.usedCost += actual.Cost.EstimatedMeteredMicros
	return nil
}

func (l *Ledger) CommitScene() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.usedScenes+1 > l.limits.SceneCommits {
		return ErrExhausted
	}
	l.usedScenes++

	return nil
}

func (l *Ledger) Snapshot(now time.Time) Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	reservedInput, reservedOutput, reservedCalls, reservedCost := l.reservedLocked()
	reservedRuntime := time.Duration(0)
	for _, reservation := range l.reservations {
		if now.After(reservation.StartedAt) && now.Sub(reservation.StartedAt) > reservedRuntime {
			reservedRuntime = now.Sub(reservation.StartedAt)
		}
	}

	status := "available"
	if remaining(l.limits.InputTokens, l.usedTokens.Input+reservedInput) == 0 ||
		remaining(l.limits.OutputTokens, l.usedTokens.Output+reservedOutput) == 0 ||
		remaining(l.limits.ModelCalls, l.usedCalls+reservedCalls) == 0 ||
		remainingDuration(l.limits.ActiveRuntime, l.usedRuntime+reservedRuntime) == 0 {
		status = "exhausted"
	}

	cost := CostSnapshot{UsedMicros: l.usedCost, ReservedMicros: reservedCost, LimitMicros: l.limits.CostMicros}
	if l.limits.CostMicros != nil {
		value := remaining(*l.limits.CostMicros, l.usedCost+reservedCost)
		cost.RemainingMicros = &value
		if value == 0 {
			status = "exhausted"
		}
	}

	return Snapshot{
		AsOf:               now,
		Status:             status,
		InputTokens:        amount(l.usedTokens.Input, reservedInput, l.limits.InputTokens),
		OutputTokens:       amount(l.usedTokens.Output, reservedOutput, l.limits.OutputTokens),
		Calls:              amount(l.usedCalls, reservedCalls, l.limits.ModelCalls),
		ActiveRuntime:      durationAmount(l.usedRuntime, reservedRuntime, l.limits.ActiveRuntime),
		SceneCommits:       amount(l.usedScenes, 0, l.limits.SceneCommits),
		Cost:               cost,
		Observed:           l.usedTokens,
		PerCallOutputLimit: l.limits.PerCallOutput,
	}
}

func (l *Ledger) reservedLocked() (input, output, calls, cost int64) {
	for _, reservation := range l.reservations {
		input += reservation.InputTokens
		output += reservation.MaxOutputTokens
		calls++
		cost += reservation.MeteredCostMicros
	}

	return input, output, calls, cost
}

func amount(used, reserved, limit int64) Amount {
	return Amount{Used: used, Reserved: reserved, Limit: limit, Remaining: remaining(limit, used+reserved)}
}

func durationAmount(used, reserved, limit time.Duration) DurationAmount {
	return DurationAmount{
		UsedSeconds:      int64(used.Seconds()),
		ReservedSeconds:  int64(reserved.Seconds()),
		LimitSeconds:     int64(limit.Seconds()),
		RemainingSeconds: int64(remainingDuration(limit, used+reserved).Seconds()),
	}
}

func remaining(limit, spent int64) int64 {
	if spent >= limit {
		return 0
	}

	return limit - spent
}

func remainingDuration(limit, spent time.Duration) time.Duration {
	if spent >= limit {
		return 0
	}

	return limit - spent
}
