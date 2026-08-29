import type { ComponentChildren } from 'preact';
import { useEffect, useMemo, useRef, useState } from 'preact/hooks';

import { api, objectURL } from '../api/client';
import { useAsync } from '../api/poll';
import type {
  Frame,
  InferenceRequest,
  JournalEntry,
  Lease,
  ModelProfile,
  PersonaDetail,
  Schedule,
  Status,
  StewardEvent,
} from '../api/types';
import {
  ASSIGNMENT_JOIN_NOTE,
  assignmentSourceLabel,
  effectiveAssignment,
  endpointHost,
} from '../lib/assignment';
import { clockTime, count, duration, money, pretty, stamp } from '../lib/format';
import { toTranscript } from '../lib/transcript';
import { ToolCallView } from './ToolCallView';
import { CodeBlock, Disclosure, Empty, Failure, Loading, Modal, Rows } from './ui';

export interface PersonaDeepDiveProps {
  personaID?: string;
  onClose: () => void;
  profiles: ModelProfile[];
  status?: Status;
}

type TabKey =
  | 'overview'
  | 'prompts'
  | 'transcript'
  | 'events'
  | 'leases'
  | 'frames'
  | 'inference'
  | 'journal'
  | 'schedules';

const TABS: Array<{ key: TabKey; label: string }> = [
  { key: 'overview', label: 'Overview' },
  { key: 'prompts', label: 'Prompts' },
  { key: 'transcript', label: 'Transcript' },
  { key: 'events', label: 'Events' },
  { key: 'leases', label: 'Leases' },
  { key: 'frames', label: 'Frames' },
  { key: 'inference', label: 'Inference' },
  { key: 'journal', label: 'Journal' },
  { key: 'schedules', label: 'Schedules' },
];

/**
 * The complete persona record. Every stream the controller exposes is reachable
 * here; nothing is summarised away, and raw JSON stays one disclosure deep.
 */
export function PersonaDeepDive(props: PersonaDeepDiveProps) {
  const { personaID, onClose, profiles, status } = props;
  const detail = useAsync<PersonaDetail | undefined>(
    () => (personaID ? api.personaDetail(personaID) : Promise.resolve(undefined)),
    [personaID],
  );
  const [tab, setTab] = useState<TabKey>('overview');
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([]);

  useEffect(() => {
    setTab('overview');
  }, [personaID]);

  const record = detail.data;

  function moveTab(event: KeyboardEvent, index: number) {
    const last = TABS.length - 1;
    let next: number;
    switch (event.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        next = index === last ? 0 : index + 1;
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        next = index === 0 ? last : index - 1;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = last;
        break;
      default:
        return;
    }
    event.preventDefault();
    setTab(TABS[next].key);
    tabRefs.current[next]?.focus();
  }

  const counts: Record<TabKey, number | undefined> = {
    overview: undefined,
    prompts: record?.prompts.length,
    transcript: record?.transcript.length,
    events: record?.events.length,
    leases: record?.leases.length,
    frames: record?.frames.length,
    inference: record?.inference.length,
    journal: record?.journal.length,
    schedules: record?.schedules.length,
  };

  return (
    <Modal
      open={personaID !== undefined}
      title={record?.persona.display_name || personaID || 'Persona'}
      subtitle={personaID}
      onClose={onClose}
      wide
    >
      <div class="dd">
        {detail.loading ? <Loading>Reading the complete persona record</Loading> : null}
        {detail.error ? <Failure error={detail.error} retry={detail.refresh} /> : null}
        {!detail.loading && !detail.error && !record ? (
          <Empty>No record returned for this persona.</Empty>
        ) : null}

        {record ? (
          <>
            <div class="dd-tabs" role="tablist" aria-label="Persona record sections">
              {TABS.map((entry, index) => {
                const selected = tab === entry.key;
                const badge = counts[entry.key];
                return (
                  <button
                    key={entry.key}
                    ref={(element) => {
                      tabRefs.current[index] = element;
                    }}
                    type="button"
                    role="tab"
                    id={`dd-tab-${entry.key}`}
                    aria-controls="dd-panel"
                    aria-selected={selected}
                    tabIndex={selected ? 0 : -1}
                    class={selected ? 'dd-tab is-selected' : 'dd-tab'}
                    onClick={() => setTab(entry.key)}
                    onKeyDown={(event) => moveTab(event, index)}
                  >
                    {entry.label}
                    {typeof badge === 'number' ? <span class="dd-tab-count mono">{badge}</span> : null}
                  </button>
                );
              })}
            </div>

            <div
              class="dd-panel"
              id="dd-panel"
              role="tabpanel"
              aria-labelledby={`dd-tab-${tab}`}
              tabIndex={0}
            >
              {tab === 'overview' ? (
                <Overview detail={record} profiles={profiles} status={status} />
              ) : null}
              {tab === 'prompts' ? <Prompts events={record.prompts} /> : null}
              {tab === 'transcript' ? (
                <Transcript
                  events={record.transcript}
                  empty="No runtime transcript events for this persona yet."
                />
              ) : null}
              {tab === 'events' ? (
                <Transcript
                  events={record.events}
                  empty="No controller events recorded for this persona yet."
                />
              ) : null}
              {tab === 'leases' ? <Leases leases={record.leases} /> : null}
              {tab === 'frames' ? <Frames frames={record.frames} /> : null}
              {tab === 'inference' ? <Inference requests={record.inference} /> : null}
              {tab === 'journal' ? <Journal entries={record.journal} /> : null}
              {tab === 'schedules' ? <Schedules schedules={record.schedules} /> : null}
            </div>
          </>
        ) : null}
      </div>
    </Modal>
  );
}

