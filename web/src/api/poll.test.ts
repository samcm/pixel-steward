import { describe, expect, it } from 'vitest';
import { ApiError } from './client';
import { applyPollResult, depsKey } from './poll';

const empty = { generation: 0, failures: 0 } as const;

describe('applyPollResult', () => {
  it('keeps the last good payload when the same query fails transiently', () => {
    const loaded = applyPollResult(empty, 0, { data: ['a'] }, 1000);
    const failed = applyPollResult(loaded, 0, { error: new ApiError('gateway', 502) }, 2000);

    expect(failed.data).toEqual(['a']);
    expect(failed.error?.status).toBe(502);
    expect(failed.failures).toBe(1);
    expect(failed.updatedAt).toBe(1000);
  });

  it('discards a slow response from a superseded query generation', () => {
    const current = applyPollResult(empty, 1, { data: ['new filter'] }, 2000);
    const stale = applyPollResult(current, 0, { data: ['old filter'] }, 2100);

    expect(stale.data).toEqual(['new filter']);
    expect(stale.generation).toBe(1);
  });

  it('does not carry the previous query payload into a new generation failure', () => {
    const loaded = applyPollResult(empty, 0, { data: ['persona a'] }, 1000);
    const failed = applyPollResult(loaded, 1, { error: new ApiError('not found', 404) }, 2000);

    expect(failed.data).toBeUndefined();
    expect(failed.failures).toBe(1);
  });

  it('counts consecutive failures and resets them on success', () => {
    let state = applyPollResult(empty, 0, { error: new ApiError('down', 0) }, 1);
    state = applyPollResult(state, 0, { error: new ApiError('down', 0) }, 2);
    expect(state.failures).toBe(2);

    state = applyPollResult(state, 0, { data: ['back'] }, 3);
    expect(state.failures).toBe(0);
    expect(state.error).toBeUndefined();
  });
});

describe('depsKey', () => {
  it('separates queries that differ only by a dependency value', () => {
    expect(depsKey(['atlas'])).not.toBe(depsKey(['borealis']));
  });

  it('treats an empty filter and an absent filter as the same query', () => {
    expect(depsKey([''])).toBe(depsKey(['']));
  });

  it('does not collide across dependency boundaries', () => {
    expect(depsKey(['a', 'bc'])).not.toBe(depsKey(['ab', 'c']));
  });
});
