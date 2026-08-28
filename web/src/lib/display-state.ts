// Truthful derivation of what the 64x64 panel is actually doing.
//
// The controller exposes four independent layers: policy (blackout / daylight /
// test override), proxy health, physical screen state and content publication.
// This module refuses to collapse them into one indicator, and refuses to claim
// the screen is off when the only honest answer is "unknown".
//
// Content publication has exactly one proof: the newest controller frame record
// says it was published. `display.frames`, `display.skipped` and
// `display.last_frame_at` are proxy/device counters — the device may have drawn
// fallback or third-party content, or kept a stale count across a power cycle —
// so they are surfaced as their own device fact and never as steward content.
//
// Pure and total: no DOM, no fetch, never throws on partial data.

import type { DisplayStatus, Status } from '../api/types';
import { count, since, stamp, until } from './format';

export type DisplayCode =
  | 'controller_unknown' // the dashboard has no controller status to reason about
  | 'proxy_unreachable' // controller could not probe the display proxy right now
  | 'device_offline' // proxy answered, device reported offline
  | 'blackout' // controller policy: scheduled blackout in force
  | 'test_override' // scheduled blackout overridden by an operator test window
  | 'armed_waiting_frame' // controller armed the panel, no published steward frame on record
  | 'screen_off' // device says the physical screen is off outside policy blackout
  | 'live'; // steward content is on the panel

export type DisplayTone = 'signal' | 'warn' | 'bad' | 'muted';

export interface DisplayFact {
  label: string;
  value: string;
  tone: DisplayTone;
  hint?: string;
}

export interface DisplayState {
  code: DisplayCode;
  /** Short operator-facing headline, sentence case, no marketing. */
  headline: string;
  /** One truthful sentence explaining exactly why, naming the responsible layer. */
  detail: string;
  tone: DisplayTone;
  /** Independent facts rendered as separate indicators, never collapsed into one dot. */
  facts: DisplayFact[];
  /** Device error worth surfacing, with truthful staleness. */
  error?: { message: string; at?: string; stale: boolean };
}

export interface DisplayInput {
  status: Status | undefined;
  /**
   * Newest controller frame record, published or not (from /api/v1/frames).
   * `published === true` is the only proof that steward content reached the
   * panel; device counters never substitute for it.
   */
  latestFrame?: { created_at: string; published: boolean; publish_error?: string } | undefined;
  /** Poll-level failure talking to the controller itself. */
  fetchError?: boolean;
  now: number;
  staleAfterMs?: number;
}

const DEFAULT_STALE_MS = 120_000;

/** Every layer reads "unknown" when the controller itself has not answered. */
const UNKNOWN_FACTS: DisplayFact[] = [
  { label: 'Policy', value: 'unknown', tone: 'muted', hint: 'no controller status' },
  { label: 'Controller', value: 'unknown', tone: 'muted', hint: 'no controller status' },
  { label: 'Proxy', value: 'unknown', tone: 'muted', hint: 'not probed by this dashboard' },
  { label: 'Screen', value: 'unknown', tone: 'muted', hint: 'never inferred from a missing reply' },
  { label: 'Content', value: 'unknown', tone: 'muted' },
  { label: 'Device counters', value: 'unknown', tone: 'muted', hint: 'no controller status' },
  { label: 'Agent', value: 'unknown', tone: 'muted' },
];

function msOf(value: string | undefined): number | undefined {
  if (!value) return undefined;
  const ms = new Date(value).getTime();
  return Number.isNaN(ms) ? undefined : ms;
}

/** "just now" | "5m ago" for a past instant; never used on future timestamps. */
function ago(value: string | undefined, now: number): string {
  if (msOf(value) === undefined) return 'at an unknown time';
  const text = since(value, now);
  return text === 'just now' ? 'just now' : `${text} ago`;
}

/** "in 3h" | "imminent" | "unknown" for a future instant. */
function inWhen(value: string | undefined, now: number): string {
  if (msOf(value) === undefined) return 'unknown';
  const text = until(value, now);
  return text === 'just now' ? 'imminent' : `in ${text}`;
}

function atText(value: string | undefined, now: number): string {
  if (msOf(value) === undefined) return 'at an unknown time';
  return `at ${stamp(value)} (${ago(value, now)})`;
}

function dueText(value: string | undefined, now: number): string {
  if (msOf(value) === undefined) return 'unknown';
  return `${stamp(value)} (${inWhen(value, now)})`;
}

function agoHint(prefix: string, value: string | undefined, now: number, unknown: string): string {
  return msOf(value) === undefined ? unknown : `${prefix} ${ago(value, now)}`;
}

/** "1 frame" | "42 frames", always about the device's own counter. */
function frameCount(frames: number): string {
  return `${count(frames)} frame${frames === 1 ? '' : 's'}`;
}

/**
 * Sentence appended when the device counts frames the controller has no
 * published record of. Keeps the device's bookkeeping visible while denying it
 * any authority over what the steward published.
 */
