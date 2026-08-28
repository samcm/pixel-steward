// Single place where operator HTTP behaviour lives: JSON decoding, error
// normalisation, operator-token attachment and object URLs. Nothing else in the
// UI calls fetch directly.

import { authHeaders, isOperatorPath, tokenStore } from './auth';
import type {
  Frame,
  InferenceRequest,
  JournalEntry,
  Lease,
  ModelProfile,
  Persona,
  PersonaDetail,
  Status,
  StewardEvent,
} from './types';

export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

const unauthorizedListeners = new Set<() => void>();

/**
 * Fires when the controller rejects an operator request. The app raises the
 * token gate from this; a deployment with auth disabled never triggers it.
 */
export function onUnauthorized(listener: () => void): () => void {
  unauthorizedListeners.add(listener);
  return () => unauthorizedListeners.delete(listener);
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const auth = isOperatorPath(path) ? authHeaders() : undefined;
  const headers = auth ? { ...(init?.headers as Record<string, string> | undefined), ...auth } : init?.headers;
  let response: Response;
  try {
    response = await fetch(path, headers ? { ...init, headers } : init);
  } catch (cause) {
    throw new ApiError(cause instanceof Error ? cause.message : 'network unreachable', 0);
  }
  const body = await response.text();
  if (!response.ok) {
    let message = body.trim() || response.statusText;
    try {
      const parsed = JSON.parse(body) as { error?: string };
      if (parsed?.error) message = parsed.error;
    } catch {
      /* body was not JSON */
    }
    if (response.status === 401) {
      for (const listener of unauthorizedListeners) listener();
    }
    throw new ApiError(message, response.status);
  }
  return (body ? JSON.parse(body) : null) as T;
}

function query(params: Record<string, string | number | undefined>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') search.set(key, String(value));
  }
  const text = search.toString();
  return text ? `?${text}` : '';
}

export interface EventQueryParams {
  leaseID?: string;
  personaID?: string;
  /** `transcript` drops per-frame renderer telemetry server-side. */
  scope?: 'all' | 'transcript';
  /** Exclusive lower bound; ascending results. */
  afterID?: number;
  /** Exclusive upper bound; descending results, for paging back through history. */
  beforeID?: number;
  limit?: number;
}

function normalisePersonaDetail(detail: PersonaDetail): PersonaDetail {
  return {
    ...detail,
    leases: Array.isArray(detail.leases) ? detail.leases : [],
    events: Array.isArray(detail.events) ? detail.events : [],
    prompts: Array.isArray(detail.prompts) ? detail.prompts : [],
    transcript: Array.isArray(detail.transcript) ? detail.transcript : [],
    frames: Array.isArray(detail.frames) ? detail.frames : [],
    journal: Array.isArray(detail.journal) ? detail.journal : [],
    inference: Array.isArray(detail.inference) ? detail.inference : [],
    schedules: Array.isArray(detail.schedules) ? detail.schedules : [],
  };
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  status: () => request<Status>('/api/v1/status'),
  personas: () => request<Persona[]>('/api/v1/personas'),
  personaDetail: (id: string) =>
    request<PersonaDetail>(`/api/v1/personas/${encodeURIComponent(id)}`).then(normalisePersonaDetail),
  modelProfiles: () => request<ModelProfile[]>('/api/v1/model-profiles'),
  events: (limit = 200) => request<StewardEvent[]>(`/api/v1/events${query({ limit })}`),
  eventsQuery: (params: EventQueryParams) =>
    request<StewardEvent[]>(
      `/api/v1/events${query({
        lease_id: params.leaseID,
        persona_id: params.personaID,
        scope: params.scope,
        after_id: params.afterID,
        before_id: params.beforeID,
        limit: params.limit,
      })}`,
    ),
  journal: (limit = 50, personaID?: string) =>
    request<JournalEntry[]>(`/api/v1/journal${query({ limit, persona_id: personaID })}`),
  frames: (limit = 40, leaseID?: string) => request<Frame[]>(`/api/v1/frames${query({ limit, lease_id: leaseID })}`),
  inference: (limit = 60, leaseID?: string) =>
    request<InferenceRequest[]>(`/api/v1/inference${query({ limit, lease_id: leaseID })}`),
  leases: (limit = 40) => request<Lease[]>(`/api/v1/leases${query({ limit })}`),
  setPersonaEnabled: (id: string, enabled: boolean) =>
    request<{ enabled: boolean }>(`/api/v1/personas/${encodeURIComponent(id)}/enabled`, {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ enabled }),
    }),
  revokeLease: () => request<{ revoked: boolean }>('/api/v1/lease/revoke', { method: 'POST' }),
  setReasoning: (value: string) =>
    request<{ effective: string }>('/api/v1/lease/reasoning', {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ value }),
    }),
};

const OBJECT_COOKIE = 'pixel_steward_operator';

/**
 * <img> cannot carry an Authorization header, so in bearer mode the token is
 * mirrored into a SameSite=Strict cookie scoped to the objects route. The
 * controller accepts either. SameSite=Strict is what keeps the cookie from
 * being usable for cross-site request forgery. With auth disabled no cookie is
 * ever written.
 */
function syncObjectCookie(): void {
  const token = tokenStore.get();
  if (!token) return;
  try {
    document.cookie = `${OBJECT_COOKIE}=${encodeURIComponent(token)}; Path=/api/v1/objects; SameSite=Strict; Max-Age=86400`;
  } catch {
    /* cookies unavailable; header-authenticated fetches still work */
  }
}

export function clearObjectCookie(): void {
  try {
    document.cookie = `${OBJECT_COOKIE}=; Path=/api/v1/objects; SameSite=Strict; Max-Age=0`;
  } catch {
    /* nothing to clear */
  }
}

// Signing out, or another tab clearing the token, must also drop the cookie
// mirror or <img> loads would keep authenticating after the operator left.
tokenStore.subscribe(() => {
  if (!tokenStore.get()) clearObjectCookie();
});

export function objectURL(key: string): string {
  syncObjectCookie();
  return `/api/v1/objects?key=${encodeURIComponent(key)}`;
}
