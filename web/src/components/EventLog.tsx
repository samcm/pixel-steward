// `.rec-select`, `.rec-meta` and `.rec-clear` come from the shared record
// surface styles; the table below is this component's own.
import '../styles/records.css';
import '../styles/eventlog.css';

import { useMemo, useState } from 'preact/hooks';
import type { JSX } from 'preact';

import type { Persona, StewardEvent } from '../api/types';
import { clockTime, stamp } from '../lib/format';
import { toTranscript } from '../lib/transcript';
import type { TranscriptItem } from '../lib/transcript';
import { CodeBlock, Disclosure, Empty, Failure, Label, Loading, Panel } from './ui';

/**
 * Renderer telemetry: a running renderer emits one of these per frame, roughly
 * once a second, and they bury everything else. Hidden by default and never
 * permanently — this is the forensic view, so the toggle brings them back.
 */
const TELEMETRY: Record<string, true> = {
  'frame.submitted': true,
  'frame.duplicate_skipped': true,
};

/**
 * Persona filter value for events the controller wrote outside any persona.
 * Persona ids come from the configured roster, which has no `system` entry.
 */
const SYSTEM = 'system';

/** Lines of raw JSON shown before the disclosure offers to expand. */
const RAW_CLAMP = 18;

export interface EventLogProps {
  events: StewardEvent[];
  personas: Persona[];
  loading: boolean;
  error?: unknown;
  onRetry: () => void;
  onOpenPersona: (personaID: string) => void;
}

interface Row {
  /** Event id: stable across polls so appended rows never remount old ones. */
  key: string;
  at: string;
  personaID?: string;
  type: string;
  /** Human sentence, straight from the transcript mapping. */
  title: string;
  /** Second line: the command, the model's words, the failure. */
  detail?: string;
  /** The detail is machine text (command, payload) and reads as mono. */
  machine: boolean;
  error: boolean;
  telemetry: boolean;
  raw: string;
  /** Lowercased type + activity text, precomputed so typing stays cheap. */
  search: string;
}

/**
 * The global raw event log: every event the controller wrote, newest first,
 * with the transcript's human wording as the primary text and the verbatim
 * payload one disclosure away. Reachable without opening a persona.
 */
