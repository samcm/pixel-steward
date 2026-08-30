package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samcm/pixel-steward/internal/domain"
)

const postgresSchema = `
CREATE TABLE IF NOT EXISTS personas (
  id text PRIMARY KEY,
  display_name text NOT NULL,
  enabled_default boolean NOT NULL,
  enabled_override boolean,
  weight integer NOT NULL,
  cooldown_ns bigint NOT NULL,
  lease_ns bigint NOT NULL,
  model_profile text NOT NULL,
  thinking text NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS leases (
  id text PRIMARY KEY,
  persona_id text NOT NULL REFERENCES personas(id),
  model_profile text NOT NULL,
  thinking text NOT NULL,
  started_at timestamptz NOT NULL,
  ends_at timestamptz NOT NULL,
  ended_at timestamptz,
  status text NOT NULL,
  summary text NOT NULL DEFAULT '',
  content_digest text NOT NULL DEFAULT '',
  agent_token_hash text NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS leases_one_active ON leases ((status)) WHERE status = 'active';
CREATE TABLE IF NOT EXISTS events (
  id bigserial PRIMARY KEY,
  at timestamptz NOT NULL,
  lease_id text NOT NULL DEFAULT '',
  persona_id text NOT NULL DEFAULT '',
  actor text NOT NULL,
  type text NOT NULL,
  correlation_id text NOT NULL DEFAULT '',
  payload jsonb NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS frames (
  id bigserial PRIMARY KEY,
  lease_id text NOT NULL REFERENCES leases(id),
  persona_id text NOT NULL REFERENCES personas(id),
  sequence bigint NOT NULL,
  created_at timestamptz NOT NULL,
  source_object text NOT NULL,
  final_object text NOT NULL,
  sha256 text NOT NULL,
  width integer NOT NULL,
  height integer NOT NULL,
  published boolean NOT NULL,
  publish_error text NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS inference_requests (
  id text PRIMARY KEY,
  lease_id text NOT NULL REFERENCES leases(id),
  persona_id text NOT NULL REFERENCES personas(id),
  provider text NOT NULL,
  model text NOT NULL,
  thinking text NOT NULL,
  thinking_source text NOT NULL,
  provider_request_id text NOT NULL DEFAULT '',
  started_at timestamptz NOT NULL,
  ended_at timestamptz,
  status text NOT NULL,
  stop_reason text NOT NULL DEFAULT '',
  model_calls bigint NOT NULL DEFAULT 1,
  prompt_tokens bigint NOT NULL DEFAULT 0,
  completion_tokens bigint NOT NULL DEFAULT 0,
  reasoning_tokens bigint NOT NULL DEFAULT 0,
  cache_read_tokens bigint NOT NULL DEFAULT 0,
  cache_write_tokens bigint NOT NULL DEFAULT 0,
  estimated_metered_micros bigint NOT NULL DEFAULT 0,
  provider_reported_micros bigint,
  actual_billed_micros bigint,
  allocated_cost_micros bigint,
  raw_usage jsonb
);
ALTER TABLE inference_requests ADD COLUMN IF NOT EXISTS model_calls bigint NOT NULL DEFAULT 1;
CREATE TABLE IF NOT EXISTS schedules (
  id text PRIMARY KEY,
  lease_id text NOT NULL REFERENCES leases(id) ON DELETE CASCADE,
  persona_id text NOT NULL REFERENCES personas(id),
  kind text NOT NULL,
  label text NOT NULL,
  run_at timestamptz NOT NULL,
  interval_ns bigint NOT NULL DEFAULT 0,
  missed_policy text NOT NULL,
  enabled boolean NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  last_run_at timestamptz,
  next_run_at timestamptz
);
CREATE OR REPLACE VIEW history_leases AS
  SELECT id, persona_id, model_profile, thinking, started_at, ends_at, ended_at, status, summary, content_digest FROM leases;
CREATE OR REPLACE VIEW history_events AS SELECT * FROM events;
CREATE OR REPLACE VIEW history_journal AS
  SELECT id, at, lease_id, persona_id, payload->>'entry' AS entry
  FROM events WHERE type = 'journal.entry';
CREATE OR REPLACE VIEW history_frames AS SELECT * FROM frames;
CREATE OR REPLACE VIEW history_inference AS SELECT * FROM inference_requests;
`

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, postgresSchema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply postgres schema: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() error {
	p.pool.Close()
	return nil
}

