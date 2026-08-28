// Paged, append-only event feed for one lease's transcript.
//
// The live controller emits roughly one `frame.submitted` and one
// `controller.tick.error` every second, so an unfiltered "last N events" fetch
// loses the model's own words within minutes. The fix is not a bigger N: this
// feed asks the store for one lease's transcript-relevant events only, pages
// backwards until that transcript is whole, and then polls forward from the
// newest id it holds. Polling appends; it never replaces the accumulated array
// and never drops a row because a poll happened to return fewer of them.

import { useCallback, useEffect, useRef, useState } from 'preact/hooks';

import { api } from './client';
import type { StewardEvent } from './types';

export type FeedScope = 'transcript' | 'all';

export interface EventFeed {
  /** Ascending by id, deduplicated, with stable per-event object identity. */
  events: StewardEvent[];
  /** True only before the first page of the current lease resolves. */
  loading: boolean;
  /** A backfill page is in flight. */
  loadingMore: boolean;
  /** Older transcript history remains and can be fetched with `loadOlder`. */
  hasMore: boolean;
  /** Most recent failure. Cleared by the next success; events are retained. */
  error: unknown;
  /** The whole lease transcript has been loaded. */
  complete: boolean;
  loadOlder: () => void;
  refresh: () => void;
}

export interface EventFeedArgs {
  leaseID: string | undefined;
  scope?: FeedScope;
  /** Events per request. */
  pageSize?: number;
  /** Forward poll cadence. */
  intervalMs?: number;
  /** Backfill pages loaded without the operator asking. */
  maxBackfillPages?: number;
}

const EMPTY: StewardEvent[] = [];
const NOOP = () => {};

interface FeedState {
  /** Identifies the lease/scope the rest of this object describes. */
  key: string;
  events: StewardEvent[];
  loading: boolean;
  loadingMore: boolean;
  hasMore: boolean;
  error: unknown;
  complete: boolean;
}

/**
 * Merge `incoming` into `held`, keyed by event id, and return an ascending
 * array. Append-only: an id already held keeps its existing object, so Preact
 * reuses that row's DOM and an expanded disclosure, a text selection or the
 * scroll position all survive a poll. Nothing new to add returns `held` itself,
 * which lets a poll that only re-reports known events cost zero renders.
 */
export function mergeEvents(held: StewardEvent[], incoming: StewardEvent[]): StewardEvent[] {
  if (!Array.isArray(incoming) || incoming.length === 0) return held;
  const known = new Set<number>();
  for (const event of held) known.add(event.id);
  const fresh: StewardEvent[] = [];
  for (const event of incoming) {
    if (!isEvent(event) || known.has(event.id)) continue;
    known.add(event.id);
    fresh.push(event);
  }
  if (fresh.length === 0) return held;
  const merged = held.concat(fresh);
  merged.sort((left, right) => left.id - right.id);
  return merged;
}

/** Forward cursor: the highest id held, or undefined on an empty feed. */
export function newestID(events: StewardEvent[]): number | undefined {
  let newest: number | undefined = undefined;
  for (const event of events) {
    if (!isEvent(event)) continue;
    if (newest === undefined || event.id > newest) newest = event.id;
  }
  return newest;
}

/** Backward cursor: the lowest id held, or undefined on an empty feed. */
export function oldestID(events: StewardEvent[]): number | undefined {
  let oldest: number | undefined = undefined;
  for (const event of events) {
    if (!isEvent(event)) continue;
    if (oldest === undefined || event.id < oldest) oldest = event.id;
  }
  return oldest;
}

function isEvent(value: unknown): value is StewardEvent {
  if (typeof value !== 'object' || value === null || !('id' in value)) return false;
  return typeof value.id === 'number' && Number.isFinite(value.id);
}

function blank(key: string, loading: boolean): FeedState {
  return {
    key,
    events: EMPTY,
    loading,
    loadingMore: false,
    hasMore: false,
    error: undefined,
    complete: false,
  };
}

/** Structural equality over the published fields, events compared by identity. */
function unchanged(left: FeedState, right: FeedState): boolean {
  return (
    left.events === right.events &&
    left.loading === right.loading &&
    left.loadingMore === right.loadingMore &&
    left.hasMore === right.hasMore &&
    left.error === right.error &&
    left.complete === right.complete
  );
}