function deviceCounterNote(
  display: DisplayStatus,
  frames: number,
  applies: boolean,
  now: number,
): string {
  if (!applies) return '';
  const last = msOf(display.last_frame_at) === undefined ? '' : ` (last ${ago(display.last_frame_at, now)})`;
  return ` The device separately counts ${frameCount(frames)} of its own${last}; that is device-reported bookkeeping, not evidence of steward content.`;
}

interface Layers {
  status: Status;
  display: DisplayStatus;
  probeError: string | undefined;
  testActive: boolean;
  /** Proven only by the newest controller frame record, never by device counters. */
  stewardContent: boolean;
  /** The device reports frames the controller has no published record of. */
  deviceClaimsContent: boolean;
  /** Device-reported frame counter. Not a steward publication count. */
  frames: number;
  /** Creation time of the newest published steward frame record, if any. */
  lastPublishAt: string | undefined;
  publishError: string | undefined;
  now: number;
}

function buildFacts(layer: Layers): DisplayFact[] {
  const { status, display, probeError, testActive, stewardContent, deviceClaimsContent } = layer;
  const { frames, lastPublishAt, publishError, now } = layer;
  const armedWaiting = status.display_armed && !display.screen_on && !stewardContent;

  const policy: DisplayFact = status.blackout
    ? { label: 'Policy', value: 'scheduled blackout', tone: 'muted', hint: `next transition ${inWhen(status.next_transition, now)}` }
    : testActive
      ? { label: 'Policy', value: 'test override', tone: 'warn', hint: `expires ${inWhen(status.test_window_until, now)}` }
      : { label: 'Policy', value: 'daylight', tone: 'signal', hint: `next transition ${inWhen(status.next_transition, now)}` };

  const controller: DisplayFact = status.display_armed
    ? {
        label: 'Controller',
        value: 'armed',
        tone: 'signal',
        hint: armedWaiting ? 'screen wakes on the first published frame' : undefined,
      }
    : { label: 'Controller', value: 'not armed', tone: 'muted', hint: status.blackout ? 'blackout in force' : undefined };

  const proxy: DisplayFact = probeError
    ? {
        label: 'Proxy',
        value: 'unreachable',
        tone: 'bad',
        hint: agoHint('probe failed', status.display_probe_error_at, now, 'probe failure time unknown'),
      }
    : {
        label: 'Proxy',
        value: display.online ? 'online' : 'offline',
        tone: display.online ? 'signal' : 'bad',
        hint: agoHint('probed', display.checked_at, now, 'probe time unknown'),
      };

  const screen: DisplayFact = probeError
    ? { label: 'Screen', value: 'unknown', tone: 'muted', hint: 'no successful probe since the failure' }
    : display.screen_on
      ? { label: 'Screen', value: 'on', tone: 'signal' }
      : {
          label: 'Screen',
          value: 'off',
          tone: 'warn',
          hint: armedWaiting ? 'waiting for the first steward frame' : undefined,
        };

  // Content speaks only for the steward's own archive. A device counter is
  // never allowed to stand in for a published steward frame here: the device
  // may have shown fallback or third-party content, or kept a stale count
  // across a power cycle.
  const content: DisplayFact = stewardContent
    ? {
        label: 'Content',
        value: 'steward frame published',
        tone: publishError ? 'bad' : 'signal',
        hint: publishError
          ? `publish error: ${publishError}`
          : agoHint('published', lastPublishAt, now, 'publish time unknown'),
      }
    : deviceClaimsContent
      ? {
          label: 'Content',
          value: 'not from the steward',
          tone: 'warn',
          hint: publishError
            ? `publish error: ${publishError}`
            : `device counters report ${frameCount(frames)}, but no published steward frame is on record`,
        }
      : {
          label: 'Content',
          value: 'no steward frame yet',
          tone: 'muted',
          hint: publishError ? `publish error: ${publishError}` : undefined,
        };

  // The device's own bookkeeping, kept beside Content and never merged into it.
  const deviceCounters: DisplayFact = {
    label: 'Device counters',
    value: `${frameCount(frames)}, ${count(display.skipped)} skipped`,
    tone: 'muted',
    hint: agoHint(
      'device-reported; last device frame',
      display.last_frame_at,
      now,
      'device-reported; no device frame time',
    ),
  };

  const agent: DisplayFact = status.agent_running
    ? { label: 'Agent', value: 'awake', tone: 'signal' }
    : { label: 'Agent', value: 'idle', tone: 'muted' };

  return [policy, controller, proxy, screen, content, deviceCounters, agent];
}

function buildError(display: DisplayStatus, now: number, staleAfterMs: number): DisplayState['error'] {
  const message = display.last_error?.trim();
  if (!message) return undefined;
  const at = msOf(display.last_error_at);
  // An error whose age is unknown is unverified, never a live outage.
  const stale = at === undefined || now - at > staleAfterMs;
  return { message, at: display.last_error_at, stale };
}

