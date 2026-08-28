import '../styles/canvas.css';

import type { Status } from '../api/types';
import type { DisplayState } from '../lib/display-state';
import { clockTime, count, since, stamp, until } from '../lib/format';
import { Panel } from './ui';

export interface DisplayPanelProps {
  state: DisplayState;
  status?: Status;
  nextTransition?: string;
}

/**
 * Reads out the display layers separately: policy, controller arming, proxy
 * health, screen state, steward content publication and agent liveness. The
 * device's own frame counters are reported beside them as device facts, never
 * as proof that the steward published anything. A device error of unverified
 * age is rendered quietly as history, never as an outage.
 */
export function DisplayPanel({ state, status, nextTransition }: DisplayPanelProps) {
  const next = nextTransition ?? status?.next_transition;
  const display = status?.display;
  const error = state.error;
  // since()/until() return bare spans ("5m", "just now"); phrase them here so no
  // string ever reads "just now ago".
  const errorAge = error?.at ? since(error.at) : undefined;
  const errorAgeText = errorAge === undefined ? '' : errorAge === 'just now' ? 'just now' : `${errorAge} ago`;
  const nextAway = next ? until(next) : undefined;
  const nextText =
    next === undefined || nextAway === undefined
      ? 'unknown'
      : `${stamp(next)} · ${nextAway === 'just now' ? 'imminent' : `in ${nextAway}`}`;

  return (
    <Panel
      title="Display"
      meta={
        <span class="display-asof">{status ? `as of ${clockTime(status.as_of)}` : 'no controller reply'}</span>
      }
    >
      <p class={`display-headline tone-${state.tone}`} aria-live="polite">
        {state.headline}
      </p>
      <p class="display-detail">{state.detail}</p>

      <dl class="facts">
        {state.facts.map((fact) => (
          <div class="fact" key={fact.label}>
            <dt class="fact-label">{fact.label}</dt>
            <dd class="fact-value">
              <span class={`tone-${fact.tone}`}>{fact.value}</span>
              {fact.hint ? <span class="fact-hint">{fact.hint}</span> : null}
            </dd>
          </div>
        ))}
      </dl>

      {error ? (
        <div class={error.stale ? 'display-error is-stale' : 'display-error'}>
          <span class="fact-label">{error.stale ? 'Historical device error' : 'Device error'}</span>
          <p class="display-error-message">{error.message}</p>
          <p class="display-error-when">
            {error.at
              ? `${error.stale ? 'Last observed' : 'Observed'} ${stamp(error.at)} · ${errorAgeText}`
              : 'The controller did not report when this error was observed, so its age is unknown and it is treated as unverified rather than current.'}
          </p>
        </div>
      ) : null}

      <div class="display-counters">
        <span class="display-counter">
          <span class="fact-label">Device frames</span>
          <span class="display-value">{display ? count(display.frames) : '—'}</span>
        </span>
        <span class="display-counter">
          <span class="fact-label">Device skipped</span>
          <span class={display && display.skipped > 0 ? 'display-value tone-warn' : 'display-value'}>
            {display ? count(display.skipped) : '—'}
          </span>
        </span>
        <span class="display-counter">
          <span class="fact-label">Next policy transition</span>
          <span class="display-value">{nextText}</span>
        </span>
      </div>
    </Panel>
  );
}
