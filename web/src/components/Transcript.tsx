import '../styles/transcript.css';

import { useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks';

import { useEventFeed } from '../api/eventFeed';
import type { Lease } from '../api/types';
import { clockTime } from '../lib/format';
import { toTranscript } from '../lib/transcript';
import type { TranscriptItem } from '../lib/transcript';
import { ToolCallView } from './ToolCallView';
import { CodeBlock, Disclosure, Empty, Failure, Label, Loading, Panel } from './ui';

/** Select value standing in for events that belong to no lease. */
const CONTROLLER = 'controller-activity';
/** Distance from the bottom that still counts as following the live tail. */
const PIN_SLACK = 48;
const CLAMP_LINES = 14;

export interface TranscriptProps {
  /** Lease the controller reports as live, if any. */
  activeLeaseID?: string;
  agentRunning: boolean;
  /** Selectable leases, newest first, as returned by `/api/v1/leases`. */
  leases: Lease[];
}

interface LeaseOption {
  /** The lease id: also the select value. */
  value: string;
  personaID?: string;
  startedAt: string;
}

export function Transcript(props: TranscriptProps) {
  const leases = useMemo(() => leaseOptions(props.leases, props.activeLeaseID), [props.leases, props.activeLeaseID]);
  const [choice, setChoice] = useState<string | null>(null);
  const [showLifecycle, setShowLifecycle] = useState(false);
  const [pending, setPending] = useState(0);

  // Options arrive newest first, so the head of the list is the newest lease.
  const live = props.activeLeaseID ?? (leases.length === 0 ? undefined : leases[0].value);
  const current = choice === null ? live : choice === CONTROLLER ? undefined : choice;
  const historical = choice !== null && current !== live;

  // Filtered and paged server-side: this lease's transcript-relevant events
  // only, however much renderer telemetry the controller emits beside them.
  const feed = useEventFeed({ leaseID: current, scope: 'transcript' });
  const items = useMemo(() => toTranscript(feed.events), [feed.events]);
  const visible = useMemo(
    () => (showLifecycle ? items : items.filter((item) => !item.demoted)),
    [items, showLifecycle],
  );

  const scroller = useRef<HTMLDivElement | null>(null);
  const pinned = useRef(true);
  const seenLease = useRef<string | undefined>(current);
  const seenKey = useRef<string | undefined>(undefined);
  const seenHead = useRef<string | undefined>(undefined);
  /** Distance from the bottom at the operator's last deliberate scroll. */
  const seenGap = useRef(0);

  // Appended rows only move the viewport while the operator is following the
  // tail; otherwise the arrival is counted and offered as a jump control.
  useLayoutEffect(() => {
    const node = scroller.current;
    if (node === null) return;
    const tail = visible.length === 0 ? undefined : visible[visible.length - 1].key;
    const head = visible.length === 0 ? undefined : visible[0].key;
    if (seenLease.current !== current) {
      // A different lease is a different reading: start at its tail.
      pinned.current = true;
      setPending(0);
      node.scrollTop = node.scrollHeight;
    } else if (tail === seenKey.current) {
      // Nothing appended, so a backfill page has added rows above the viewport.
      // Hold the operator's place instead of letting history push it down.
      if (head !== seenHead.current && !pinned.current) {
        node.scrollTop = node.scrollHeight - node.clientHeight - seenGap.current;
      }
    } else if (pinned.current) {
      node.scrollTop = node.scrollHeight;
      setPending(0);
    } else {
      const previous = seenKey.current === undefined ? -1 : visible.findIndex((item) => item.key === seenKey.current);
      const added = previous === -1 ? visible.length : visible.length - 1 - previous;
      if (added > 0) setPending((count) => count + added);
    }
    seenLease.current = current;
    seenKey.current = tail;
    seenHead.current = head;
    seenGap.current = node.scrollHeight - node.scrollTop - node.clientHeight;
  }, [visible, current]);

  const jump = () => {
    const node = scroller.current;
    if (node !== null) node.scrollTop = node.scrollHeight;
    pinned.current = true;
    setPending(0);
  };

  const meta = (
    <div class="tr-meta">
      <label class="tr-pick">
        <span class="label">lease</span>
        <select
          value={current ?? CONTROLLER}
          onChange={(event) => setChoice(event.currentTarget.value)}
          aria-label="Lease transcript to inspect"
        >
          {leases.map((option) => (
            <option key={option.value} value={option.value}>
              {optionLabel(option, props.activeLeaseID)}
            </option>
          ))}
          {leases.length === 0 ? <option value={CONTROLLER}>controller activity</option> : null}
        </select>
      </label>
      <button
        type="button"
        class="tr-toggle"
        aria-pressed={showLifecycle}
        onClick={() => setShowLifecycle(!showLifecycle)}
        title="Show step lifecycle and token bookkeeping rows"
      >
        lifecycle
      </button>
      {feed.hasMore ? (
        <button
          type="button"
          class="tr-toggle"
          onClick={feed.loadOlder}
          disabled={feed.loadingMore}
          title="Fetch an older page of this lease's transcript"
        >
          {feed.loadingMore ? 'loading older' : 'load older'}
        </button>
      ) : null}
      <span class="tr-count" aria-live="polite">
        {visible.length} {visible.length === 1 ? 'row' : 'rows'}
      </span>
    </div>
  );

  return (
    <Panel title="transcript" className="transcript" meta={meta}>
      {feed.error !== undefined ? <Failure error={feed.error} retry={feed.refresh} /> : null}
      {historical ? (
        <div class="tr-historical">
          <span>
            historical lease <code>{current ?? 'controller activity'}</code> — not the live transcript
          </span>
          <button type="button" onClick={() => setChoice(null)}>
            return to live
          </button>
        </div>
      ) : null}
      <div
        class="tr-scroll"
        ref={scroller}
        tabIndex={0}
        role="log"
        aria-label="Model transcript"
        onScroll={(event) => {
          const node = event.currentTarget;
          const gap = node.scrollHeight - node.scrollTop - node.clientHeight;
          seenGap.current = gap;
          pinned.current = gap <= PIN_SLACK;
          if (pinned.current && pending !== 0) setPending(0);
        }}
      >
        <Content
          items={items}
          visible={visible}
          loading={feed.loading}
          error={feed.error}
          idle={current === undefined}
          agentRunning={props.agentRunning && !historical}
          showLifecycle={showLifecycle}
        />
        {pending > 0 ? (
          <div class="tr-jumpwrap">
            <button type="button" class="tr-jump" onClick={jump}>
              {pending} new
            </button>
          </div>
        ) : null}
      </div>
    </Panel>
  );
}

function Content(props: {
  items: TranscriptItem[];
  visible: TranscriptItem[];
  loading: boolean;
  error: unknown;
  idle: boolean;
  agentRunning: boolean;
  showLifecycle: boolean;
}) {
  if (props.visible.length > 0) {
    return (
      <div class="tr-list">
        {props.visible.map((item) => (
          <Row key={item.key} item={item} />
        ))}
      </div>
    );
  }
  // A failure with nothing retained is already stated by the banner above.
  if (props.error !== undefined && props.items.length === 0) return null;
  if (props.idle) return <Empty>No lease has run yet.</Empty>;
  if (props.loading) return <Loading>Reading the event log</Loading>;
  if (props.agentRunning && props.items.length === 0) {
    return <Empty>Agent is awake. Waiting for the first emitted token.</Empty>;
  }
  if (props.items.length === 0) return <Empty>No transcript recorded for this lease.</Empty>;
  if (!props.showLifecycle) return <Empty>Only lifecycle bookkeeping here. Enable lifecycle to inspect it.</Empty>;
  return <Empty>Nothing recorded for this lease.</Empty>;
}

function Row(props: { item: TranscriptItem }) {
  const item = props.item;
  return (
    <article class={`tr-row tr-row-${item.kind}${item.demoted ? ' tr-row-demoted' : ''}`}>
      <time class="tr-when" dateTime={item.at} title={item.at}>
        {clockTime(item.at)}
      </time>
      <div class="tr-content">
        <Body item={item} />
        <Disclosure summary="raw" tone="quiet">
          <CodeBlock text={rawText(item.raw)} language="json" clamp={CLAMP_LINES} />
        </Disclosure>
      </div>
    </article>
  );
}

function Body(props: { item: TranscriptItem }) {
  const item = props.item;
  if (item.kind === 'text') return <p class="tr-say">{item.body}</p>;
  if (item.kind === 'tool' && item.tool !== undefined) return <ToolCallView tool={item.tool} />;
  if (item.kind === 'journal') {
    return (
      <div class="tr-journal">
        <Label>journal · {item.personaID ?? 'agent'}</Label>
        <p>{item.body}</p>
      </div>
    );
  }
  return (
    <div class="tr-note">
      <div class="tr-line">
        <span class="tr-type">{item.type}</span>
        <span class="tr-title">{item.title}</span>
      </div>
      {item.body === undefined || item.body === '' ? null : <CodeBlock text={item.body} clamp={10} />}
    </div>
  );
}

/**
 * Selectable leases, newest first. The controller's lease list is the source of
 * truth now that events are fetched per lease; the live lease is still offered
 * even when the slower lease poll has not caught up with it yet.
 */
function leaseOptions(leases: Lease[], activeLeaseID: string | undefined): LeaseOption[] {
  const options: LeaseOption[] = [];
  const seen = new Set<string>();
  for (const lease of Array.isArray(leases) ? leases : []) {
    if (typeof lease.id !== 'string' || lease.id === '' || seen.has(lease.id)) continue;
    seen.add(lease.id);
    options.push({ value: lease.id, personaID: lease.persona_id, startedAt: lease.started_at ?? '' });
  }
  if (activeLeaseID !== undefined && !seen.has(activeLeaseID)) {
    options.unshift({ value: activeLeaseID, startedAt: '' });
  }
  return options;
}

function optionLabel(option: LeaseOption, activeLeaseID: string | undefined): string {
  const persona = option.personaID ?? option.value;
  const started = option.startedAt === '' ? '' : ` · ${clockTime(option.startedAt)}`;
  return option.value === activeLeaseID ? `${persona}${started} · live` : `${persona}${started}`;
}

function rawText(raw: unknown): string {
  try {
    return JSON.stringify(raw, null, 2) ?? '';
  } catch {
    return String(raw);
  }
}
