import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { ApiError } from './client';

export interface Poll<T> {
  /** Last successful payload for the CURRENT query. Kept while later fetches fail. */
  data: T | undefined;
  /** Error from the most recent attempt, or undefined when it succeeded. */
  error: ApiError | undefined;
  /** True while the current query has neither data nor an error yet. */
  loading: boolean;
  /** Wall-clock of the last successful load. */
  updatedAt: number | undefined;
  /** Consecutive failures since the last success. */
  failures: number;
  refresh: () => void;
}

/** Stable identity for a dependency list, used to detect a query change. */
export function depsKey(deps: readonly unknown[]): string {
  return deps.map((value) => (typeof value === 'object' ? JSON.stringify(value) : String(value))).join('\u0000');
}

interface PollState<T> {
  /** Query generation this payload belongs to. */
  generation: number;
  data?: T;
  error?: ApiError;
  updatedAt?: number;
  failures: number;
}

/**
 * Folds one settled request into the poll state. A response tagged with a
 * superseded generation is discarded, so a slow answer for the previous journal
 * filter can never be presented as the current one. A failure of the CURRENT
 * query keeps the last good payload.
 */
export function applyPollResult<T>(
  current: PollState<T>,
  generation: number,
  outcome: { data: T } | { error: ApiError },
  at: number,
): PollState<T> {
  if (current.generation > generation) return current;
  if ('data' in outcome) {
    return { generation, data: outcome.data, error: undefined, updatedAt: at, failures: 0 };
  }
  const sameQuery = current.generation === generation;
  return {
    generation,
    data: sameQuery ? current.data : undefined,
    error: outcome.error,
    updatedAt: sameQuery ? current.updatedAt : undefined,
    failures: (sameQuery ? current.failures : 0) + 1,
  };
}

/**
 * Polling that keeps good data across transient failures of the SAME query, and
 * discards results from a superseded query. Changing the dependencies clears the
 * previous payload immediately, so a slow response for the old journal filter can
 * never be presented as the new one.
 */
export function usePoll<T>(fetcher: () => Promise<T>, intervalMs: number, deps: readonly unknown[] = []): Poll<T> {
  const key = depsKey(deps);
  const [tick, setTick] = useState(0);
  const [state, setState] = useState<PollState<T>>({ generation: 0, failures: 0 });

  // Generation increments on every query change, so an in-flight response can be
  // matched against the query that asked for it.
  const generation = useMemo(() => ({ current: 0 }), []);
  const previousKey = useRef(key);
  if (previousKey.current !== key) {
    previousKey.current = key;
    generation.current += 1;
  }
  const active = generation.current;

  const inflight = useRef(false);
  const call = useRef(fetcher);
  call.current = fetcher;

  const refresh = useCallback(() => setTick((value) => value + 1), []);

  useEffect(() => {
    let alive = true;
    let timer = 0;

    const run = async () => {
      if (inflight.current) {
        timer = window.setTimeout(run, intervalMs);
        return;
      }
      if (document.hidden) {
        timer = window.setTimeout(run, intervalMs);
        return;
      }
      inflight.current = true;
      try {
        const value = await call.current();
        if (!alive) return;
        setState((current) => applyPollResult(current, active, { data: value }, Date.now()));
      } catch (cause) {
        if (!alive) return;
        const failure = cause instanceof ApiError ? cause : new ApiError(String(cause), 0);
        setState((current) => applyPollResult(current, active, { error: failure }, Date.now()));
      } finally {
        inflight.current = false;
        if (alive) timer = window.setTimeout(run, intervalMs);
      }
    };

    // A hidden tab stops polling; returning to it must refresh at once rather
    // than showing data from before the tab was backgrounded.
    const onVisible = () => {
      if (document.hidden || !alive) return;
      window.clearTimeout(timer);
      void run();
    };
    document.addEventListener('visibilitychange', onVisible);

    void run();
    return () => {
      alive = false;
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, tick, key, active]);

  const current = state.generation === active ? state : undefined;
  return {
    data: current?.data,
    error: current?.error,
    loading: current?.data === undefined && current?.error === undefined,
    updatedAt: current?.updatedAt,
    failures: current?.failures ?? 0,
    refresh,
  };
}

/** One-shot loader for drawer/dialog content. */
export function useAsync<T>(fetcher: () => Promise<T>, deps: readonly unknown[]): Poll<T> {
  const key = depsKey(deps);
  const [state, setState] = useState<{ key: string; data?: T; error?: ApiError; updatedAt?: number }>({ key });
  const [tick, setTick] = useState(0);
  const refresh = useCallback(() => setTick((value) => value + 1), []);
  const call = useRef(fetcher);
  call.current = fetcher;

  useEffect(() => {
    let alive = true;
    setState({ key });
    void (async () => {
      try {
        const value = await call.current();
        if (alive) setState({ key, data: value, updatedAt: Date.now() });
      } catch (cause) {
        if (alive) setState({ key, error: cause instanceof ApiError ? cause : new ApiError(String(cause), 0) });
      }
    })();
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tick, key]);

  const current = state.key === key ? state : undefined;
  return {
    data: current?.data,
    error: current?.error,
    loading: current?.data === undefined && current?.error === undefined,
    updatedAt: current?.updatedAt,
    failures: current?.error ? 1 : 0,
    refresh,
  };
}
