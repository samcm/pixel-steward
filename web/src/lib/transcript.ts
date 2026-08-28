// Event -> transcript mapping. Pure, dependency free, and total: payloads are
// opaque JSON written by the controller and by opencode, so every access is
// defensive and nothing here may throw on a hostile or truncated payload.
//
// The controller emits one event per opencode stdout line, typed
// `runtime.<opencode type>` with the whole parsed line as payload, so
// `runtime.text` payload is `{ type: 'text', part: { text } }` and
// `runtime.tool_use` payload is `{ type: 'tool_use', part: { tool, state } }`.
// Unparseable lines arrive as `runtime.output` with a plain string payload.

import type { StewardEvent } from '../api/types';

export type TranscriptKind = 'text' | 'tool' | 'controller' | 'journal' | 'frame' | 'error' | 'lifecycle';

export interface ToolCall {
  /** Exact runtime tool name. Never a friendly rename. */
  name: string;
  /** Complete command for shell-like tools. Never truncated in the data layer. */
  command?: string;
  /** Structured input flattened to ordered pairs, insertion order preserved. */
  input: Array<[string, string]>;
  status: string;
  output?: string;
  stderr?: string;
  error?: string;
  durationMs?: number;
  title?: string;
  /** Process exit code once known. Non-zero is a failure, see `toolTone`. */
  exitCode?: number;
  /** The tool itself reported that it cut the output short. */
  truncated?: boolean;
  /**
   * Result fields beyond exit/stdout/stderr/duration, kept readable and kept
   * distinct from the call inputs. Absent unless the tool reported extras.
   */
  result?: Array<[string, string]>;
}

export interface TranscriptItem {
  /** Stable per-event identity so appended rows never remount existing rows. */
  key: string;
  at: string;
  kind: TranscriptKind;
  leaseID?: string;
  personaID?: string;
  actor: string;
  type: string;
  /** Step lifecycle and token bookkeeping: hidden from the primary narrative. */
  demoted: boolean;
  title: string;
  body?: string;
  tool?: ToolCall;
  /** The original event, kept verbatim for forensic inspection. */
  raw: unknown;
}

export interface LeaseGroup {
  leaseID: string | undefined;
  personaID?: string;
  startedAt: string;
  endedAt?: string;
  items: TranscriptItem[];
}

/**
 * Events arrive newest-first from `/api/v1/events`; the transcript reads
 * oldest-first. Sorted by timestamp then id so equal timestamps keep insertion
 * order from the controller.
 */
export function toTranscript(events: StewardEvent[]): TranscriptItem[] {
  const source = Array.isArray(events) ? events.slice() : [];
  source.sort(chronological);
  const merged = planMerges(source);
  const items: TranscriptItem[] = [];
  for (let index = 0; index < source.length; index++) {
    if (merged.dropped.has(index)) continue;
    const item = convert(source[index], index, merged.results.get(index));
    if (item !== undefined) items.push(item);
  }
  return items;
}

/** One group per contiguous run of the same lease. Lease-less rows group under `undefined`. */
export function groupByLease(items: TranscriptItem[]): LeaseGroup[] {
  const groups: LeaseGroup[] = [];
  for (const item of Array.isArray(items) ? items : []) {
    const last = groups[groups.length - 1];
    if (last !== undefined && last.leaseID === item.leaseID) {
      last.items.push(item);
      last.endedAt = item.at;
      if (last.personaID === undefined) last.personaID = item.personaID;
      continue;
    }
    groups.push({
      leaseID: item.leaseID,
      personaID: item.personaID,
      startedAt: item.at,
      endedAt: item.at,
      items: [item],
    });
  }
  return groups;
}

/**
 * One tone for both renderers. A non-zero exit code, an explicit error or
 * anything written to stderr is a failure; an unsettled call is a warning.
 * The data layer never fabricates an `error` string for a non-zero exit — this
 * is what tones the row instead, so exit code stays the single source of truth.
 */