func (p *Postgres) SyncPersonas(ctx context.Context, personas []domain.Persona) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	configuredIDs := make([]string, 0, len(personas))
	for _, persona := range personas {
		configuredIDs = append(configuredIDs, persona.ID)
	}
	if _, err = tx.Exec(ctx, `UPDATE personas SET enabled_default=false, enabled_override=NULL, weight=0, updated_at=now()
      WHERE NOT (id = ANY($1::text[]))`, configuredIDs); err != nil {
		return err
	}
	for _, persona := range personas {
		_, err = tx.Exec(ctx, `INSERT INTO personas
      (id, display_name, enabled_default, weight, cooldown_ns, lease_ns, model_profile, thinking, updated_at)
      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
      ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name,
      enabled_default=EXCLUDED.enabled_default, weight=EXCLUDED.weight,
      cooldown_ns=EXCLUDED.cooldown_ns, lease_ns=EXCLUDED.lease_ns,
      model_profile=EXCLUDED.model_profile, thinking=EXCLUDED.thinking, updated_at=EXCLUDED.updated_at`,
			persona.ID, persona.DisplayName, persona.Enabled, persona.Weight, int64(persona.Cooldown), int64(persona.Lease),
			"", "", persona.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListPersonas(ctx context.Context) ([]domain.Persona, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, display_name, COALESCE(enabled_override, enabled_default), weight,
    cooldown_ns, lease_ns, updated_at FROM personas ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Persona
	for rows.Next() {
		var persona domain.Persona
		var cooldown, lease int64
		if err := rows.Scan(&persona.ID, &persona.DisplayName, &persona.Enabled, &persona.Weight, &cooldown, &lease,
			&persona.UpdatedAt); err != nil {
			return nil, err
		}
		persona.Cooldown, persona.Lease = time.Duration(cooldown), time.Duration(lease)
		result = append(result, persona)
	}
	return result, rows.Err()
}

func (p *Postgres) SetPersonaEnabled(ctx context.Context, id string, enabled bool) error {
	tag, err := p.pool.Exec(ctx, `UPDATE personas SET enabled_override=$2, updated_at=now() WHERE id=$1`, id, enabled)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("persona not found")
	}
	return err
}

func (p *Postgres) CreateLease(ctx context.Context, lease domain.Lease) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO leases
    (id,persona_id,model_profile,thinking,started_at,ends_at,ended_at,status,summary,content_digest,agent_token_hash)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, lease.ID, lease.PersonaID, lease.ModelProfile,
		lease.Thinking, lease.StartedAt, lease.EndsAt, lease.EndedAt, lease.Status, lease.Summary, lease.ContentDigest, lease.AgentTokenHash)
	return err
}

const leaseColumns = `id,persona_id,model_profile,thinking,started_at,ends_at,ended_at,status,summary,content_digest,agent_token_hash`

func scanLease(row pgx.Row) (domain.Lease, error) {
	var lease domain.Lease
	err := row.Scan(&lease.ID, &lease.PersonaID, &lease.ModelProfile, &lease.Thinking, &lease.StartedAt, &lease.EndsAt,
		&lease.EndedAt, &lease.Status, &lease.Summary, &lease.ContentDigest, &lease.AgentTokenHash)
	return lease, err
}

func (p *Postgres) ActiveLease(ctx context.Context) (*domain.Lease, error) {
	lease, err := scanLease(p.pool.QueryRow(ctx, `SELECT `+leaseColumns+` FROM leases WHERE status='active' LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &lease, err
}

func (p *Postgres) SetLeaseAgentTokenHash(ctx context.Context, id, hash string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE leases SET agent_token_hash=$2 WHERE id=$1 AND status='active'`, id, hash)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("active lease not found")
	}
	return err
}

func (p *Postgres) SetLeaseThinking(ctx context.Context, id, thinking string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE leases SET thinking=$2 WHERE id=$1 AND status='active'`, id, thinking)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("active lease not found")
	}
	return err
}

func (p *Postgres) EndLease(ctx context.Context, id, status string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE leases SET status=$2, ended_at=now() WHERE id=$1 AND status='active'`, id, status)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("active lease not found")
	}
	return err
}

