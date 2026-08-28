import { afterEach, describe, expect, it } from 'vitest';
import type { StorageLike, TokenScope } from './auth';
import { authHeaders, createTokenStore, isOperatorPath, TOKEN_STORAGE_KEY, tokenStore } from './auth';

interface Fake extends StorageLike {
  entries: Map<string, string>;
}

function fakeStorage(seed?: string): Fake {
  const entries = new Map<string, string>();
  if (seed !== undefined) entries.set(TOKEN_STORAGE_KEY, seed);
  return {
    entries,
    getItem: (key) => entries.get(key) ?? null,
    setItem: (key, value) => void entries.set(key, value),
    removeItem: (key) => void entries.delete(key),
  };
}

const hostile: Fake = {
  entries: new Map(),
  getItem: () => {
    throw new Error('storage denied');
  },
  setItem: () => {
    throw new Error('storage denied');
  },
  removeItem: () => {
    throw new Error('storage denied');
  },
};

function storeOver(session: StorageLike | undefined, local: StorageLike | undefined) {
  return createTokenStore((scope: TokenScope) => (scope === 'session' ? session : local));
}

// The module singleton has no storage under environment: 'node', so it falls
// back to memory. Reset it so the header assertions cannot leak between tests.
afterEach(() => tokenStore.clear());

describe('token store precedence', () => {
  it('prefers a session token over a remembered one', () => {
    const store = storeOver(fakeStorage('tab-token'), fakeStorage('device-token'));

    expect(store.get()).toBe('tab-token');
    expect(store.scope()).toBe('session');
  });

  it('falls through to the remembered token when no tab token exists', () => {
    const store = storeOver(fakeStorage(), fakeStorage('device-token'));

    expect(store.get()).toBe('device-token');
    expect(store.scope()).toBe('local');
  });

  it('single-homes a write so switching scope does not leave a shadow copy', () => {
    const session = fakeStorage();
    const local = fakeStorage();
    const store = storeOver(session, local);

    store.set('first', 'session');
    store.set('second', 'local');

    expect(session.entries.size).toBe(0);
    expect(local.entries.get(TOKEN_STORAGE_KEY)).toBe('second');
    expect(store.get()).toBe('second');
    expect(store.scope()).toBe('local');
  });

  it('writes under the documented key', () => {
    const session = fakeStorage();
    storeOver(session, fakeStorage()).set('written', 'session');

    expect(session.entries.get('pixel-steward.operator-token')).toBe('written');
  });

  it('treats a blank token as a clear', () => {
    const session = fakeStorage('tab-token');
    const store = storeOver(session, fakeStorage());

    store.set('   ', 'session');

    expect(store.get()).toBeUndefined();
    expect(session.entries.size).toBe(0);
  });

  it('trims a pasted token', () => {
    const store = storeOver(fakeStorage(), fakeStorage());
    store.set('  padded\n', 'session');

    expect(store.get()).toBe('padded');
  });
});

describe('token store clear', () => {
  it('empties both scopes', () => {
    const session = fakeStorage('tab-token');
    const local = fakeStorage('device-token');
    const store = storeOver(session, local);

    store.clear();

    expect(session.entries.size).toBe(0);
    expect(local.entries.size).toBe(0);
    expect(store.get()).toBeUndefined();
    expect(store.scope()).toBeUndefined();
  });

  it('forgets a memory-only token so another tab clearing storage signs this one out', () => {
    const session = fakeStorage();
    const store = storeOver(session, fakeStorage());
    store.set('tab-token', 'session');

    // Stands in for a cross-tab removal: storage dropped it behind our back.
    session.entries.clear();

    expect(store.get()).toBeUndefined();
  });
});