export function toolTone(call: ToolCall): 'ok' | 'warn' | 'bad' {
  if (call.exitCode !== undefined && call.exitCode !== 0) return 'bad';
  if (call.error !== undefined || call.stderr !== undefined) return 'bad';
  if (/error|fail|abort|denied/i.test(call.status)) return 'bad';
  if (call.exitCode === undefined && /pending|running|partial/i.test(call.status)) return 'warn';
  return 'ok';
}

function chronological(left: StewardEvent, right: StewardEvent): number {
  const delta = epoch(record(left).at) - epoch(record(right).at);
  if (delta !== 0) return delta;
  return (numeric(record(left).id) ?? 0) - (numeric(record(right).id) ?? 0);
}

function convert(event: StewardEvent, index: number, authoritative?: ExecResult): TranscriptItem | undefined {
  const source = record(event);
  const type = str(source.type) ?? 'unknown';
  const payload = record(source.payload);
  const base: TranscriptItem = {
    key: identity(source, type, index),
    at: str(source.at) ?? '',
    kind: 'controller',
    leaseID: str(source.lease_id),
    personaID: str(source.persona_id),
    actor: str(source.actor) ?? 'controller',
    type,
    demoted: false,
    title: type.replace(/\./g, ' · ').replace(/_/g, ' '),
    raw: event,
  };
  if (type.startsWith('runtime.')) {
    return runtimeItem(base, type.slice('runtime.'.length), payload, source.payload, authoritative);
  }
  return controllerItem(base, payload);
}

function runtimeItem(
  base: TranscriptItem,
  kind: string,
  payload: Record<string, unknown>,
  rawPayload: unknown,
  authoritative?: ExecResult,
): TranscriptItem | undefined {
  const part = record(payload.part);
  if (kind === 'text') {
    const value = pickString(part.text) ?? pickString(payload.text) ?? '';
    if (value.trim() === '') return undefined;
    return { ...base, kind: 'text', title: 'model', body: value };
  }
  if (kind === 'tool_use') {
    const tool = toolCall(part, authoritative);
    return { ...base, kind: 'tool', title: tool.title ?? tool.name, tool };
  }
  if (kind === 'error') {
    // A real runtime failure is never demoted: it must never hide behind the
    // lifecycle toggle. The message may be a string, an object with `message`,
    // or the whole payload may be a bare string.
    const message =
      messageOf(part.error) ??
      messageOf(payload.error) ??
      messageOf(part.message) ??
      messageOf(payload.message) ??
      (typeof rawPayload === 'string' ? str(rawPayload) : undefined);
    const head = message === undefined ? undefined : headline(message);
    const full = message ?? jsonText(rawPayload);
    return {
      ...base,
      kind: 'error',
      demoted: false,
      title: detail('runtime error', head),
      body: full === head ? undefined : full,
    };
  }
  if (kind === 'step_start') {
    return { ...base, kind: 'lifecycle', demoted: true, title: 'step start' };
  }
  if (kind === 'step_finish') {
    return { ...base, kind: 'lifecycle', demoted: true, title: stepFinishTitle(part) };
  }
  if (kind === 'output') {
    const value = typeof rawPayload === 'string' ? rawPayload : jsonText(rawPayload);
    return { ...base, kind: 'lifecycle', demoted: true, title: 'runtime output', body: value };
  }
  return { ...base, kind: 'lifecycle', demoted: true, title: `runtime ${kind.replace(/_/g, ' ')}` };
}