func (p *Postgres) ListLeases(ctx context.Context, limit int) ([]domain.Lease, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := p.pool.Query(ctx, `SELECT `+leaseColumns+` FROM leases ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Lease
	for rows.Next() {
		lease, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, lease)
	}
	return result, rows.Err()
}

func (p *Postgres) AppendEvent(ctx context.Context, event domain.Event) (domain.Event, error) {
	err := p.pool.QueryRow(ctx, `INSERT INTO events (at,lease_id,persona_id,actor,type,correlation_id,payload)
    VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, event.At, event.LeaseID, event.PersonaID, event.Actor,
		event.Type, event.CorrelationID, event.Payload).Scan(&event.ID)
	return event, err
}

func (p *Postgres) ListEvents(ctx context.Context, limit int) ([]domain.Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := p.pool.Query(ctx, `SELECT id,at,lease_id,persona_id,actor,type,correlation_id,payload
    FROM events ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Event
	for rows.Next() {
		var event domain.Event
		if err := rows.Scan(&event.ID, &event.At, &event.LeaseID, &event.PersonaID, &event.Actor, &event.Type,
			&event.CorrelationID, &event.Payload); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

// ListEventsQuery is the Postgres twin of Memory.ListEventsQuery and must keep
// identical semantics. Every filter value travels as a numbered placeholder so
// no caller-supplied string ever reaches the SQL text.
func (p *Postgres) ListEventsQuery(ctx context.Context, query domain.EventQuery) ([]domain.Event, error) {
	limit := query.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	conditions := make([]string, 0, 5)
	args := make([]any, 0, 6)
	placeholder := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if query.LeaseID != "" {
		conditions = append(conditions, "lease_id="+placeholder(query.LeaseID))
	}
	if query.PersonaID != "" {
		conditions = append(conditions, "persona_id="+placeholder(query.PersonaID))
	}
	if len(query.Types) > 0 {
		conditions = append(conditions, "type = ANY("+placeholder(query.Types)+")")
	}
	if query.AfterID > 0 {
		conditions = append(conditions, "id > "+placeholder(query.AfterID))
	}
	if query.BeforeID > 0 {
		conditions = append(conditions, "id < "+placeholder(query.BeforeID))
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	// A forward cursor reads the oldest rows past it; everything else reads the
	// newest rows first.
	order := " ORDER BY id DESC LIMIT "
	if query.AfterID > 0 {
		order = " ORDER BY id ASC LIMIT "
	}
	statement := `SELECT id,at,lease_id,persona_id,actor,type,correlation_id,payload FROM events` +
		where + order + placeholder(limit)
	rows, err := p.pool.Query(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Event, 0, limit)
	for rows.Next() {
		var event domain.Event
		if err := rows.Scan(&event.ID, &event.At, &event.LeaseID, &event.PersonaID, &event.Actor, &event.Type,
			&event.CorrelationID, &event.Payload); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (p *Postgres) ListJournalEntries(ctx context.Context, personaID string, limit int) ([]domain.JournalEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := p.pool.Query(ctx, `SELECT id,at,lease_id,persona_id,payload->>'entry'
    FROM events WHERE type='journal.entry' AND ($1='' OR persona_id=$1) ORDER BY id DESC LIMIT $2`, personaID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.JournalEntry
	for rows.Next() {
		var entry domain.JournalEntry
		if err := rows.Scan(&entry.ID, &entry.At, &entry.LeaseID, &entry.PersonaID, &entry.Entry); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (p *Postgres) AppendFrame(ctx context.Context, frame domain.Frame) (domain.Frame, error) {
	err := p.pool.QueryRow(ctx, `INSERT INTO frames
    (lease_id,persona_id,sequence,created_at,source_object,final_object,sha256,width,height,published,publish_error)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, frame.LeaseID, frame.PersonaID, frame.Sequence,
		frame.CreatedAt, frame.SourceObject, frame.FinalObject, frame.SHA256, frame.Width, frame.Height, frame.Published,
		frame.PublishError).Scan(&frame.ID)
	return frame, err
}

