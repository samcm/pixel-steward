import { useMemo } from 'preact/hooks';
import { Empty, Failure, Label, Loading, Panel } from './ui';
import type { JournalEntry, Persona } from '../api/types';
import { since, stamp } from '../lib/format';
import '../styles/records.css';

export interface JournalProps {
  entries: JournalEntry[];
  personas: Persona[];
  personaFilter: string;
  onPersonaFilter: (value: string) => void;
  loading: boolean;
  error?: unknown;
  onRetry: () => void;
  onOpenPersona: (personaID: string) => void;
}

interface DayGroup {
  key: string;
  heading: string;
  entries: JournalEntry[];
}

/**
 * Calendar-day bucket for an entry: a stable key plus the operator-facing
 * heading. Local calendar days, not UTC, and not fixed-width age buckets.
 */
function dayBucket(date: Date, now: Date): { key: string; heading: string } {
  const key = `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
  if (key === `${now.getFullYear()}-${now.getMonth()}-${now.getDate()}`) return { key, heading: 'Today' };
  const yesterday = new Date(now.getTime());
  yesterday.setDate(now.getDate() - 1);
  if (key === `${yesterday.getFullYear()}-${yesterday.getMonth()}-${yesterday.getDate()}`) {
    return { key, heading: 'Yesterday' };
  }
  return {
    key,
    heading: date.toLocaleDateString([], { weekday: 'short', day: '2-digit', month: 'short', year: 'numeric' }),
  };
}

/** Newest first, split into contiguous calendar days so long history stays navigable. */
function groupByDay(entries: JournalEntry[], now: Date): DayGroup[] {
  const ordered = entries.slice().sort((a, b) => {
    const delta = new Date(b.at).getTime() - new Date(a.at).getTime();
    return Number.isNaN(delta) || delta === 0 ? b.id - a.id : delta;
  });
  const groups: DayGroup[] = [];
  for (const entry of ordered) {
    const date = new Date(entry.at);
    const bucket = Number.isNaN(date.getTime())
      ? { key: 'undated', heading: 'Undated' }
      : dayBucket(date, now);
    const open = groups[groups.length - 1];
    if (open !== undefined && open.key === bucket.key) open.entries.push(entry);
    else groups.push({ key: bucket.key, heading: bucket.heading, entries: [entry] });
  }
  return groups;
}

export function Journal(props: JournalProps) {
  const { entries, personas, personaFilter, onPersonaFilter, loading, error, onRetry, onOpenPersona } = props;

  const names = useMemo<Record<string, string>>(
    () => Object.fromEntries(personas.map((persona) => [persona.id, persona.display_name])),
    [personas],
  );
  const groups = useMemo(() => groupByDay(entries, new Date()), [entries]);

  const filtered = personaFilter !== '';
  const filterName = filtered ? (names[personaFilter] ?? personaFilter) : '';

  const meta = (
    <div class="rec-filter">
      <Label>persona</Label>
      <select
        class="rec-select"
        aria-label="Filter journal by persona"
        value={personaFilter}
        onChange={(event) => onPersonaFilter(event.currentTarget.value)}
      >
        <option value="">All personas</option>
        {personas.map((persona) => (
          <option key={persona.id} value={persona.id}>
            {persona.display_name}
          </option>
        ))}
      </select>
      <span class="rec-meta">
        {entries.length} {entries.length === 1 ? 'entry' : 'entries'}
      </span>
      {filtered ? (
        <button type="button" class="rec-clear" onClick={() => onPersonaFilter('')}>
          clear filter
        </button>
      ) : null}
    </div>
  );

  return (
    <Panel title="Journal" meta={meta}>
      {error === undefined || error === null ? null : <Failure error={error} retry={onRetry} />}
      {filtered ? (
        <div class="journal-active">
          <span>filtered to {filterName}</span>
          <button type="button" class="rec-clear" onClick={() => onPersonaFilter('')}>
            show all personas
          </button>
        </div>
      ) : null}
      {loading ? <Loading>Reading the journal…</Loading> : null}
      {!loading && entries.length === 0 ? (
        filtered ? (
          <Empty>
            <div>No journal entries from {filterName}.</div>
            <button type="button" class="rec-clear" onClick={() => onPersonaFilter('')}>
              show all personas
            </button>
          </Empty>
        ) : (
          <Empty>No agent-written journal entries yet.</Empty>
        )
      ) : null}
      {groups.length === 0 ? null : (
        <div class="journal-scroll">
          {groups.map((group) => (
            <section class="journal-group" key={group.key}>
              <h3 class="journal-day">
                <span>{group.heading}</span>
                <span>
                  {group.entries.length} {group.entries.length === 1 ? 'entry' : 'entries'}
                </span>
              </h3>
              {group.entries.map((entry) => {
                const relative = since(entry.at);
                return (
                  <article class="journal-entry" key={entry.id}>
                    <div class="journal-head">
                      <button
                        type="button"
                        class="link journal-persona"
                        onClick={() => onOpenPersona(entry.persona_id)}
                      >
                        {names[entry.persona_id] ?? entry.persona_id}
                      </button>
                      <span class="rec-meta" title={`lease ${entry.lease_id}`}>
                        {stamp(entry.at)} · {relative === 'just now' ? relative : `${relative} ago`}
                      </span>
                    </div>
                    <p class="journal-prose">{entry.entry}</p>
                  </article>
                );
              })}
            </section>
          ))}
        </div>
      )}
    </Panel>
  );
}
