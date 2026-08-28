// Persona identity and inference routing are configured independently. The
// controller never stores a route on a persona: a route reaches a persona only
// through the lease the scheduler opens for it. This module performs the
// read-only join the operator UI shows, and always reports how it resolved so
// the presentation is never mistaken for ownership.

import type { Lease, ModelProfile, Status } from '../api/types';

/** Shown wherever the joined assignment appears. Kept in one place so the two
 *  surfaces that display it cannot drift apart. */
export const ASSIGNMENT_JOIN_NOTE =
  'Routing is configured independently of persona identity. This provider/model pairing is joined ' +
  'here for presentation only: it comes from the lease or the default route, never from the persona record.';

export interface EffectiveAssignment {
  profileName: string;
  provider: string;
  model: string;
  /** Effective reasoning variant, not merely the route default. */
  thinking: string;
  cacheImpact: string;
  billingMode: string;
  endpoint?: string;
  /**
   * How this was resolved, shown to the operator so the join is never mistaken
   * for ownership. `latest_lease` means this persona's most recent historical
   * lease supplied the route; `default_route` means nothing had ever leased
   * this persona and the configured default was reported instead.
   */
  source: 'active_lease' | 'latest_lease' | 'default_route' | 'unknown';
}

const UNKNOWN = 'unknown';

/**
 * Resolve the route a persona is running on, or would run on next.
 *
 * Resolution order, each step reported distinctly so the operator can tell a
 * live binding from a historical one from a config default:
 *   1. the active lease, when it belongs to this persona -> `active_lease`
 *   2. this persona's most recent lease in `recentLeases` -> `latest_lease`
 *   3. the configured default (`selected`) profile -> `default_route`
 *   4. nothing resolvable -> `unknown`
 *
 * Pure and total: unresolvable inputs produce `source: 'unknown'` with whatever
 * raw profile name was seen, never an exception.
 */
export function effectiveAssignment(args: {
  personaID: string;
  status?: Status;
  profiles: ModelProfile[];
  recentLeases?: Lease[];
}): EffectiveAssignment {
  const { personaID, status, profiles, recentLeases } = args;
  const active = status?.lease;

  let name = '';
  let thinking = '';
  let source: EffectiveAssignment['source'];

  if (active && active.persona_id === personaID) {
    name = active.model_profile ?? '';
    thinking = status?.reasoning?.effective || active.thinking || '';
    source = 'active_lease';
  } else {
    const historical = latestLease(recentLeases, personaID);
    if (historical) {
      name = historical.model_profile ?? '';
      thinking = historical.thinking || '';
      source = 'latest_lease';
    } else {
      const selected = profiles.find((profile) => profile.selected);
      name = selected ? selected.name : '';
      source = selected ? 'default_route' : 'unknown';
    }
  }

  const profile = name ? profiles.find((candidate) => candidate.name === name) : undefined;
  if (!profile) {
    return {
      profileName: name,
      provider: UNKNOWN,
      model: UNKNOWN,
      thinking: thinking || UNKNOWN,
      cacheImpact: UNKNOWN,
      billingMode: UNKNOWN,
      source: 'unknown',
    };
  }

  return {
    profileName: profile.name,
    provider: profile.provider || UNKNOWN,
    model: profile.model || UNKNOWN,
    thinking: thinking || profile.thinking?.default || UNKNOWN,
    cacheImpact: profile.thinking?.cache_impact || UNKNOWN,
    billingMode: profile.billing?.mode || UNKNOWN,
    endpoint: profile.endpoint,
    source,
  };
}

/** Human wording for `EffectiveAssignment.source`. */
export function assignmentSourceLabel(source: EffectiveAssignment['source']): string {
  switch (source) {
    case 'active_lease':
      return 'from active lease';
    case 'latest_lease':
      return 'from latest lease';
    case 'default_route':
      return 'config default route';
    default:
      return 'unresolved route';
  }
}

/** Host portion of a route endpoint, falling back to the raw value. */
export function endpointHost(endpoint: string | undefined): string {
  if (!endpoint) return '';
  try {
    return new URL(endpoint).host || endpoint;
  } catch {
    return endpoint;
  }
}

function latestLease(leases: Lease[] | undefined, personaID: string): Lease | undefined {
  if (!leases || leases.length === 0) return undefined;
  let best: Lease | undefined;
  let bestAt = Number.NEGATIVE_INFINITY;
  for (const lease of leases) {
    if (lease.persona_id !== personaID || !lease.model_profile) continue;
    const at = Date.parse(lease.started_at);
    const rank = Number.isNaN(at) ? Number.NEGATIVE_INFINITY : at;
    if (best === undefined || rank > bestAt) {
      best = lease;
      bestAt = rank;
    }
  }
  return best;
}