export function EventLog(props: EventLogProps): JSX.Element {
  const { events, personas, loading, error, onRetry, onOpenPersona } = props;

  const [persona, setPersona] = useState('');
  const [query, setQuery] = useState('');
  const [hideTelemetry, setHideTelemetry] = useState(true);

  const names = useMemo<Record<string, string>>(
    () => Object.fromEntries(personas.map((entry) => [entry.id, entry.display_name])),
    [personas],
  );
  const rows = useMemo(() => buildRows(events), [events]);

  const needle = query.trim().toLowerCase();
  // One pass: the rows to show, and how many the telemetry toggle is holding
  // back out of the rows the other filters would have let through.
  const view = useMemo(() => {
    const shown: Row[] = [];
    let hidden = 0;
    for (const row of rows) {
      if (!matches(row, persona, needle)) continue;
      if (hideTelemetry && row.telemetry) hidden++;
      else shown.push(row);
    }
    return { shown, hidden };
  }, [rows, persona, needle, hideTelemetry]);

  const active = persona !== '' || needle !== '';
  const clearFilters = () => {
    setPersona('');
    setQuery('');
  };

  const counted =
    view.shown.length === rows.length
      ? `${rows.length} ${rows.length === 1 ? 'event' : 'events'}`
      : `${view.shown.length} of ${rows.length} events`;

  const meta = (
    <div class="ev-filters">
      <Label>persona</Label>
      <select
        class="rec-select"
        aria-label="Filter events by persona"
        value={persona}
        onChange={(event) => setPersona(event.currentTarget.value)}
      >
        <option value="">All personas</option>
        {personas.map((entry) => (
          <option key={entry.id} value={entry.id}>
            {entry.display_name}
          </option>
        ))}
        <option value={SYSTEM}>system (no persona)</option>
      </select>
      <input
        class="ev-search"
        type="search"
        aria-label="Filter events by type or text"
        placeholder="type or text"
        value={query}
        onInput={(event) => setQuery(event.currentTarget.value)}
      />
      <button
        type="button"
        class="ev-toggle"
        aria-pressed={hideTelemetry}
        title="frame.submitted and frame.duplicate_skipped arrive about once a second while a renderer runs"
        onClick={() => setHideTelemetry(!hideTelemetry)}
      >
        hide renderer telemetry
      </button>
      <span class="rec-meta">
        {counted}
        {view.hidden > 0 ? ` · ${view.hidden} telemetry hidden` : ''}
      </span>
      {active ? (
        <button type="button" class="rec-clear" onClick={clearFilters}>
          clear filters
        </button>
      ) : null}
    </div>
  );

  return (
    <Panel title="Event log" meta={meta} className="eventlog">
      {error === undefined || error === null ? null : <Failure error={error} retry={onRetry} />}
      {loading ? <Loading>Reading the event log…</Loading> : null}
      {!loading && rows.length === 0 ? <Empty>No events recorded yet.</Empty> : null}
      {rows.length > 0 && view.shown.length === 0 ? (
        <Empty>
          <div>No events match the current filters.</div>
          <div class="ev-empty-actions">
            {active ? (
              <button type="button" class="rec-clear" onClick={clearFilters}>
                clear filters
              </button>
            ) : null}
            {view.hidden > 0 ? (
              <button type="button" class="rec-clear" onClick={() => setHideTelemetry(false)}>
                show {view.hidden} renderer telemetry {view.hidden === 1 ? 'event' : 'events'}
              </button>
            ) : null}
          </div>
        </Empty>
      ) : null}
      {view.shown.length === 0 ? null : (
        <div class="ev-scroll">
          <table class="ev-table">
            <thead>
              <tr>
                <th class="ev-head-time">time</th>
                <th class="ev-head-persona">persona</th>
                <th class="ev-head-type">type</th>
                <th class="ev-head-activity">activity</th>
                <th class="ev-head-details">details</th>
              </tr>
            </thead>
            <tbody>
              {view.shown.map((row) => {
                const personaID = row.personaID;
                return (
                  <tr key={row.key} class={row.error ? 'ev-row is-error' : 'ev-row'}>
                    <td class="ev-time" title={stamp(row.at)}>
                      {clockTime(row.at)}
                    </td>
                    <td class="ev-persona">
                      {personaID === undefined ? (
                        <span class="ev-system">system</span>
                      ) : (
                        <button
                          type="button"
                          class="link ev-persona-link"
                          title={personaID}
                          onClick={() => onOpenPersona(personaID)}
                        >
                          {names[personaID] ?? personaID}
                        </button>
                      )}
                    </td>
                    <td class="ev-type">{row.type}</td>
                    <td class="ev-activity">
                      {/* The type column collapses on a phone; the sentence keeps it. */}
                      <span class="ev-type-inline">{row.type}</span>
                      <span class="ev-title">{row.title}</span>
                      {row.detail === undefined ? null : (
                        <span class={row.machine ? 'ev-detail is-machine' : 'ev-detail'} title={row.detail}>
                          {row.detail}
                        </span>
                      )}
                    </td>
                    <td class="ev-details">
                      <Disclosure summary="raw" tone="quiet">
                        <CodeBlock text={row.raw} language="json" clamp={RAW_CLAMP} />
                      </Disclosure>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

/** Persona and free text only; the telemetry toggle is applied by the caller. */
function matches(row: Row, persona: string, needle: string): boolean {
  if (persona === SYSTEM && row.personaID !== undefined) return false;
  if (persona !== '' && persona !== SYSTEM && row.personaID !== persona) return false;
  if (needle !== '' && !row.search.includes(needle)) return false;
  return true;
}

/**
 * Newest first, whatever order the caller supplied. `toTranscript` owns the
 * wording, so a row here reads exactly like the same event in the lease
 * transcript; events it declines to map (an empty model chunk) still get a
 * readable row, because nothing may vanish from the raw log.
 */
function buildRows(events: StewardEvent[]): Row[] {
  const source = Array.isArray(events) ? events.slice() : [];
  source.sort(newestFirst);
  const items = new Map<string, TranscriptItem>();
  for (const item of toTranscript(source)) items.set(item.key, item);
  return source.map((event, index) => toRow(event, items.get(String(idOf(event))), index));
}

function newestFirst(left: StewardEvent, right: StewardEvent): number {
  const delta = epoch(right.at) - epoch(left.at);
  if (delta !== 0) return delta;
  return idOf(right) - idOf(left);
}

function toRow(event: StewardEvent, item: TranscriptItem | undefined, index: number): Row {
  const id = idOf(event);
  const type = str(event.type) ?? 'unknown';
  const at = str(event.at) ?? '';
  // Unknown types still read as a sentence: `frame.duplicate_skipped` becomes
  // `frame · duplicate skipped`, the same shape the transcript falls back to.
  const title = item?.title ?? type.replace(/\./g, ' · ').replace(/_/g, ' ');
  const detail = describe(item);
  return {
    key: id === 0 ? `${type}@${at}#${index}` : String(id),
    at,
    personaID: str(event.persona_id),
    type,
    title,
    detail: detail?.text,
    machine: detail?.machine === true,
    error: item?.kind === 'error',
    telemetry: TELEMETRY[type] === true,
    raw: rawJSON(event),
    search: `${type} ${title} ${detail?.text ?? ''}`.toLowerCase(),
  };
}

/** The second line: what the operator needs before opening the payload. */
function describe(item: TranscriptItem | undefined): { text: string; machine: boolean } | undefined {
  if (item === undefined) return undefined;
  const call = item.tool;
  if (call === undefined) {
    return oneLine(item.body, item.kind !== 'text' && item.kind !== 'journal');
  }
  const invocation = call.command ?? call.input.map(([key, value]) => `${key}=${value}`).join(' ');
  // A non-zero exit is the whole story of a tool row; the transcript keeps it
  // in `status`, and a one-line log entry has to say it out loud.
  const exit = call.exitCode !== undefined && call.exitCode !== 0 ? `exit ${call.exitCode}` : '';
  const failure = call.error ?? exit;
  if (failure !== '' && invocation !== '') return oneLine(`${failure} · ${invocation}`, true);
  if (failure !== '') return oneLine(failure, true);
  if (invocation !== '') return oneLine(invocation, true);
  return oneLine(call.output ?? call.stderr, true);
}

function oneLine(value: string | undefined, machine: boolean): { text: string; machine: boolean } | undefined {
  if (value === undefined) return undefined;
  const collapsed = value.replace(/\s+/g, ' ').trim();
  return collapsed === '' ? undefined : { text: collapsed, machine };
}

/** Exactly the five fields the old dashboard's raw disclosure printed. */
function rawJSON(event: StewardEvent): string {
  try {
    return JSON.stringify(
      {
        actor: event.actor,
        type: event.type,
        lease_id: event.lease_id,
        persona_id: event.persona_id,
        payload: event.payload,
      },
      null,
      2,
    );
  } catch {
    return String(event.payload);
  }
}

function idOf(event: StewardEvent): number {
  return typeof event.id === 'number' && Number.isFinite(event.id) ? event.id : 0;
}

function epoch(value: string | undefined): number {
  const parsed = new Date(value ?? '').getTime();
  return Number.isNaN(parsed) ? 0 : parsed;
}

function str(value: string | undefined): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}
