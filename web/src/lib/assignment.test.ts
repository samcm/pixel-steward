import { describe, expect, it } from 'vitest';
import type { Lease, ModelProfile, Status } from '../api/types';
import { assignmentSourceLabel, effectiveAssignment } from './assignment';

const NOW = Date.parse('2026-08-29T12:00:00.000Z');
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

function profileOf(name: string, overrides: Partial<ModelProfile> = {}): ModelProfile {
  return {
    name,
    provider: `${name}-provider`,
    model: `${name}-model`,
    endpoint: 'https://models.example.com/v1',
    thinking: { default: 'medium', allowed: ['off', 'medium', 'high'], cache_impact: 'preserved' },
    billing: { mode: 'subscription' },
    selected: false,
    ...overrides,
  };
}

function leaseOf(personaID: string, profile: string, startedAtOffsetMs: number, thinking: string): Lease {
  return {
    id: `lease-${personaID}-${startedAtOffsetMs}`,
    persona_id: personaID,
    model_profile: profile,
    thinking,
    started_at: iso(startedAtOffsetMs),
    ends_at: iso(startedAtOffsetMs + 900_000),
    status: 'ended',
  };
}

function statusOf(lease?: Lease, effectiveThinking = 'high'): Status {
  return {
    as_of: iso(0),
    blackout: false,
    scheduled_blackout: false,
    next_transition: iso(3_600_000),
    lease,
    display: { online: true, screen_on: true, frames: 0, skipped: 0 },
    display_armed: true,
    agent_running: lease !== undefined,
    reasoning: { effective: effectiveThinking, source: 'lease', allowed: ['off', 'high'], cache_impact: 'preserved' },
  };
}

const PROFILES: ModelProfile[] = [
  profileOf('careful'),
  profileOf('quick', { selected: true }),
  profileOf('archived'),
];

describe('effectiveAssignment provenance', () => {
  it('reports the active lease, outranking history and the default route', () => {
    const active = leaseOf('painter', 'careful', -60_000, 'off');
    const assignment = effectiveAssignment({
      personaID: 'painter',
      status: statusOf(active),
      profiles: PROFILES,
      recentLeases: [active, leaseOf('painter', 'archived', -7_200_000, 'medium')],
    });
    expect(assignment.source).toBe('active_lease');
    expect(assignment.profileName).toBe('careful');
    expect(assignment.provider).toBe('careful-provider');
    // The effective reasoning variant, not the lease's requested one.
    expect(assignment.thinking).toBe('high');
  });

  it('reports this persona\u2019s most recent lease as latest_lease, never as default_route', () => {
    const assignment = effectiveAssignment({
      personaID: 'painter',
      // The active lease belongs to somebody else.
      status: statusOf(leaseOf('writer', 'quick', -60_000, 'off')),
      profiles: PROFILES,
      recentLeases: [leaseOf('painter', 'careful', -3_600_000, 'medium')],
    });
    expect(assignment.source).toBe('latest_lease');
    expect(assignment.source).not.toBe('default_route');
    expect(assignment.profileName).toBe('careful');
    expect(assignment.thinking).toBe('medium');
  });

  it('picks the most recent lease for the persona whatever order the array arrives in', () => {
    const older = leaseOf('painter', 'archived', -7_200_000, 'off');
    const newest = leaseOf('painter', 'careful', -600_000, 'high');
    const middle = leaseOf('painter', 'quick', -3_600_000, 'medium');
    const otherPersona = leaseOf('writer', 'quick', -1_000, 'off');

    const orders: Lease[][] = [
      [older, middle, newest, otherPersona],
      [newest, middle, older, otherPersona],
      [middle, otherPersona, older, newest],
      [otherPersona, newest, older, middle],
    ];

    for (const recentLeases of orders) {
      const assignment = effectiveAssignment({ personaID: 'painter', profiles: PROFILES, recentLeases });
      expect(assignment.source).toBe('latest_lease');
      expect(assignment.profileName).toBe('careful');
      expect(assignment.thinking).toBe('high');
    }
  });

  it('falls back to the selected profile as the config default route', () => {
    const assignment = effectiveAssignment({
      personaID: 'painter',
      status: statusOf(undefined),
      profiles: PROFILES,
      recentLeases: [leaseOf('writer', 'careful', -3_600_000, 'medium')],
    });
    expect(assignment.source).toBe('default_route');
    expect(assignment.profileName).toBe('quick');
    // No lease supplied a variant, so the route default stands in.
    expect(assignment.thinking).toBe('medium');
  });

  it('reports unknown and preserves the raw profile name the controller reported', () => {
    const assignment = effectiveAssignment({
      personaID: 'painter',
      profiles: PROFILES,
      recentLeases: [leaseOf('painter', 'retired-profile', -3_600_000, 'low')],
    });
    expect(assignment.source).toBe('unknown');
    expect(assignment.profileName).toBe('retired-profile');
    expect(assignment.provider).toBe('unknown');
    expect(assignment.model).toBe('unknown');
    expect(assignment.thinking).toBe('low');
  });

  it('reports unknown when nothing at all resolves', () => {
    const assignment = effectiveAssignment({ personaID: 'painter', profiles: [] });
    expect(assignment.source).toBe('unknown');
    expect(assignment.profileName).toBe('');
    expect(assignment.model).toBe('unknown');
  });
});

describe('assignmentSourceLabel', () => {
  it('reads distinctly for every provenance case', () => {
    const labels = (['active_lease', 'latest_lease', 'default_route', 'unknown'] as const).map(assignmentSourceLabel);
    expect(labels).toEqual(['from active lease', 'from latest lease', 'config default route', 'unresolved route']);
    expect(new Set(labels).size).toBe(labels.length);
  });
});