export function useEventFeed(args: EventFeedArgs): EventFeed {
  const leaseID = args.leaseID === '' ? undefined : args.leaseID;
  const scope: FeedScope = args.scope ?? 'transcript';
  const pageSize = args.pageSize ?? 200;
  const intervalMs = args.intervalMs ?? 2500;
  const maxBackfillPages = args.maxBackfillPages ?? 10;
  const key = `${scope}\u0000${leaseID ?? ''}`;

  const [state, setState] = useState<FeedState>(() => blank(key, leaseID !== undefined));
  const [tick, setTick] = useState(0);
  // Incremented on every lease/scope change and on unmount, so a slow response
  // for lease A can never land in lease B's feed.
  const generation = useRef(0);
  const older = useRef<() => void>(NOOP);

  useEffect(() => {
    generation.current += 1;
    const gen = generation.current;
    const alive = () => generation.current === gen;

    if (leaseID === undefined) {
      setState((prev) => (prev.key === key ? prev : blank(key, false)));
      return;
    }

    // The accumulator lives in the effect, not in state: page cursors must read
    // what has already landed without waiting for a render.
    let held: StewardEvent[] = EMPTY;
    let ready = false;
    let timer = 0;
    // Guards are per-generation locals, not refs: a superseded lease's request
    // settling late must not unlock the current lease's request slot.
    let paging = false;
    let polling = false;

    const publish = (patch: Partial<FeedState>) => {
      if (!alive()) return;
      setState((prev) => {
        if (prev.key !== key) return prev;
        const next = { ...prev, events: held, ...patch };
        return unchanged(prev, next) ? prev : next;
      });
    };

    const fetchPage = async (cursor: number | undefined): Promise<number> => {
      const batch = await api.eventsQuery(
        cursor === undefined
          ? { leaseID, scope, limit: pageSize }
          : { leaseID, scope, beforeID: cursor, limit: pageSize },
      );
      if (!alive()) return -1;
      held = mergeEvents(held, batch);
      return batch.length;
    };

    // Newest page first, then walk backwards. Backfill is automatic so the
    // operator reads the whole transcript without clicking for it.
    const load = async () => {
      if (paging) return;
      paging = true;
      try {
        let cursor: number | undefined = undefined;
        for (let page = 1; ; page++) {
          const size = await fetchPage(cursor);
          if (size < 0) return;
          const whole = size < pageSize;
          const room = page < maxBackfillPages;
          publish({
            loading: false,
            loadingMore: false,
            complete: whole,
            hasMore: !whole && !room,
            error: undefined,
          });
          if (whole || !room) return;
          cursor = oldestID(held);
          if (cursor === undefined) return;
        }
      } catch (cause) {
        publish({ loading: false, loadingMore: false, error: cause });
      } finally {
        paging = false;
        ready = true;
      }
    };

    const loadOlder = async () => {
      if (paging) return;
      const cursor = oldestID(held);
      if (cursor === undefined) return;
      paging = true;
      publish({ loadingMore: true });
      try {
        const size = await fetchPage(cursor);
        if (size < 0) return;
        const whole = size < pageSize;
        publish({ loadingMore: false, complete: whole, hasMore: !whole, error: undefined });
      } catch (cause) {
        publish({ loadingMore: false, error: cause });
      } finally {
        paging = false;
      }
    };

    // Forward poll. An empty feed re-reads the newest page instead of skipping:
    // a lease that has emitted nothing yet must still come alive on its first
    // event. Either way the result is merged, never substituted.
    const poll = async () => {
      if (!ready || polling || document.hidden) return;
      polling = true;
      try {
        const after = newestID(held);
        const batch = await api.eventsQuery(
          after === undefined
            ? { leaseID, scope, limit: pageSize }
            : { leaseID, scope, afterID: after, limit: pageSize },
        );
        if (!alive()) return;
        held = mergeEvents(held, batch);
        publish({ error: undefined });
      } catch (cause) {
        publish({ error: cause });
      } finally {
        polling = false;
      }
    };

    const loop = async () => {
      await poll();
      if (alive()) timer = window.setTimeout(() => void loop(), intervalMs);
    };

    // Catch up the moment the tab comes back rather than waiting out a hidden
    // interval that was deliberately skipped.
    const onVisibility = () => {
      if (!document.hidden) void poll();
    };

    setState((prev) => {
      const reset = blank(key, true);
      return prev.key === key && unchanged(prev, reset) ? prev : reset;
    });
    older.current = () => void loadOlder();
    document.addEventListener('visibilitychange', onVisibility);
    timer = window.setTimeout(() => void loop(), intervalMs);
    void load();

    return () => {
      generation.current += 1;
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', onVisibility);
      older.current = NOOP;
    };
  }, [key, leaseID, scope, pageSize, intervalMs, maxBackfillPages, tick]);

  const loadOlder = useCallback(() => older.current(), []);
  const refresh = useCallback(() => setTick((value) => value + 1), []);

  // A lease change is visible in props one render before the effect resets
  // state. Report the new lease as empty and loading rather than showing the
  // previous lease's rows for a frame.
  const stale = state.key !== key;
  return {
    events: stale ? EMPTY : state.events,
    loading: stale ? leaseID !== undefined : state.loading,
    loadingMore: stale ? false : state.loadingMore,
    hasMore: stale ? false : state.hasMore,
    error: stale ? undefined : state.error,
    complete: stale ? false : state.complete,
    loadOlder,
    refresh,
  };
}