func (p *Postgres) ListFrames(ctx context.Context, leaseID string, limit int) ([]domain.Frame, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := p.pool.Query(ctx, `SELECT id,lease_id,persona_id,sequence,created_at,source_object,final_object,
    sha256,width,height,published,publish_error FROM frames WHERE ($1='' OR lease_id=$1) ORDER BY id DESC LIMIT $2`, leaseID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Frame
	for rows.Next() {
		var frame domain.Frame
		if err := rows.Scan(&frame.ID, &frame.LeaseID, &frame.PersonaID, &frame.Sequence, &frame.CreatedAt,
			&frame.SourceObject, &frame.FinalObject, &frame.SHA256, &frame.Width, &frame.Height, &frame.Published,
			&frame.PublishError); err != nil {
			return nil, err
		}
		result = append(result, frame)
	}
	return result, rows.Err()
}

func (p *Postgres) LatestPublishedFrame(ctx context.Context, leaseID string) (*domain.Frame, error) {
	row := p.pool.QueryRow(ctx, `SELECT id,lease_id,persona_id,sequence,created_at,source_object,final_object,
    sha256,width,height,published,publish_error FROM frames
    WHERE published AND ($1='' OR lease_id=$1) ORDER BY id DESC LIMIT 1`, leaseID)
	var value domain.Frame
	if err := row.Scan(&value.ID, &value.LeaseID, &value.PersonaID, &value.Sequence, &value.CreatedAt,
		&value.SourceObject, &value.FinalObject, &value.SHA256, &value.Width, &value.Height, &value.Published,
		&value.PublishError); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &value, nil
}

func (p *Postgres) UpsertInferenceRequest(ctx context.Context, request domain.InferenceRequest) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO inference_requests
    (id,lease_id,persona_id,provider,model,thinking,thinking_source,provider_request_id,started_at,ended_at,status,
     stop_reason,model_calls,prompt_tokens,completion_tokens,reasoning_tokens,cache_read_tokens,cache_write_tokens,
     estimated_metered_micros,provider_reported_micros,actual_billed_micros,allocated_cost_micros,raw_usage)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
    ON CONFLICT (id) DO UPDATE SET provider_request_id=EXCLUDED.provider_request_id, ended_at=EXCLUDED.ended_at,
    status=EXCLUDED.status, stop_reason=EXCLUDED.stop_reason, model_calls=EXCLUDED.model_calls, prompt_tokens=EXCLUDED.prompt_tokens,
    completion_tokens=EXCLUDED.completion_tokens, reasoning_tokens=EXCLUDED.reasoning_tokens,
    cache_read_tokens=EXCLUDED.cache_read_tokens, cache_write_tokens=EXCLUDED.cache_write_tokens,
    estimated_metered_micros=EXCLUDED.estimated_metered_micros, provider_reported_micros=EXCLUDED.provider_reported_micros,
    actual_billed_micros=EXCLUDED.actual_billed_micros, allocated_cost_micros=EXCLUDED.allocated_cost_micros,
    raw_usage=EXCLUDED.raw_usage`, request.ID, request.LeaseID, request.PersonaID, request.Provider, request.Model,
		request.Thinking, request.ThinkingSource, request.ProviderRequestID, request.StartedAt, request.EndedAt, request.Status,
		request.StopReason, request.ModelCalls, request.PromptTokens, request.CompletionTokens, request.ReasoningTokens, request.CacheReadTokens,
		request.CacheWriteTokens, request.EstimatedMeteredMicros, request.ProviderReportedMicros, request.ActualBilledMicros,
		request.AllocatedCostMicros, nullableJSON(request.RawUsage))
	return err
}

func (p *Postgres) ListInferenceRequests(ctx context.Context, leaseID string, limit int) ([]domain.InferenceRequest, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := p.pool.Query(ctx, `SELECT id,lease_id,persona_id,provider,model,thinking,thinking_source,
    provider_request_id,started_at,ended_at,status,stop_reason,model_calls,prompt_tokens,completion_tokens,reasoning_tokens,
    cache_read_tokens,cache_write_tokens,estimated_metered_micros,provider_reported_micros,actual_billed_micros,
    allocated_cost_micros,raw_usage FROM inference_requests WHERE ($1='' OR lease_id=$1) ORDER BY started_at DESC LIMIT $2`, leaseID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.InferenceRequest
	for rows.Next() {
		var request domain.InferenceRequest
		if err := rows.Scan(&request.ID, &request.LeaseID, &request.PersonaID, &request.Provider, &request.Model,
			&request.Thinking, &request.ThinkingSource, &request.ProviderRequestID, &request.StartedAt, &request.EndedAt,
			&request.Status, &request.StopReason, &request.ModelCalls, &request.PromptTokens, &request.CompletionTokens, &request.ReasoningTokens,
			&request.CacheReadTokens, &request.CacheWriteTokens, &request.EstimatedMeteredMicros,
			&request.ProviderReportedMicros, &request.ActualBilledMicros, &request.AllocatedCostMicros, &request.RawUsage); err != nil {
			return nil, err
		}
		result = append(result, request)
	}
	return result, rows.Err()
}

func (p *Postgres) CreateSchedule(ctx context.Context, schedule domain.Schedule) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO schedules
    (id,lease_id,persona_id,kind,label,run_at,interval_ns,missed_policy,enabled,payload,last_run_at,next_run_at)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, schedule.ID, schedule.LeaseID, schedule.PersonaID,
		schedule.Kind, schedule.Label, schedule.RunAt, int64(schedule.Interval), schedule.MissedPolicy, schedule.Enabled,
		schedule.Payload, schedule.LastRunAt, schedule.NextRunAt)
	return err
}

func (p *Postgres) ListSchedules(ctx context.Context, leaseID string, dueAt *time.Time) ([]domain.Schedule, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,lease_id,persona_id,kind,label,run_at,interval_ns,missed_policy,enabled,
    payload,last_run_at,next_run_at FROM schedules WHERE ($1='' OR lease_id=$1)
    AND ($2::timestamptz IS NULL OR (enabled AND next_run_at <= $2)) ORDER BY next_run_at NULLS LAST`, leaseID, dueAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Schedule
	for rows.Next() {
		var schedule domain.Schedule
		var interval int64
		if err := rows.Scan(&schedule.ID, &schedule.LeaseID, &schedule.PersonaID, &schedule.Kind, &schedule.Label,
			&schedule.RunAt, &interval, &schedule.MissedPolicy, &schedule.Enabled, &schedule.Payload, &schedule.LastRunAt,
			&schedule.NextRunAt); err != nil {
			return nil, err
		}
		schedule.Interval = time.Duration(interval)
		result = append(result, schedule)
	}
	return result, rows.Err()
}

