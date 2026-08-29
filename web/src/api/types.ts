// Typed mirrors of the controller's operator API. Field names track the Go
// json tags in internal/domain, internal/controller, internal/display and
// internal/budget.

export interface Persona {
  id: string;
  display_name: string;
  enabled: boolean;
  weight: number;
  cooldown: number;
  lease: number;
  updated_at: string;
}

export interface Lease {
  id: string;
  persona_id: string;
  model_profile: string;
  thinking: string;
  started_at: string;
  ends_at: string;
  ended_at?: string;
  status: string;
  summary?: string;
  content_digest?: string;
}

export interface StewardEvent {
  id: number;
  at: string;
  lease_id?: string;
  persona_id?: string;
  actor: string;
  type: string;
  correlation_id: string;
  payload: unknown;
}

export interface JournalEntry {
  id: number;
  at: string;
  lease_id: string;
  persona_id: string;
  entry: string;
}

export interface Frame {
  id: number;
  lease_id: string;
  persona_id: string;
  sequence: number;
  created_at: string;
  source_object: string;
  final_object: string;
  sha256: string;
  width: number;
  height: number;
  published: boolean;
  publish_error?: string;
}

export interface InferenceRequest {
  id: string;
  lease_id: string;
  persona_id: string;
  provider: string;
  model: string;
  thinking: string;
  thinking_source: string;
  provider_request_id?: string;
  started_at: string;
  ended_at?: string;
  status: string;
  stop_reason?: string;
  model_calls: number;
  prompt_tokens: number;
  completion_tokens: number;
  reasoning_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  estimated_metered_micros: number;
  provider_reported_micros: number | null;
  actual_billed_micros: number | null;
  allocated_cost_micros: number | null;
  raw_usage?: unknown;
}

export interface Schedule {
  id: string;
  lease_id: string;
  persona_id: string;
  kind: string;
  label: string;
  run_at: string;
  interval: number;
  missed_policy: string;
  enabled: boolean;
  payload: unknown;
  last_run_at?: string;
  next_run_at?: string;
}

export interface Amount {
  used: number;
  reserved: number;
  limit: number;
  remaining: number;
}

export interface DurationAmount {
  used_seconds: number;
  reserved_seconds: number;
  limit_seconds: number;
  remaining_seconds: number;
}

export interface CostSnapshot {
  used_micros: number;
  reserved_micros: number;
  limit_micros: number | null;
  remaining_micros: number | null;
}

export interface BudgetSnapshot {
  as_of: string;
  status: string;
  input_tokens: Amount;
  output_tokens: Amount;
  calls: Amount;
  active_runtime: DurationAmount;
  scene_commits: Amount;
  estimated_metered_cost: CostSnapshot;
  observed_tokens: Record<string, number>;
  per_call_output_limit: number;
}

export interface DisplayStatus {
  online: boolean;
  screen_on: boolean;
  last_frame_at?: string;
  last_error?: string;
  /** When the currently reported last_error was first observed. */
  last_error_at?: string;
  /** When the controller last completed a device probe. */
  checked_at?: string;
  frames: number;
  skipped: number;
}

export interface ReasoningStatus {
  effective: string;
  source: string;
  allowed: string[];
  cache_impact: string;
}

export interface Status {
  as_of: string;
  blackout: boolean;
  scheduled_blackout: boolean;
  test_window_until?: string;
  next_transition: string;
  lease?: Lease;
  budget?: BudgetSnapshot;
  display: DisplayStatus;
  /** True once the controller has armed the panel for the daylight window. */
  display_armed: boolean;
  /** Live probe failure. Absent when the last probe succeeded. */
  display_probe_error?: string;
  display_probe_error_at?: string;
  agent_running: boolean;
  reasoning?: ReasoningStatus;
}

export interface Thinking {
  default: string;
  allowed: string[];
  cache_impact: string;
}

export interface Billing {
  mode: string;
  input_micros_per_mtok?: number;
  output_micros_per_mtok?: number;
  [key: string]: unknown;
}

export interface ModelProfile {
  name: string;
  provider: string;
  model: string;
  endpoint?: string;
  credential_env?: string;
  thinking: Thinking;
  billing: Billing;
  selected: boolean;
}

export interface PersonaDetail {
  persona: Persona;
  configuration: Record<string, unknown>;
  leases: Lease[];
  events: StewardEvent[];
  prompts: StewardEvent[];
  transcript: StewardEvent[];
  frames: Frame[];
  journal: JournalEntry[];
  inference: InferenceRequest[];
  schedules: Schedule[];
  truncated: boolean;
}