function Overview(props: { detail: PersonaDetail; profiles: ModelProfile[]; status?: Status }) {
  const { detail, profiles, status } = props;
  const persona = detail.persona;
  const assignment = effectiveAssignment({
    personaID: persona.id,
    status,
    profiles,
    recentLeases: detail.leases,
  });

  const identity: Array<[string, ComponentChildren]> = [
    ['persona id', <span class="mono">{persona.id}</span>],
    ['display name', persona.display_name || '—'],
    [
      'scheduling',
      <span class={persona.enabled ? 'ok' : 'warn'}>{persona.enabled ? 'enabled' : 'disabled'}</span>,
    ],
    ['weight', <span class="mono">{persona.weight}</span>],
    ['cooldown', <span class="mono">{nanosText(persona.cooldown)}</span>],
    ['lease duration', <span class="mono">{nanosText(persona.lease)}</span>],
    ['record updated', <span class="mono">{stamp(persona.updated_at)}</span>],
  ];

  const route: Array<[string, ComponentChildren]> = [
    ['route', <span class="mono">{assignment.profileName || 'none'}</span>],
    [
      'provider / model',
      <span class="mono">
        {assignment.provider}/{assignment.model}
      </span>,
    ],
    ['reasoning', <span class="mono">{assignment.thinking}</span>],
    ['cache impact', assignment.cacheImpact],
    ['billing mode', assignment.billingMode],
    [
      'endpoint',
      assignment.endpoint ? (
        <span class="mono">{endpointHost(assignment.endpoint)}</span>
      ) : (
        <span class="muted">provider default</span>
      ),
    ],
    [
      'resolved',
      <span class={`source source-${assignment.source}`}>{assignmentSourceLabel(assignment.source)}</span>,
    ],
  ];

  return (
    <div class="dd-sections">
      {detail.truncated ? (
        <p class="warn-strip" role="note">
          History capped at 1,000 rows per stream. Older leases, events, frames and inference records
          exist in storage but are not included in this record.
        </p>
      ) : null}

      <section class="dd-section">
        <h3 class="label">Identity</h3>
        <Rows items={identity} />
      </section>

      <section class="dd-section">
        <h3 class="label">Inference route (joined)</h3>
        <Rows items={route} />
        <p class="hint">{ASSIGNMENT_JOIN_NOTE}</p>
      </section>

      <section class="dd-section">
        <h3 class="label">Configuration</h3>
        <Configuration config={detail.configuration} />
      </section>
    </div>
  );
}