func (p *Postgres) DeleteSchedule(ctx context.Context, leaseID, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM schedules WHERE id=$1 AND lease_id=$2`, id, leaseID)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("schedule not found")
	}
	return err
}

// markScheduleRunSQL binds $4 as timestamptz explicitly. Without the casts the
// parameter appears only as a bare assignment target and inside IS NOT NULL, so
// PostgreSQL cannot infer its type and every scheduler tick fails with
// "could not determine data type of parameter $4". Do not drop the casts.
const markScheduleRunSQL = `UPDATE schedules SET last_run_at=$3,next_run_at=$4::timestamptz,enabled=($4::timestamptz IS NOT NULL)
    WHERE id=$1 AND lease_id=$2`

func (p *Postgres) MarkScheduleRun(ctx context.Context, leaseID, id string, ranAt time.Time, next *time.Time) error {
	tag, err := p.pool.Exec(ctx, markScheduleRunSQL, id, leaseID, ranAt, next)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("schedule not found")
	}
	return err
}

func (p *Postgres) QueryHistory(ctx context.Context, leaseID, query string) (domain.SQLResult, error) {
	if err := validateReadOnlySQL(query); err != nil {
		return domain.SQLResult{}, err
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.SQLResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('statement_timeout', '3000', true), set_config('pixel_steward.lease_id', $1, true)`, leaseID); err != nil {
		return domain.SQLResult{}, err
	}
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return domain.SQLResult{}, err
	}
	defer rows.Close()
	result := domain.SQLResult{}
	for _, field := range rows.FieldDescriptions() {
		result.Columns = append(result.Columns, field.Name)
	}
	for rows.Next() {
		if len(result.Rows) == 500 {
			result.Limited = true
			break
		}
		values, err := rows.Values()
		if err != nil {
			return domain.SQLResult{}, err
		}
		result.Rows = append(result.Rows, values)
	}
	return result, rows.Err()
}

func validateReadOnlySQL(query string) error {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.TrimRightFunc(trimmed, func(r rune) bool { return r == ';' || unicode.IsSpace(r) })
	lower := strings.ToLower(trimmed)
	if !(strings.HasPrefix(lower, "select ") || strings.HasPrefix(lower, "select\n") || strings.HasPrefix(lower, "with ") || strings.HasPrefix(lower, "with\n")) {
		return errors.New("history query must be a SELECT or WITH query")
	}
	if strings.Contains(trimmed, ";") {
		return errors.New("multiple SQL statements are not allowed")
	}
	return nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