describe('token store without usable storage', () => {
  it('keeps the token in memory instead of throwing', () => {
    const store = storeOver(hostile, hostile);

    expect(() => store.set('memory-token', 'session')).not.toThrow();
    expect(store.get()).toBe('memory-token');
    expect(store.scope()).toBe('session');
    expect(() => store.clear()).not.toThrow();
    expect(store.get()).toBeUndefined();
  });

  it('survives a provider that throws on access', () => {
    const store = createTokenStore(() => {
      throw new Error('sessionStorage is not available');
    });

    expect(() => store.set('memory-token', 'local')).not.toThrow();
    expect(store.get()).toBe('memory-token');
    expect(store.scope()).toBe('local');
  });

  it('survives an absent provider result', () => {
    const store = storeOver(undefined, undefined);

    store.set('memory-token', 'session');
    expect(store.get()).toBe('memory-token');
  });
});

describe('token store subscribers', () => {
  it('fires on set and clear, and stops after unsubscribe', () => {
    const store = storeOver(fakeStorage(), fakeStorage());
    let calls = 0;
    const stop = store.subscribe(() => void (calls += 1));

    store.set('tab-token', 'session');
    expect(calls).toBe(1);

    store.clear();
    expect(calls).toBe(2);

    stop();
    store.set('again', 'session');
    expect(calls).toBe(2);
  });

  it('attaches the cross-tab watcher on the first subscriber and drops it with the last', () => {
    let watching = 0;
    let external: (() => void) | undefined;
    const store = createTokenStore(
      () => undefined,
      (onChange) => {
        watching += 1;
        external = onChange;
        return () => void (watching -= 1);
      },
    );

    expect(watching).toBe(0);

    let calls = 0;
    const first = store.subscribe(() => void (calls += 1));
    const second = store.subscribe(() => void (calls += 1));
    expect(watching).toBe(1);

    external?.();
    expect(calls).toBe(2);

    first();
    expect(watching).toBe(1);
    second();
    expect(watching).toBe(0);
  });
});

describe('authHeaders', () => {
  it('returns nothing while no token is stored', () => {
    expect(authHeaders()).toBeUndefined();
  });

  it('produces a bearer header for a stored token', () => {
    tokenStore.set('operator', 'session');

    expect(authHeaders()).toEqual({ Authorization: 'Bearer operator' });
  });

  it('stops producing a header once the token is cleared', () => {
    tokenStore.set('operator', 'session');
    tokenStore.clear();

    expect(authHeaders()).toBeUndefined();
  });
});

describe('isOperatorPath', () => {
  it('accepts same-origin operator paths', () => {
    expect(isOperatorPath('/api/v1/status')).toBe(true);
    expect(isOperatorPath('/api/v1/objects?key=x')).toBe(true);
    expect(isOperatorPath('/api/v1/events?limit=100&scope=transcript')).toBe(true);
    expect(isOperatorPath('/api/v1/personas/studio')).toBe(true);
  });

  it('rejects anything that could carry a foreign authority', () => {
    expect(isOperatorPath('https://example.test/api/v1/status')).toBe(false);
    expect(isOperatorPath('http://example.test/api/v1/status')).toBe(false);
    expect(isOperatorPath('//example.test/api/v1/x')).toBe(false);
    expect(isOperatorPath('/\\example.test/api/v1/x')).toBe(false);
    expect(isOperatorPath('\\\\example.test/api/v1/x')).toBe(false);
  });

  it('rejects relative paths, other namespaces and traversal', () => {
    expect(isOperatorPath('../api/v1/status')).toBe(false);
    expect(isOperatorPath('api/v1/status')).toBe(false);
    expect(isOperatorPath('/agent/v1/budget')).toBe(false);
    expect(isOperatorPath('/api/v2/status')).toBe(false);
    expect(isOperatorPath('/API/v1/status')).toBe(false);
    expect(isOperatorPath('/api/v1/../../agent/v1/budget')).toBe(false);
    expect(isOperatorPath('/api/v1/%2e%2e/agent/v1/budget')).toBe(false);
    expect(isOperatorPath('/api/v1/%zz')).toBe(false);
  });

  it('rejects empty, short and control-character paths', () => {
    expect(isOperatorPath('')).toBe(false);
    expect(isOperatorPath('/')).toBe(false);
    expect(isOperatorPath('/api/v1')).toBe(false);
    expect(isOperatorPath('/api/v1/status\n')).toBe(false);
    expect(isOperatorPath(undefined as unknown as string)).toBe(false);
  });
});
