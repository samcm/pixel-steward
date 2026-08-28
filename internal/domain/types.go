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