function toolCall(part: Record<string, unknown>, authoritative?: ExecResult): ToolCall {
  const state = record(part.state);
  // opencode carries the live call under `part.state`; older lines are flat.
  const pick = (key: string): unknown => (state[key] !== undefined ? state[key] : part[key]);
  const metadata = record(pick('metadata'));
  const timing = record(pick('time'));

  const call: ToolCall = {
    name: str(part.tool) ?? str(part.name) ?? 'tool',
    input: [],
    status: '',
  };

  const rawInput = pick('input');
  const pairs: Array<[string, string]> = [];
  if (isPlainObject(rawInput)) {
    for (const key of Object.keys(rawInput)) {
      const value = rawInput[key];
      if (key === 'command' && (typeof value === 'string' || typeof value === 'number')) {
        call.command = String(value);
        continue;
      }
      pairs.push([key, plain(value)]);
    }
  } else if (rawInput !== undefined && rawInput !== null && rawInput !== '') {
    pairs.push(['input', plain(rawInput)]);
  }
  call.input = pairs;

  // On the live controller `state.output` is a JSON-encoded exec result. Decode
  // it: the encoded blob must never reach a renderer as the primary output.
  const rawOutput = pick('output') !== undefined ? pick('output') : pick('result');
  const decoded = outputObject(rawOutput);
  const structured = decoded === undefined ? undefined : execFields(decoded);
  let output: string | undefined;
  let stderr: string | undefined;
  let exitCode: number | undefined;
  let durationMs: number | undefined;
  if (structured !== undefined) {
    output = structured.stdout;
    stderr = structured.stderr;
    exitCode = structured.exitCode;
    durationMs = structured.durationMs;
  } else if (decoded !== undefined) {
    // An object output that is not an exec result: pretty, never the one-liner.
    output = jsonText(decoded);
  } else if (typeof rawOutput === 'string') {
    const split = splitStreams(rawOutput);
    output = split.output;
    stderr = split.stderr;
  } else if (rawOutput !== undefined && rawOutput !== null) {
    output = plain(rawOutput);
  }
  output = pickString(metadata.stdout) ?? output;
  stderr = pickString(metadata.stderr) ?? stderr;
  exitCode = exitCode ?? numeric(metadata.exit) ?? numeric(metadata.exitCode) ?? numeric(metadata.exit_code);
  const start = numeric(timing.start);
  const end = numeric(timing.end);
  if (durationMs === undefined && start !== undefined && end !== undefined && end >= start) {
    durationMs = end - start;
  }

  // The controller's own sandbox record outranks anything opencode reported.
  if (authoritative !== undefined) {
    if (authoritative.stdout !== undefined) output = authoritative.stdout;
    if (authoritative.stderr !== undefined) stderr = authoritative.stderr;
    if (authoritative.exitCode !== undefined) exitCode = authoritative.exitCode;
    if (authoritative.durationMs !== undefined) durationMs = authoritative.durationMs;
  }

  if (output !== undefined && output.trim() === '') output = undefined;
  if (stderr !== undefined && stderr.trim() === '') stderr = undefined;
  if (output !== undefined) call.output = output;
  if (stderr !== undefined) call.stderr = stderr;
  if (exitCode !== undefined) call.exitCode = exitCode;
  if (durationMs !== undefined) call.durationMs = durationMs;
  if (metadata.truncated === true) call.truncated = true;
  const rows =
    authoritative !== undefined && authoritative.extra.length > 0 ? authoritative.extra : structured?.extra;
  if (rows !== undefined && rows.length > 0) call.result = rows;

  const rawError = pick('error');
  const error = pickString(rawError) ?? pickString(record(rawError).message);
  if (error !== undefined) call.error = error;

  const reported = str(pick('status'));
  const settled = error !== undefined ? 'error' : output !== undefined || stderr !== undefined ? 'completed' : 'pending';
  const status = reported ?? settled;
  call.status = exitCode === undefined ? status : `${status} exit ${exitCode}`;

  const title = str(pick('title'));
  if (title !== undefined) call.title = title;

  return call;
}

function stepFinishTitle(part: Record<string, unknown>): string {
  const tokens = record(part.tokens);
  const cache = record(tokens.cache);
  const segments: string[] = [];
  const input = numeric(tokens.input);
  const output = numeric(tokens.output);
  const reasoning = numeric(tokens.reasoning);
  const read = numeric(cache.read);
  const write = numeric(cache.write);
  const cost = numeric(part.cost);
  if (input !== undefined) segments.push(`${input} in`);
  if (output !== undefined) segments.push(`${output} out`);
  if (reasoning) segments.push(`${reasoning} reasoning`);
  if (read || write) segments.push(`cache ${read ?? 0}r/${write ?? 0}w`);
  if (cost) segments.push(`$${cost.toFixed(4)}`);
  const reason = str(part.reason);
  if (reason !== undefined) segments.push(reason);
  return segments.length === 0 ? 'step finish' : `step finish · ${segments.join(' · ')}`;
}