function Configuration(props: { config: Record<string, unknown> }) {
  const entries = Object.entries(props.config ?? {});
  if (entries.length === 0) return <Empty>No secret-safe configuration recorded.</Empty>;
  return (
    <div class="dd-config">
      {entries.map(([key, value]) => (
        <div key={key} class="dd-config-group">
          <h4 class="label">{key.replace(/_/g, ' ')}</h4>
          <ConfigValue value={value} />
        </div>
      ))}
      <Disclosure summary="Raw configuration JSON" tone="quiet">
        <CodeBlock text={pretty(props.config)} language="json" />
      </Disclosure>
    </div>
  );
}

function ConfigValue(props: { value: unknown }) {
  const { value } = props;
  if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
    const items: Array<[string, ComponentChildren]> = Object.entries(value as Record<string, unknown>).map(
      ([key, entry]) => [key.replace(/_/g, ' '), <span class="mono">{scalarText(entry)}</span>],
    );
    return <Rows items={items} />;
  }
  if (typeof value === 'string') {
    if (value.includes('\n') || value.length > 120) return <CodeBlock text={value} clamp={360} />;
    return <p class="prose">{value || '—'}</p>;
  }
  return <CodeBlock text={pretty(value)} language="json" />;
}

function Prompts(props: { events: StewardEvent[] }) {
  const ordered = [...props.events].sort(byNewest);
  if (ordered.length === 0) {
    return (
      <Empty>
        No recorded prompt yet. Verbatim controller inputs are captured from the next wake onward.
      </Empty>
    );
  }
  return (
    <div class="dd-list">
      {ordered.map((event) => {
        const payload = event.payload as { prompt?: unknown; reason?: unknown } | null;
        const prompt =
          typeof payload?.prompt === 'string' && payload.prompt ? payload.prompt : pretty(event.payload);
        const reason = typeof payload?.reason === 'string' && payload.reason ? payload.reason : 'wake';
        return (
          <article key={event.id} class="dd-row">
            <header class="dd-row-head">
              <span class="time mono">{stamp(event.at)}</span>
              <span class="tag tag-quiet">{reason}</span>
              <span class="hint">verbatim controller input</span>
            </header>
            <CodeBlock text={prompt} clamp={480} />
          </article>
        );
      })}
    </div>
  );
}

function Transcript(props: { events: StewardEvent[]; empty: string }) {
  const items = useMemo(() => toTranscript(props.events), [props.events]);
  const [showDemoted, setShowDemoted] = useState(false);
  const demotedCount = items.filter((item) => item.demoted).length;
  const visible = showDemoted ? items : items.filter((item) => !item.demoted);

  if (items.length === 0) return <Empty>{props.empty}</Empty>;

  return (
    <div class="dd-transcript">
      <div class="dd-toolbar">
        <button
          type="button"
          class={showDemoted ? 'toggle is-on' : 'toggle'}
          aria-pressed={showDemoted}
          onClick={() => setShowDemoted(!showDemoted)}
        >
          {showDemoted ? 'Hide' : 'Show'} step lifecycle
          <span class="mono dd-tab-count">{demotedCount}</span>
        </button>
        <span class="hint mono">
          {visible.length}/{items.length} rows
        </span>
      </div>
      <div class="dd-list">
        {visible.map((item) => (
          <article key={item.key} class={`dd-t dd-t-${item.kind}${item.demoted ? ' is-demoted' : ''}`}>
            <header class="dd-row-head">
              <span class="time mono">{clockTime(item.at)}</span>
              <span class="actor mono">{item.actor}</span>
              <span class="t-title">{item.title}</span>
              <span class="hint mono">{item.type}</span>
            </header>
            {item.body ? <pre class="t-body">{item.body}</pre> : null}
            {item.tool ? <ToolCallView tool={item.tool} compact /> : null}
            <Disclosure summary="Raw event JSON" tone="quiet">
              <CodeBlock text={pretty(item.raw)} language="json" />
            </Disclosure>
          </article>
        ))}
      </div>
    </div>
  );
}

