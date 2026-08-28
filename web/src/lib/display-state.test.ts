import { describe, expect, it } from 'vitest';
import type { DisplayStatus, Status } from '../api/types';
import type { DisplayState } from './display-state';
import { deriveDisplayState } from './display-state';

const NOW = Date.parse('2026-08-29T12:00:00.000Z');
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

function statusOf(overrides: Partial<Status> = {}, display: Partial<DisplayStatus> = {}): Status {
  return {
    as_of: iso(0),
    blackout: false,
    scheduled_blackout: false,
    next_transition: iso(3_600_000),
    display_armed: true,
    agent_running: false,
    display: {
      online: true,
      screen_on: true,
      frames: 12,
      skipped: 1,
      last_frame_at: iso(-30_000),
      checked_at: iso(-2_000),
      ...display,
    },
    ...overrides,
  };
}

const derive = (status: Status | undefined, extra: Partial<Parameters<typeof deriveDisplayState>[0]> = {}) =>
  deriveDisplayState({ status, now: NOW, ...extra });

/** A controller frame record. `published: true` is the only proof of steward content. */
const stewardFrame = (offsetMs = -10_000, published = true) => ({ created_at: iso(offsetMs), published });

const factOf = (state: DisplayState, label: string) => state.facts.find((fact) => fact.label === label);

describe('deriveDisplayState precedence', () => {
  it('lets a probe failure beat blackout and offline, and calls the screen unknown', () => {
    const state = derive(
      statusOf(
        {
          blackout: true,
          scheduled_blackout: true,
          display_probe_error: 'dial tcp display-proxy:80: connect: connection refused',
          display_probe_error_at: iso(-5_000),
        },
        { online: false, screen_on: false },
      ),
    );
    expect(state.code).toBe('proxy_unreachable');
    expect(state.tone).toBe('bad');
    expect(state.detail).toContain('connection refused');
    expect(state.detail).toContain('unknown, not off');
    expect(factOf(state, 'Screen')?.value).toBe('unknown');
    expect(factOf(state, 'Policy')?.value).toBe('scheduled blackout');
  });

  it('reports device_offline when the proxy answered but the device did not', () => {
    const state = derive(statusOf({}, { online: false, screen_on: false }));
    expect(state.code).toBe('device_offline');
    expect(factOf(state, 'Proxy')?.value).toBe('offline');
  });

  it('prefers blackout over an expired test window', () => {
    const state = derive(
      statusOf({ blackout: true, scheduled_blackout: true, test_window_until: iso(-60_000) }, { screen_on: false }),
    );
    expect(state.code).toBe('blackout');
    expect(state.tone).toBe('muted');
    expect(state.detail).toContain('Controller policy');
  });

  it('reports a test override when a live test window suppresses a scheduled blackout', () => {
    const state = derive(statusOf({ scheduled_blackout: true, test_window_until: iso(900_000) }));
    expect(state.code).toBe('test_override');
    expect(state.tone).toBe('warn');
    expect(factOf(state, 'Policy')?.value).toBe('test override');
  });

  it('surfaces a test override outside a scheduled blackout as a fact, not a headline', () => {
    const state = derive(statusOf({ test_window_until: iso(900_000) }), { latestFrame: stewardFrame() });
    expect(state.code).toBe('live');
    expect(factOf(state, 'Policy')?.value).toBe('test override');
  });

  it('explains armed-but-waiting-for-the-first-frame', () => {
    const state = derive(
      statusOf({ display_armed: true, agent_running: true }, { screen_on: false, frames: 0, last_frame_at: undefined }),
    );
    expect(state.code).toBe('armed_waiting_frame');
    expect(state.tone).toBe('signal');
    expect(state.detail).toContain('first steward frame is published');
    expect(factOf(state, 'Content')?.value).toBe('no steward frame yet');
    expect(factOf(state, 'Controller')?.value).toBe('armed');
    expect(factOf(state, 'Agent')?.value).toBe('awake');
  });

  it('reports screen_off once a frame has been published but the screen is dark', () => {
    const state = derive(statusOf({}, { screen_on: false }), { latestFrame: stewardFrame(-30_000) });
    expect(state.code).toBe('screen_off');
    expect(state.tone).toBe('warn');
  });

  it('reports live when the proxy is online, the screen is on and a frame exists', () => {
    const state = derive(statusOf({}, { frames: 0, last_frame_at: undefined }), {
      latestFrame: { created_at: iso(-10_000), published: true },
    });
    expect(state.code).toBe('live');
    expect(state.tone).toBe('signal');
    expect(factOf(state, 'Content')?.value).toBe('steward frame published');
  });
});

