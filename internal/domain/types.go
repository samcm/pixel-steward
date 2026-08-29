package domain

import (
	"encoding/json"
	"time"
)

type Persona struct {
	ID          string        `json:"id"`
	DisplayName string        `json:"display_name"`
	Enabled     bool          `json:"enabled"`
	Weight      int           `json:"weight"`
	Cooldown    time.Duration `json:"cooldown"`
	Lease       time.Duration `json:"lease"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type Lease struct {
	ID             string     `json:"id"`
	PersonaID      string     `json:"persona_id"`
	ModelProfile   string     `json:"model_profile"`
	Thinking       string     `json:"thinking"`
	StartedAt      time.Time  `json:"started_at"`
	EndsAt         time.Time  `json:"ends_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	Status         string     `json:"status"`
	Summary        string     `json:"summary,omitempty"`
	ContentDigest  string     `json:"content_digest,omitempty"`
	AgentTokenHash string     `json:"-"`
}

type Event struct {
	ID            int64           `json:"id"`
	At            time.Time       `json:"at"`
	LeaseID       string          `json:"lease_id,omitempty"`
	PersonaID     string          `json:"persona_id,omitempty"`
	Actor         string          `json:"actor"`
	Type          string          `json:"type"`
	CorrelationID string          `json:"correlation_id"`
	Payload       json.RawMessage `json:"payload"`
}

// EventQuery selects a slice of the event log. AfterID walks forward in
// ascending id order; BeforeID pages backward in descending id order; neither
// set returns the newest events in descending id order.
type EventQuery struct {
	LeaseID   string
	PersonaID string
	Types     []string // exact type match; empty means every type
	AfterID   int64    // exclusive lower bound; ascending results
	BeforeID  int64    // exclusive upper bound; descending results
	Limit     int      // 1..1000, defaults to 100
}

// TranscriptEventTypes are the events that belong in the operator's reading of
// what the agent actually did. Per-frame renderer telemetry is deliberately
// excluded: a renderer loop emits frame.submitted every second and would push
// the model's own words out of any bounded window.
var TranscriptEventTypes = []string{
	"runtime.text", "runtime.tool_use", "runtime.error", "runtime.output",
	"runtime.step_start", "runtime.step_finish",
	"agent.prompt", "agent.wake.started", "agent.wake.completed", "agent.wake.failed",
	"lease.selected", "lease.ended", "lease.revoked",
	"sandbox.exec", "journal.entry", "schedule.created", "schedule.skipped",
	"frame.rejected", "controller.tick.error",
}

// JournalEntry is the agent-authored, human-readable account of a wake. Raw
// events remain available for diagnostics; journal entries are the durable
// narrative that future agents and operators should read first.
type JournalEntry struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	LeaseID   string    `json:"lease_id"`
	PersonaID string    `json:"persona_id"`
	Entry     string    `json:"entry"`
}

type Frame struct {
	ID           int64     `json:"id"`
	LeaseID      string    `json:"lease_id"`
	PersonaID    string    `json:"persona_id"`
	Sequence     int64     `json:"sequence"`
	CreatedAt    time.Time `json:"created_at"`
	SourceObject string    `json:"source_object"`
	FinalObject  string    `json:"final_object"`
	SHA256       string    `json:"sha256"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	Published    bool      `json:"published"`
	PublishError string    `json:"publish_error,omitempty"`
}

type InferenceRequest struct {
	ID                     string          `json:"id"`
	LeaseID                string          `json:"lease_id"`
	PersonaID              string          `json:"persona_id"`
	Provider               string          `json:"provider"`
	Model                  string          `json:"model"`
	Thinking               string          `json:"thinking"`
	ThinkingSource         string          `json:"thinking_source"`
	ProviderRequestID      string          `json:"provider_request_id,omitempty"`
	StartedAt              time.Time       `json:"started_at"`
	EndedAt                *time.Time      `json:"ended_at,omitempty"`
	Status                 string          `json:"status"`
	StopReason             string          `json:"stop_reason,omitempty"`
	ModelCalls             int64           `json:"model_calls"`
	PromptTokens           int64           `json:"prompt_tokens"`
	CompletionTokens       int64           `json:"completion_tokens"`
	ReasoningTokens        int64           `json:"reasoning_tokens"`
	CacheReadTokens        int64           `json:"cache_read_tokens"`
	CacheWriteTokens       int64           `json:"cache_write_tokens"`
	EstimatedMeteredMicros int64           `json:"estimated_metered_micros"`
	ProviderReportedMicros *int64          `json:"provider_reported_micros"`
	ActualBilledMicros     *int64          `json:"actual_billed_micros"`
	AllocatedCostMicros    *int64          `json:"allocated_cost_micros"`
	RawUsage               json.RawMessage `json:"raw_usage,omitempty"`
}

type Schedule struct {
	ID           string          `json:"id"`
	LeaseID      string          `json:"lease_id"`
	PersonaID    string          `json:"persona_id"`
	Kind         string          `json:"kind"`
	Label        string          `json:"label"`
	RunAt        time.Time       `json:"run_at"`
	Interval     time.Duration   `json:"interval"`
	MissedPolicy string          `json:"missed_policy"`
	Enabled      bool            `json:"enabled"`
	Payload      json.RawMessage `json:"payload"`
	LastRunAt    *time.Time      `json:"last_run_at,omitempty"`
	NextRunAt    *time.Time      `json:"next_run_at,omitempty"`
}

type SQLResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Limited bool     `json:"limited"`
}