export function deriveDisplayState(input: DisplayInput): DisplayState {
  const { status, latestFrame, now } = input;
  const staleAfterMs = input.staleAfterMs ?? DEFAULT_STALE_MS;

  if (!status || !status.display) {
    return {
      code: 'controller_unknown',
      headline: 'Controller state unknown',
      detail: input.fetchError
        ? 'This dashboard cannot reach the controller, so nothing about the panel is known right now. That is a dashboard-to-controller failure and is not evidence that the display is off.'
        : status
          ? 'The controller replied without a display section, so the panel state is unknown.'
          : 'The controller has not reported status yet, so the panel state is unknown.',
      tone: 'muted',
      facts: UNKNOWN_FACTS,
    };
  }

  const display = status.display;
  const probeError = status.display_probe_error?.trim() || undefined;
  const testUntil = msOf(status.test_window_until);
  const testActive = testUntil !== undefined && testUntil > now;
  // Device-reported counters. They describe what the device believes it drew,
  // including fallback or third-party content and counts kept across a power
  // cycle, so they can never prove the steward published anything.
  const frames = display.frames ?? 0;
  const deviceClaimsContent = frames > 0 || msOf(display.last_frame_at) !== undefined;
  const publishError = latestFrame?.publish_error?.trim() || undefined;
  // The only proof of steward content: the newest controller frame record says
  // it was published. Nothing the device reports can substitute for it.
  const stewardContent = latestFrame?.published === true;
  const lastPublishAt = stewardContent ? latestFrame?.created_at : undefined;

  const facts = buildFacts({
    status,
    display,
    probeError,
    testActive,
    stewardContent,
    deviceClaimsContent,
    frames,
    lastPublishAt,
    publishError,
    now,
  });
  const error = buildError(display, now, staleAfterMs);

  // 2. A failed probe outranks everything: we know nothing about the panel.
  if (probeError) {
    return {
      code: 'proxy_unreachable',
      headline: 'Display proxy unreachable',
      detail: `The controller could not probe the display proxy: ${probeError}. Reported ${atText(status.display_probe_error_at, now)}. The physical panel state is unknown, not off.`,
      tone: 'bad',
      facts,
      error,
    };
  }

  // 3. Proxy answered and told us the device is not there.
  if (!display.online) {
    return {
      code: 'device_offline',
      headline: 'Display device offline',
      detail:
        'The display proxy answered the controller but reports the device offline. No frame can reach the panel until the device answers again; controller policy and the lease are unaffected.',
      tone: 'bad',
      facts,
      error,
    };
  }

  // 4. Controller policy is deliberately holding the screen off.
  if (status.blackout) {
    return {
      code: 'blackout',
      headline: 'Scheduled blackout',
      detail: `Controller policy holds the panel in scheduled blackout, so the screen is off on purpose. The next policy transition is ${dueText(status.next_transition, now)}.`,
      tone: 'muted',
      facts,
      error,
    };
  }

  // 5. An operator test window is running the panel through a blackout.
  if (testActive && status.scheduled_blackout) {
    return {
      code: 'test_override',
      headline: 'Test window overriding blackout',
      detail: `An operator test window overrides the scheduled blackout, so the controller keeps the panel usable outside policy hours. The override expires ${dueText(status.test_window_until, now)}.`,
      tone: 'warn',
      facts,
      error,
    };
  }

  // 6. Daylight arms the panel; the screen wakes on the first published frame.
  // Only a published steward frame record counts here. Device counters are
  // reported beside this, never folded into it.
  if (status.display_armed && !stewardContent) {
    return {
      code: 'armed_waiting_frame',
      headline: deviceClaimsContent ? 'Armed, no steward frame on record' : 'Armed, waiting for the first frame',
      detail:
        (display.screen_on
          ? 'The controller armed the panel for the daylight window and no steward frame has been published yet, so whatever the device is showing did not come from the steward.'
          : 'The controller armed the panel for the daylight window and the device still reports the screen off: the screen turns on when the first steward frame is published.') +
        deviceCounterNote(display, frames, deviceClaimsContent, now),
      tone: deviceClaimsContent ? 'warn' : 'signal',
      facts,
      error,
    };
  }

  // 7. Screen off with no policy reason for it.
  if (!display.screen_on) {
    return {
      code: 'screen_off',
      headline: 'Screen reported off',
      detail: `The device reports the physical screen off even though controller policy is not in blackout${
        status.display_armed ? ' and the controller has armed the panel' : ''
      }.${lastPublishAt ? ` The last steward frame was published ${ago(lastPublishAt, now)}.` : ''}${deviceCounterNote(
        display,
        frames,
        deviceClaimsContent && !stewardContent,
        now,
      )}`,
      tone: 'warn',
      facts,
      error,
    };
  }

  if (!stewardContent) {
    return {
      code: 'screen_off',
      headline: 'No steward content on the panel',
      detail:
        'The proxy is online and the device reports the screen on, but the controller has not armed the panel and no published steward frame is on record, so the panel content did not come from the steward.' +
        deviceCounterNote(display, frames, deviceClaimsContent, now),
      tone: 'warn',
      facts,
      error,
    };
  }

  return {
    code: 'live',
    headline: 'Live on the panel',
    detail: `The proxy is online, the device reports the screen on, and steward content owns the panel.${
      lastPublishAt ? ` Last frame published ${ago(lastPublishAt, now)}.` : ''
    }`,
    tone: 'signal',
    facts,
    error,
  };
}