function Leases(props: { leases: Lease[] }) {
  if (props.leases.length === 0) return <Empty>No leases recorded for this persona.</Empty>;
  return (
    <table class="dd-table">
      <thead>
        <tr>
          <th scope="col">Started</th>
          <th scope="col">Ended</th>
          <th scope="col">Status</th>
          <th scope="col">Route</th>
          <th scope="col">Reasoning</th>
          <th scope="col">Summary</th>
          <th scope="col">Lease id</th>
          <th scope="col">Content digest</th>
        </tr>
      </thead>
      <tbody>
        {props.leases.map((lease) => (
          <tr key={lease.id}>
            <td class="mono">{stamp(lease.started_at)}</td>
            <td class="mono">{stamp(lease.ended_at ?? lease.ends_at)}</td>
            <td class="mono">{lease.status}</td>
            <td class="mono">{lease.model_profile || '—'}</td>
            <td class="mono">{lease.thinking || '—'}</td>
            <td class="cell-prose">{lease.summary || <span class="muted">—</span>}</td>
            <td class="mono truncate" title={lease.id}>
              {lease.id}
            </td>
            <td class="mono truncate" title={lease.content_digest}>
              {lease.content_digest || '—'}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Frames(props: { frames: Frame[] }) {
  if (props.frames.length === 0) return <Empty>No frames archived for this persona.</Empty>;
  return (
    <div class="frame-grid">
      {props.frames.map((frame) => (
        <figure key={frame.id} class="frame-cell">
          <a href={objectURL(frame.final_object)} target="_blank" rel="noreferrer" title={frame.sha256}>
            <img
              class="frame-thumb"
              src={objectURL(frame.final_object)}
              alt={`frame ${frame.sequence}`}
              width={128}
              height={128}
              loading="lazy"
            />
          </a>
          <figcaption class="frame-meta">
            <span class="mono">
              #{frame.sequence} · {frame.width}×{frame.height}
            </span>
            <span class="mono">{clockTime(frame.created_at)}</span>
            <span class={frame.published ? 'ok' : 'warn'}>
              {frame.published ? 'published' : 'not published'}
            </span>
            {frame.publish_error ? <span class="bad">{frame.publish_error}</span> : null}
          </figcaption>
        </figure>
      ))}
    </div>
  );
}

function Inference(props: { requests: InferenceRequest[] }) {
  if (props.requests.length === 0) return <Empty>No inference requests recorded for this persona.</Empty>;

  let promptTokens = 0;
  let completionTokens = 0;
  let reasoningTokens = 0;
  let cacheRead = 0;
  let cacheWrite = 0;
  let micros = 0;
  for (const request of props.requests) {
    promptTokens += request.prompt_tokens ?? 0;
    completionTokens += request.completion_tokens ?? 0;
    reasoningTokens += request.reasoning_tokens ?? 0;
    cacheRead += request.cache_read_tokens ?? 0;
    cacheWrite += request.cache_write_tokens ?? 0;
    micros += request.estimated_metered_micros ?? 0;
  }

  return (
    <table class="dd-table">
      <thead>
        <tr>
          <th scope="col">Started</th>
          <th scope="col">Provider / model</th>
          <th scope="col">Reasoning</th>
          <th scope="col" class="num">
            Calls
          </th>
          <th scope="col" class="num">
            Prompt
          </th>
          <th scope="col" class="num">
            Completion
          </th>
          <th scope="col" class="num">
            Reasoning tok
          </th>
          <th scope="col" class="num">
            Cache r/w
          </th>
          <th scope="col" class="num">
            Est. cost
          </th>
          <th scope="col">Status</th>
          <th scope="col">Provider request</th>
        </tr>
      </thead>
      <tbody>
        {props.requests.map((request) => (
          <tr key={request.id}>
            <td class="mono">{stamp(request.started_at)}</td>
            <td class="mono">
              {request.provider}/{request.model}
            </td>
            <td class="mono">
              {request.thinking}
              {request.thinking_source ? <span class="hint"> {request.thinking_source}</span> : null}
            </td>
            <td class="num mono">{count(request.model_calls)}</td>
            <td class="num mono">{count(request.prompt_tokens)}</td>
            <td class="num mono">{count(request.completion_tokens)}</td>
            <td class="num mono">{count(request.reasoning_tokens)}</td>
            <td class="num mono">
              {count(request.cache_read_tokens)}/{count(request.cache_write_tokens)}
            </td>
            <td class="num mono">{money(request.estimated_metered_micros)}</td>
            <td>
              <span class={`tag tag-${toneFor(request.status)}`}>{request.status}</span>
              {request.stop_reason ? <div class="hint mono">{request.stop_reason}</div> : null}
              <Disclosure summary="raw usage" tone="quiet">
                <CodeBlock text={pretty(request.raw_usage)} language="json" />
              </Disclosure>
            </td>
            <td class="mono truncate" title={request.provider_request_id}>
              {request.provider_request_id || '—'}
            </td>
          </tr>
        ))}
      </tbody>
      <tfoot>
        <tr>
          <td class="mono">{props.requests.length} calls</td>
          <td colSpan={2} class="muted">
            totals
          </td>
          <td class="num mono">{count(promptTokens)}</td>
          <td class="num mono">{count(completionTokens)}</td>
          <td class="num mono">{count(reasoningTokens)}</td>
          <td class="num mono">
            {count(cacheRead)}/{count(cacheWrite)}
          </td>
          <td class="num mono">{money(micros)}</td>
          <td colSpan={2} />
        </tr>
      </tfoot>
    </table>
  );
}

function Journal(props: { entries: JournalEntry[] }) {
  const ordered = [...props.entries].sort(byNewest);
  if (ordered.length === 0) return <Empty>This persona has not written a journal entry yet.</Empty>;
  return (
    <div class="dd-journal">
      {ordered.map((entry) => (
        <article key={entry.id} class="journal-entry">
          <div class="journal-head mono">
            {stamp(entry.at)}
            {entry.lease_id ? <span class="hint"> · {entry.lease_id}</span> : null}
          </div>
          <p class="prose">{entry.entry}</p>
        </article>
      ))}
    </div>
  );
}

function Schedules(props: { schedules: Schedule[] }) {
  if (props.schedules.length === 0) return <Empty>No schedules registered by this persona.</Empty>;
  return (
    <div class="dd-list">
      {props.schedules.map((schedule) => {
        const items: Array<[string, ComponentChildren]> = [
          ['run at', <span class="mono">{stamp(schedule.run_at)}</span>],
          [
            'interval',
            <span class="mono">{schedule.interval > 0 ? nanosText(schedule.interval) : 'one shot'}</span>,
          ],
          ['missed policy', <span class="mono">{schedule.missed_policy || '—'}</span>],
          ['last run', <span class="mono">{stamp(schedule.last_run_at)}</span>],
          ['next run', <span class="mono">{stamp(schedule.next_run_at)}</span>],
          ['schedule id', <span class="mono">{schedule.id}</span>],
          ['lease', <span class="mono">{schedule.lease_id || '—'}</span>],
        ];
        return (
          <article key={schedule.id} class="dd-row">
            <header class="dd-row-head">
              <span class="mono">{schedule.kind}</span>
              <span class="t-title">{schedule.label || '—'}</span>
              <span class={schedule.enabled ? 'tag tag-active' : 'tag tag-off'}>
                {schedule.enabled ? 'ENABLED' : 'DISABLED'}
              </span>
            </header>
            <Rows items={items} />
            <Disclosure summary="Payload" tone="quiet">
              <CodeBlock text={pretty(schedule.payload)} language="json" />
            </Disclosure>
          </article>
        );
      })}
    </div>
  );
}

/** Go durations arrive as nanosecond integers. */
function nanosText(value: number | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
  const seconds = value / 1e9;
  return `${duration(value / 1e6)} (${seconds >= 10 ? Math.round(seconds) : seconds.toFixed(1)}s)`;
}

function scalarText(value: unknown): string {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'string') return value || '—';
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  return pretty(value);
}

function toneFor(status: string): string {
  const value = status.toLowerCase();
  if (value.includes('error') || value.includes('fail') || value.includes('denied')) return 'bad';
  if (value.includes('running') || value.includes('pending') || value.includes('partial')) return 'warn';
  if (value.includes('complete') || value.includes('success') || value.includes('ok')) return 'ok';
  return 'quiet';
}

function byNewest(a: { at: string }, b: { at: string }): number {
  const left = Date.parse(a.at);
  const right = Date.parse(b.at);
  if (Number.isNaN(left) || Number.isNaN(right)) return 0;
  return right - left;
}