describe('deriveDisplayState error staleness', () => {
  it('marks a two-hour-old device error as stale and keeps its timestamp', () => {
    const at = iso(-7_200_000);
    const state = derive(statusOf({}, { last_error: 'push timeout', last_error_at: at }));
    expect(state.error).toEqual({ message: 'push timeout', at, stale: true });
  });

  it('marks a five-second-old device error as live', () => {
    const state = derive(statusOf({}, { last_error: 'push timeout', last_error_at: iso(-5_000) }));
    expect(state.error?.stale).toBe(false);
  });

  it('treats an error of unknown age as stale', () => {
    const state = derive(statusOf({}, { last_error: 'push timeout', last_error_at: undefined }));
    expect(state.error).toEqual({ message: 'push timeout', at: undefined, stale: true });
  });

  it('reports no error when the device reported none', () => {
    expect(derive(statusOf()).error).toBeUndefined();
  });
});

describe('deriveDisplayState with no controller status', () => {
  it('never claims the display is off', () => {
    const state = derive(undefined);
    expect(state.code).toBe('controller_unknown');
    expect(state.tone).toBe('muted');
    expect(state.headline).toBe('Controller state unknown');
    expect(state.detail).toContain('unknown');
    expect(state.detail).not.toMatch(/screen is off|display is off|blackout/);
    expect(state.facts.every((fact) => fact.value === 'unknown')).toBe(true);
  });

  it('blames the dashboard-to-controller hop when the poll itself failed', () => {
    const state = derive(undefined, { fetchError: true });
    expect(state.code).toBe('controller_unknown');
    expect(state.detail).toContain('cannot reach the controller');
    expect(state.detail).toContain('not evidence that the display is off');
  });
});

describe('deriveDisplayState content truthfulness', () => {
  it('never reports live while no steward frame has been published', () => {
    const state = derive(statusOf({}, { frames: 0, last_frame_at: undefined }));
    expect(state.code).toBe('armed_waiting_frame');
    expect(state.detail).not.toMatch(/steward content owns the panel/);
    expect(factOf(state, 'Content')?.value).toContain('no steward frame');
  });

  it('reports live only once steward content is on the panel', () => {
    const state = derive(statusOf(), { latestFrame: stewardFrame(-30_000) });
    expect(state.code).toBe('live');
    expect(state.detail).toContain('Last frame published');
  });

  it('does not invent a publish age when the controller reports none', () => {
    const state = derive(statusOf({}, { frames: 0, last_frame_at: undefined }));
    expect(state.detail).not.toMatch(/\d+d ago/);
    expect(factOf(state, 'Content')?.hint ?? '').not.toMatch(/\d+d ago/);
  });

  it('refuses device frame counters as proof of steward content', () => {
    // The review's regression: the device reports 42 frames and a recent device
    // frame time, the proxy is online and the screen is on, but the controller
    // has no published steward frame on record.
    const state = derive(statusOf({}, { frames: 42, skipped: 3, last_frame_at: iso(-15_000) }));
    expect(state.code).not.toBe('live');
    expect(state.code).toBe('armed_waiting_frame');
    expect(state.tone).toBe('warn');
    expect(state.headline).toBe('Armed, no steward frame on record');
    expect(state.detail).not.toMatch(/steward content owns the panel/);
    expect(state.detail).toContain('did not come from the steward');
    expect(state.detail).toContain('not evidence of steward content');
    expect(factOf(state, 'Content')?.value).toBe('not from the steward');
    expect(factOf(state, 'Content')?.tone).toBe('warn');
  });

  it('refuses a newest frame record that failed to publish', () => {
    const state = derive(statusOf({}, { frames: 42, last_frame_at: iso(-15_000) }), {
      latestFrame: { created_at: iso(-9_000), published: false, publish_error: 'proxy refused the frame' },
    });
    expect(state.code).not.toBe('live');
    expect(state.detail).not.toMatch(/steward content owns the panel/);
    expect(factOf(state, 'Content')?.value).toBe('not from the steward');
    expect(factOf(state, 'Content')?.hint).toContain('proxy refused the frame');
  });

  it('trusts the steward frame archive even when the device counter reads zero', () => {
    const state = derive(statusOf({}, { frames: 0, skipped: 0, last_frame_at: undefined }), {
      latestFrame: stewardFrame(-12_000),
    });
    expect(state.code).toBe('live');
    expect(factOf(state, 'Content')?.value).toBe('steward frame published');
    expect(factOf(state, 'Device counters')?.value).toBe('0 frames, 0 skipped');
  });

  it('names device counters as their own fact, separate from steward publication', () => {
    const state = derive(statusOf({}, { frames: 7, skipped: 2, last_frame_at: iso(-20_000) }), {
      latestFrame: stewardFrame(-20_000),
    });
    const content = factOf(state, 'Content');
    const device = factOf(state, 'Device counters');
    expect(content?.value).toBe('steward frame published');
    expect(content?.value).not.toContain('7');
    expect(content?.hint).toContain('published');
    expect(device?.value).toContain('7 frames');
    expect(device?.value).toContain('2 skipped');
    expect(device?.hint).toContain('device-reported');
  });

  it('takes the publish age from the steward frame record, not the device counter', () => {
    const state = derive(statusOf({}, { frames: 9, last_frame_at: iso(-3_600_000) }), {
      latestFrame: stewardFrame(-30_000),
    });
    expect(state.code).toBe('live');
    expect(state.detail).toContain('Last frame published 30s ago');
  });
});
