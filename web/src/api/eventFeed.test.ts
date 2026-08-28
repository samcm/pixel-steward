import { describe, it, expect } from 'vitest';

import { mergeEvents, newestID, oldestID } from './eventFeed';
import type { StewardEvent } from './types';

function event(id: number, type = 'frame.submitted'): StewardEvent {
  return {
    id,
    at: new Date(Date.UTC(2026, 0, 1, 0, 0, 0, id)).toISOString(),
    actor: 'controller',
    type,
    correlation_id: `corr_${id}`,
    payload: { seq: id },
    lease_id: 'lease_a',
    persona_id: 'persona_a',
  };
}

/** A page as the API returns it: newest first, descending by id. */
function descending(events: StewardEvent[]): StewardEvent[] {
  return events.slice().sort((left, right) => right.id - left.id);
}

function ids(events: StewardEvent[]): number[] {
  return events.map((entry) => entry.id);
}

describe('mergeEvents', () => {
  it('appends a forward page and returns ascending ids', () => {
    const held = [event(1), event(2)];
    const merged = mergeEvents(held, descending([event(3), event(4)]));
    expect(ids(merged)).toEqual([1, 2, 3, 4]);
  });

  it('sorts a backfill page of older events below the newer ones already held', () => {
    const held = mergeEvents([], descending([event(90), event(91), event(92)]));
    const merged = mergeEvents(held, descending([event(10), event(11), event(50)]));
    expect(ids(merged)).toEqual([10, 11, 50, 90, 91, 92]);
  });

  it('deduplicates by id and keeps the object identity already held', () => {
    const first = event(7);
    const held = mergeEvents([], [first]);
    const merged = mergeEvents(held, [event(7), event(8)]);
    expect(ids(merged)).toEqual([7, 8]);
    // Identity is the contract that stops Preact remounting an existing row.
    expect(merged[0]).toBe(first);
  });

  it('deduplicates repeats inside a single page', () => {
    expect(ids(mergeEvents([], [event(3), event(3), event(4)]))).toEqual([3, 4]);
  });

  it('returns the held array unchanged when a poll adds nothing', () => {
    const held = mergeEvents([], [event(1), event(2)]);
    expect(mergeEvents(held, [event(2), event(1)])).toBe(held);
    expect(mergeEvents(held, [])).toBe(held);
  });

  it('never drops held rows because a later page returned fewer of them', () => {
    const held = mergeEvents([], [event(1), event(2), event(3), event(4)]);
    const merged = mergeEvents(held, [event(4)]);
    expect(ids(merged)).toEqual([1, 2, 3, 4]);
  });

  it('ignores rows without a usable id instead of poisoning the cursor', () => {
    const junk = [{ at: 'nope' }, null, event(5)] as unknown as StewardEvent[];
    const merged = mergeEvents([], junk);
    expect(ids(merged)).toEqual([5]);
    expect(newestID(merged)).toBe(5);
  });
});

describe('cursors', () => {
  it('are undefined on an empty feed', () => {
    expect(newestID([])).toBeUndefined();
    expect(oldestID([])).toBeUndefined();
  });

  it('report the id bounds of the accumulated feed', () => {
    const held = mergeEvents([], descending([event(4), event(9), event(6)]));
    expect(oldestID(held)).toBe(4);
    expect(newestID(held)).toBe(9);
  });

  it('walk backwards page by page without repeating a cursor', () => {
    let held = mergeEvents([], descending([event(30), event(29), event(28)]));
    const cursors: Array<number | undefined> = [oldestID(held)];
    held = mergeEvents(held, descending([event(27), event(26), event(25)]));
    cursors.push(oldestID(held));
    held = mergeEvents(held, descending([event(24)]));
    cursors.push(oldestID(held));
    expect(cursors).toEqual([28, 25, 24]);
    expect(newestID(held)).toBe(30);
  });
});

describe('renderer telemetry flood', () => {
  // The regression the hardening brief names: the live controller emits one
  // frame.submitted plus one controller.tick.error every second, so an
  // unfiltered window drowns the two rows the operator actually wants.
  it('keeps the model text rows while noisy pages keep arriving', () => {
    const noise: StewardEvent[] = [];
    for (let id = 1; id <= 300; id++) {
      noise.push(event(id, id % 2 === 0 ? 'frame.submitted' : 'controller.tick.error'));
    }
    const said = [event(301, 'runtime.text'), event(302, 'runtime.text')];

    // Newest page first, exactly as the initial load sees it.
    let held = mergeEvents([], descending([...noise.slice(200), ...said]));
    // Then two backfill pages of older noise.
    held = mergeEvents(held, descending(noise.slice(100, 200)));
    held = mergeEvents(held, descending(noise.slice(0, 100)));
    // Then two forward polls that report only fresh telemetry.
    held = mergeEvents(held, [event(303), event(304)]);
    held = mergeEvents(held, [event(305)]);

    expect(held).toHaveLength(305);
    expect(ids(held.filter((entry) => entry.type === 'runtime.text'))).toEqual([301, 302]);
    expect(ids(held).every((id, index) => id === index + 1)).toBe(true);
    expect(newestID(held)).toBe(305);
    expect(oldestID(held)).toBe(1);
  });
});