function controllerItem(base: TranscriptItem, payload: Record<string, unknown>): TranscriptItem {
  switch (base.type) {
    case 'journal.entry':
      return { ...base, kind: 'journal', title: 'journal entry', body: str(payload.entry) ?? '' };
    case 'agent.wake.failed':
      return { ...base, kind: 'error', title: detail('agent wake failed', str(payload.error)) };
    case 'frame.rejected':
      return { ...base, kind: 'error', title: detail('frame rejected', str(payload.error)) };
    case 'controller.tick.error':
      return { ...base, kind: 'error', title: detail('controller tick failed', str(payload.error)) };
    case 'frame.submitted': {
      const sequence = numeric(payload.sequence);
      const verb = payload.published === true ? 'published' : 'submitted';
      const head = sequence === undefined ? `frame ${verb}` : `frame #${sequence} ${verb}`;
      const failure = str(payload.publish_error);
      return { ...base, kind: 'frame', title: failure === undefined ? head : `${head} — publish failed: ${failure}` };
    }
    case 'frame.duplicate_skipped': {
      const digest = str(payload.sha256);
      const suffix = digest === undefined ? '' : ` (${digest.slice(0, 12)})`;
      return { ...base, kind: 'frame', title: `duplicate frame skipped${suffix}` };
    }
    case 'lease.selected': {
      const selected = str(payload.selected_id) ?? base.personaID ?? 'persona';
      const previous = str(payload.previous_id);
      const head = `lease selected — ${selected}`;
      return { ...base, title: previous === undefined ? head : `${head} (previous ${previous})` };
    }
    case 'lease.ended':
      return { ...base, title: detail('lease ended', str(payload.reason)) };
    case 'lease.revoked':
      return { ...base, title: 'lease revoked by operator' };
    case 'lease.reasoning_override':
      return {
        ...base,
        title: `reasoning override — ${str(payload.previous) ?? '?'} → ${str(payload.effective) ?? '?'}`,
      };
    case 'agent.prompt':
      return { ...base, title: qualified('wake brief sent', str(payload.reason)), body: str(payload.prompt) };
    case 'agent.wake.started':
      return { ...base, title: qualified('agent wake started', str(payload.reason)) };
    case 'agent.wake.completed':
      return { ...base, title: qualified('agent wake completed', str(payload.reason)) };
    case 'schedule.created':
    case 'schedule.skipped': {
      const verb = base.type === 'schedule.created' ? 'schedule created' : 'schedule skipped';
      const label = str(payload.label) ?? str(payload.kind) ?? 'wake';
      const runAt = str(payload.run_at);
      return { ...base, title: runAt === undefined ? `${verb} — ${label}` : `${verb} — ${label} at ${runAt}` };
    }
    case 'sandbox.exec': {
      const failure = str(payload.error);
      const head = failure === undefined ? 'sandbox exec' : `sandbox exec failed: ${failure}`;
      const result = execResult(payload.result);
      // Without a result there is nothing to render but the command itself.
      if (result === undefined) return { ...base, title: head, body: str(payload.command) };
      return { ...base, kind: 'tool', title: head, tool: execToolCall(payload, result, failure) };
    }
    case 'display.armed':
      return { ...base, title: 'display armed — waiting for the first steward frame' };
    case 'display.screen':
      return { ...base, title: `display screen ${payload.on === true ? 'on' : 'off'}` };
    case 'persona.enabled_override': {
      const verb = payload.enabled === true ? 'persona enabled' : 'persona disabled';
      return { ...base, title: base.personaID === undefined ? verb : `${verb} — ${base.personaID}` };
    }
    default:
      return base;
  }
}

const STDOUT_TAG = /<stdout>\n?([\s\S]*?)\n?<\/stdout>/;
const STDERR_TAG = /<stderr>\n?([\s\S]*?)\n?<\/stderr>/;

