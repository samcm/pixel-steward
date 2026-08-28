// Operator token storage for controllers running `http.auth.mode: bearer`.
// In that mode every /api/v1/* route (including /api/v1/objects) answers
// HTTP 401 `unauthorized` unless the request carries
// `Authorization: Bearer <token>` — see the operator() middleware and bearer()
// helper in internal/api/server.go.
//
// Deployments with `mode: disabled` store no token, so `authHeaders()` returns
// undefined and every request goes out exactly as it did before this module
// existed.
//
// The token is a credential. It is never logged, never placed in a URL, never
// rendered in full, and never attached to anything but a same-origin operator
// path.

/** Where the operator token lives for this browser session. */
export type TokenScope = 'session' | 'local';

export interface TokenStore {
  get(): string | undefined;
  set(token: string, scope: TokenScope): void;
  clear(): void;
  scope(): TokenScope | undefined;
  subscribe(listener: () => void): () => void;
}

/** The slice of the Web Storage API this module uses. */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** Resolves a backing store, or undefined when the browser denies access. */
export type StorageProvider = (scope: TokenScope) => StorageLike | undefined;

/**
 * Registers an out-of-band change notifier (the cross-tab `storage` event) and
 * returns its disposer. Injected so the store is testable without a DOM.
 */
export type ChangeWatcher = (onChange: () => void) => () => void;

export const TOKEN_STORAGE_KEY = 'pixel-steward.operator-token';

const SCOPES: readonly TokenScope[] = ['session', 'local'];

/**
 * Builds a token store over injectable storage.
 *
 * Reads prefer `session` over `local` so a tab-scoped token always wins over a
 * remembered one. Writes are single-homed: setting one scope removes the other.
 * Every storage call is guarded — `sessionStorage` throws outright in some
 * privacy modes — and a store that cannot persist degrades to an in-memory
 * value for the life of the page instead of failing.
 */
export function createTokenStore(provider: StorageProvider, watch?: ChangeWatcher): TokenStore {
  let memory: string | undefined;
  let memoryScope: TokenScope | undefined;

  const listeners = new Set<() => void>();
  let unwatch: (() => void) | undefined;

  const notify = () => {
    for (const listener of Array.from(listeners)) listener();
  };

  const storageFor = (scope: TokenScope): StorageLike | undefined => {
    try {
      return provider(scope) ?? undefined;
    } catch {
      return undefined;
    }
  };

  const readFrom = (scope: TokenScope): string | undefined => {
    const storage = storageFor(scope);
    if (!storage) return undefined;
    try {
      return storage.getItem(TOKEN_STORAGE_KEY) ?? undefined;
    } catch {
      return undefined;
    }
  };

  const removeFrom = (scope: TokenScope) => {
    const storage = storageFor(scope);
    if (!storage) return;
    try {
      storage.removeItem(TOKEN_STORAGE_KEY);
    } catch {
      /* nothing to do: the value was never persisted */
    }
  };

  // Returns true only once the value is proven readable again. Some privacy
  // modes accept setItem and store nothing; a silent success there would leave
  // the token in memory only, which must not look like a durable write.
  const writeTo = (scope: TokenScope, token: string): boolean => {
    const storage = storageFor(scope);
    if (!storage) return false;
    try {
      storage.setItem(TOKEN_STORAGE_KEY, token);
      return storage.getItem(TOKEN_STORAGE_KEY) === token;
    } catch {
      return false;
    }
  };

  const clear = () => {
    memory = undefined;
    memoryScope = undefined;
    for (const scope of SCOPES) removeFrom(scope);
    notify();
  };

  return {
    get(): string | undefined {
      // Storage is authoritative when it works. The in-memory value is only a
      // fallback for a store that refused the write, so clearing the token in
      // another tab genuinely signs this one out.
      return readFrom('session') ?? readFrom('local') ?? memory;
    },

    scope(): TokenScope | undefined {
      for (const scope of SCOPES) {
        if (readFrom(scope) !== undefined) return scope;
      }
      return memory === undefined ? undefined : memoryScope;
    },

    set(token: string, scope: TokenScope): void {
      const value = token.trim();
      if (value === '') {
        clear();
        return;
      }
      for (const other of SCOPES) {
        if (other !== scope) removeFrom(other);
      }
      const persisted = writeTo(scope, value);
      memory = persisted ? undefined : value;
      memoryScope = persisted ? undefined : scope;
      notify();
    },

    clear,

    subscribe(listener: () => void): () => void {
      listeners.add(listener);
      if (watch && listeners.size === 1) unwatch = watch(notify);
      return () => {
        listeners.delete(listener);
        if (unwatch && listeners.size === 0) {
          unwatch();
          unwatch = undefined;
        }
      };
    },
  };
}

function browserStorage(scope: TokenScope): StorageLike | undefined {
  // Property access itself throws a SecurityError in some privacy modes, and
  // is undefined under the node test environment.
  try {
    return (scope === 'session' ? globalThis.sessionStorage : globalThis.localStorage) ?? undefined;
  } catch {
    return undefined;
  }
}

function browserWatch(onChange: () => void): () => void {
  if (typeof globalThis.addEventListener !== 'function') return () => undefined;
  const handler = (event: Event) => {
    // key === null is a whole-store clear(); both cases may have dropped ours.
    const key = (event as StorageEvent).key;
    if (key === null || key === TOKEN_STORAGE_KEY) onChange();
  };
  globalThis.addEventListener('storage', handler);
  return () => globalThis.removeEventListener('storage', handler);
}

export const tokenStore: TokenStore = createTokenStore(browserStorage, browserWatch);

/** Authorization header for same-origin operator requests, or undefined. */
export function authHeaders(): Record<string, string> | undefined {
  const token = tokenStore.get();
  return token ? { Authorization: `Bearer ${token}` } : undefined;
}

const OPERATOR_PREFIX = '/api/v1/';

/**
 * True for paths the operator token may be attached to.
 *
 * Deliberately paranoid: the token must never reach a third-party origin, so
 * anything that could carry an authority — an absolute URL, a protocol-relative
 * `//host/...`, a backslash variant browsers normalise into one — is rejected,
 * as is any relative path, since the caller's base is unknown here. Only a
 * rooted, single-slash path inside /api/v1/ qualifies.
 */
export function isOperatorPath(path: string): boolean {
  if (typeof path !== 'string' || path.length < OPERATOR_PREFIX.length) return false;
  if (path.charCodeAt(0) !== 47 /* '/' */) return false;
  if (path.charCodeAt(1) === 47) return false; // protocol-relative //host/...
  // Browsers fold backslashes into slashes, so /\host/ is another authority
  // spelling; control characters have no business in a path either.
  if (/[\\\u0000-\u001f\u007f]/.test(path)) return false;

  const stop = path.search(/[?#]/);
  const head = stop === -1 ? path : path.slice(0, stop);
  if (!head.startsWith(OPERATOR_PREFIX)) return false;

  // A traversal segment resolves somewhere other than the operator namespace,
  // percent-encoded or not; a path we cannot even decode is not one we vouch for.
  for (const segment of head.split('/')) {
    if (segment === '..') return false;
    if (!segment.includes('%')) continue;
    try {
      if (decodeURIComponent(segment) === '..') return false;
    } catch {
      return false;
    }
  }
  return true;
}