function splitStreams(value: string): { output?: string; stderr?: string } {
  const out = STDOUT_TAG.exec(value);
  const err = STDERR_TAG.exec(value);
  if (out === null && err === null) return { output: value };
  return { output: out === null ? undefined : out[1], stderr: err === null ? undefined : err[1] };
}

/**
 * The structured result both sides of an execution report:
 * `{exit_code, stdout, stderr, duration_ms}`. opencode ships it JSON-encoded
 * inside `state.output`; the controller ships it as a real object in
 * `sandbox.exec` `payload.result`.
 */
interface ExecResult {
  exitCode?: number;
  stdout?: string;
  stderr?: string;
  durationMs?: number;
  /** Every other key, kept readable rather than dropped. */
  extra: Array<[string, string]>;
}

/** Keys the structured result renders as first-class fields, not extra rows. */
const EXEC_KEYS: Record<string, true> = {
  exit_code: true,
  exitCode: true,
  stdout: true,
  stderr: true,
  duration_ms: true,
  durationMs: true,
};

/** A tool output as an object, decoding a JSON-encoded string first. */
function outputObject(value: unknown): Record<string, unknown> | undefined {
  if (isPlainObject(value)) return value;
  if (typeof value !== 'string') return undefined;
  const text = value.trim();
  if (!text.startsWith('{') || !text.endsWith('}')) return undefined;
  try {
    const parsed: unknown = JSON.parse(text);
    return isPlainObject(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}

/** `undefined` when the object carries none of the exec result fields. */
function execFields(source: Record<string, unknown>): ExecResult | undefined {
  const exitCode = numeric(source.exit_code) ?? numeric(source.exitCode);
  const stdout = pickString(source.stdout);
  const stderr = pickString(source.stderr);
  const durationMs = numeric(source.duration_ms) ?? numeric(source.durationMs);
  if (exitCode === undefined && stdout === undefined && stderr === undefined && durationMs === undefined) {
    return undefined;
  }
  const result: ExecResult = { extra: [] };
  if (exitCode !== undefined) result.exitCode = exitCode;
  if (stdout !== undefined) result.stdout = stdout;
  if (stderr !== undefined) result.stderr = stderr;
  if (durationMs !== undefined) result.durationMs = durationMs;
  for (const key of Object.keys(source)) {
    if (EXEC_KEYS[key] === true) continue;
    result.extra.push([key, plain(source[key])]);
  }
  return result;
}

function execResult(value: unknown): ExecResult | undefined {
  const source = outputObject(value);
  return source === undefined ? undefined : execFields(source);
}

/** The controller's authoritative exec record, shaped as a tool call. */
function execToolCall(
  payload: Record<string, unknown>,
  result: ExecResult,
  failure: string | undefined,
): ToolCall {
  const call: ToolCall = { name: 'sandbox.exec', input: [], status: '' };
  const command = str(payload.command);
  if (command !== undefined) call.command = command;
  const timeout = numeric(payload.timeout_ms);
  if (timeout !== undefined) call.input.push(['timeout_ms', String(timeout)]);
  if (result.stdout !== undefined && result.stdout.trim() !== '') call.output = result.stdout;
  if (result.stderr !== undefined && result.stderr.trim() !== '') call.stderr = result.stderr;
  if (result.exitCode !== undefined) call.exitCode = result.exitCode;
  if (result.durationMs !== undefined) call.durationMs = result.durationMs;
  if (result.extra.length > 0) call.result = result.extra;
  if (failure !== undefined) call.error = failure;
  const status = failure === undefined ? 'completed' : 'error';
  call.status = result.exitCode === undefined ? status : `${status} exit ${result.exitCode}`;
  return call;
}

/** How far from a `sandbox.exec` row its own `runtime.tool_use` row may sit. */
const MERGE_WINDOW = 8;

interface MergePlan {
  /** Event indices whose standalone row the merge replaces. */
  dropped: Set<number>;
  /** Authoritative exec result, keyed by `runtime.tool_use` event index. */
  results: Map<number, ExecResult>;
}

/**
 * `sandbox.exec` and the agent's own `runtime.tool_use` row describe one
 * execution from two sides. When the commands match within a few events of each
 * other, the authoritative controller result is folded into the agent's row and
 * the duplicate row is dropped; otherwise the sandbox row renders standalone.
 */
function planMerges(events: StewardEvent[]): MergePlan {
  const plan: MergePlan = { dropped: new Set(), results: new Map() };
  for (let index = 0; index < events.length; index++) {
    const source = record(events[index]);
    if (str(source.type) !== 'sandbox.exec') continue;
    const payload = record(source.payload);
    const command = str(payload.command);
    if (command === undefined) continue;
    const result = execResult(payload.result);
    if (result === undefined) continue;
    const target = adjacentToolUse(events, index, command, str(source.lease_id), plan.results);
    if (target === undefined) continue;
    plan.results.set(target, result);
    plan.dropped.add(index);
  }
  return plan;
}

function adjacentToolUse(
  events: StewardEvent[],
  origin: number,
  command: string,
  leaseID: string | undefined,
  claimed: Map<number, ExecResult>,
): number | undefined {
  for (let step = 1; step <= MERGE_WINDOW; step++) {
    const before = origin - step;
    if (ranCommand(events, before, command, leaseID, claimed)) return before;
    const after = origin + step;
    if (ranCommand(events, after, command, leaseID, claimed)) return after;
  }
  return undefined;
}

function ranCommand(
  events: StewardEvent[],
  index: number,
  command: string,
  leaseID: string | undefined,
  claimed: Map<number, ExecResult>,
): boolean {
  if (index < 0 || index >= events.length || claimed.has(index)) return false;
  const source = record(events[index]);
  if (str(source.type) !== 'runtime.tool_use' || str(source.lease_id) !== leaseID) return false;
  return commandOf(record(source.payload)) === command;
}

/** The command a `runtime.tool_use` payload ran, in state or flat shape. */
function commandOf(payload: Record<string, unknown>): string | undefined {
  const part = record(payload.part);
  const state = record(part.state);
  const input = record(state.input !== undefined ? state.input : part.input);
  const command = input.command;
  if (typeof command === 'number') return String(command);
  return typeof command === 'string' && command !== '' ? command : undefined;
}

function messageOf(value: unknown): string | undefined {
  const direct = str(value);
  if (direct !== undefined) return direct;
  if (!isPlainObject(value)) return undefined;
  return str(value.message) ?? str(value.error);
}

/** Title limit: a row title is one line, the body carries the rest. */
const TITLE_LIMIT = 160;

function headline(value: string): string {
  const first = value.split('\n', 1)[0].trim();
  const text = first === '' ? value.trim() : first;
  return text.length <= TITLE_LIMIT ? text : `${text.slice(0, TITLE_LIMIT)}…`;
}

function identity(source: Record<string, unknown>, type: string, index: number): string {
  const id = source.id;
  if (typeof id === 'number' && Number.isFinite(id)) return String(id);
  if (typeof id === 'string' && id !== '') return id;
  return `${type}@${str(source.at) ?? ''}#${index}`;
}

function detail(head: string, value: string | undefined): string {
  return value === undefined ? head : `${head}: ${value}`;
}

function qualified(head: string, value: string | undefined): string {
  return value === undefined ? head : `${head} (${value})`;
}

function record(value: unknown): Record<string, unknown> {
  return isPlainObject(value) ? value : {};
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function str(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

function pickString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined;
}

function numeric(value: unknown): number | undefined {
  if (typeof value === 'number') return Number.isFinite(value) ? value : undefined;
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

function epoch(value: unknown): number {
  if (typeof value !== 'string') return 0;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

function plain(value: unknown): string {
  if (value === null) return 'null';
  if (value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  return jsonText(value);
}

function jsonText(value: unknown): string {
  try {
    const encoded = JSON.stringify(value, null, 2);
    return encoded === undefined ? '' : encoded;
  } catch {
    return '[unserializable]';
  }
}
