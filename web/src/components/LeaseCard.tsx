import { useEffect, useState } from 'preact/hooks';
import { ActionButton, Empty, Failure, Label, Loading, Meter, Panel, Rows } from './ui';
import { api } from '../api/client';
import type { ModelProfile, Status } from '../api/types';
import { clockTime, count, money, stamp, until } from '../lib/format';

export interface LeaseCardProps {
  status?: Status;
  profiles: ModelProfile[];
  loading: boolean;
  error?: unknown;
  onRetry: () => void;
  onChanged: () => void;
  onOpenPersona: (personaID: string) => void;
}

/** Wall clock that ticks so the remaining lease time stays live between polls. */
function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(timer);
  }, [intervalMs]);
  return now;
}

export function LeaseCard(props: LeaseCardProps) {
  const { status, profiles, loading, error, onRetry, onChanged, onOpenPersona } = props;
  const now = useNow(1000);
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState('');
  const [failure, setFailure] = useState('');
  /**
   * Value the operator just applied, tagged with the status it was applied
   * against. It holds the control steady for the one render before the next
   * poll reports the controller's own view, instead of snapping back.
   */
  const [override, setOverride] = useState<{ value: string; at: string } | null>(null);

  const lease = status?.lease;
  const budget = status?.budget;
  const reasoning = status?.reasoning;
  const profile = lease
    ? profiles.find((candidate) => candidate.name === lease.model_profile)
    : profiles.find((candidate) => candidate.selected);

  const meta =
    status === undefined ? null : (
      <span class="rec-meta">
        {lease ? (
          <span class={lease.status === 'active' ? 'rec-tone-ok' : 'rec-tone-muted'}>{lease.status}</span>
        ) : (
          <span class="rec-tone-muted">idle</span>
        )}{' '}
        · as of {clockTime(status.as_of)}
      </span>
    );

  return (
    <Panel title="Lease" meta={meta}>
      {error === undefined || error === null ? null : <Failure error={error} retry={onRetry} />}
      {status === undefined ? (
        loading ? (
          <Loading>Reading controller status…</Loading>
        ) : (
          <Empty>Controller status unavailable.</Empty>
        )
      ) : lease === undefined ? (
        <Empty>
          <div>No persona currently holds the display.</div>
          <div class="lease-note">
            next policy transition {stamp(status.next_transition)} (in {until(status.next_transition, now)})
            {status.blackout ? ' · controller is in blackout' : ''}
            {status.test_window_until
              ? ` · scheduled blackout overridden until ${stamp(status.test_window_until)}`
              : ''}
          </div>
          {profile === undefined ? null : (
            <div class="lease-note">
              next inference binding {profile.provider}/{profile.model} via route {profile.name}
            </div>
          )}
        </Empty>
      ) : (
        <div class="lease">
          <div class="lease-head">
            <div class="lease-identity">
              <button
                type="button"
                class="link lease-persona"
                onClick={() => onOpenPersona(lease.persona_id)}
              >
                {lease.persona_id}
              </button>
              <span class="rec-meta">{lease.id}</span>
            </div>
            <div class="lease-window">
              <span>
                {stamp(lease.started_at)} → {stamp(lease.ends_at)}
              </span>
              <span class="lease-remaining">
                {lease.ended_at
                  ? `ended ${stamp(lease.ended_at)}`
                  : `${until(lease.ends_at, now)} remaining`}
              </span>
            </div>
          </div>

          <div class="lease-grid">
            <div class="lease-block">
              <Label>inference binding</Label>
              <div class="lease-model-id">
                {profile === undefined ? lease.model_profile : `${profile.provider}/${profile.model}`}
              </div>
              <Rows
                items={[
                  ['route', <span class="rec-mono">{lease.model_profile}</span>],
                  [
                    'reasoning',
                    <span class="rec-mono">{reasoning?.effective || lease.thinking || '—'}</span>,
                  ],
                  ['source', <span class="rec-mono">{reasoning?.source || '—'}</span>],
                  ['cache impact', <span class="rec-mono">{reasoning?.cache_impact || 'unknown'}</span>],
                  ['billing', <span class="rec-mono">{profile?.billing.mode || 'unknown'}</span>],
                ]}
              />
              {reasoning === undefined || reasoning.allowed.length === 0 ? (
                <span class="rec-meta">No reasoning override available for this route.</span>
              ) : (
                <div class="lease-control">
                  <Label>reasoning override</Label>
                  <select
                    class="rec-select"
                    aria-label="Override lease reasoning effort"
                    disabled={pending}
                    value={
                      override !== null && override.at === status.as_of ? override.value : reasoning.effective
                    }
                    onChange={async (event) => {
                      const select = event.currentTarget;
                      const value = select.value;
                      const current =
                        override !== null && override.at === status.as_of
                          ? override.value
                          : reasoning.effective;
                      if (value === current) return;
                      if (
                        !window.confirm(
                          `Apply operator-only reasoning override "${value}"? This can change provider cache behaviour.`,
                        )
                      ) {
                        select.value = current;
                        return;
                      }
                      setPending(true);
                      setNotice('');
                      setFailure('');
                      try {
                        const result = await api.setReasoning(value);
                        const applied = result.effective || value;
                        setOverride({ value: applied, at: status.as_of });
                        setNotice(`override applied: ${applied}`);
                        onChanged();
                      } catch (cause) {
                        setFailure(cause instanceof Error ? cause.message : String(cause));
                        select.value = current;
                      } finally {
                        setPending(false);
                      }
                    }}
                  >
                    {reasoning.allowed.map((option) => (
                      <option key={option} value={option}>
                        {option}
                      </option>
                    ))}
                  </select>
                </div>
              )}
              <div class="lease-status" aria-live="polite" data-pending={pending ? 'true' : 'false'}>
                {pending ? 'applying reasoning override…' : notice}
              </div>
              {failure === '' ? null : (
                <div class="lease-error" aria-live="polite">
                  reasoning override failed: {failure}
                </div>
              )}
            </div>

            <div class="lease-block">
              <Label>budget</Label>
              {budget === undefined ? (
                <span class="rec-meta">No active budget ledger.</span>
              ) : (
                <>
                  <div class="lease-meters">
                    <Meter
                      label="input tokens"
                      used={budget.input_tokens.used}
                      limit={budget.input_tokens.limit}
                    />
                    <Meter
                      label="output tokens"
                      used={budget.output_tokens.used}
                      limit={budget.output_tokens.limit}
                    />
                    <Meter label="model calls" used={budget.calls.used} limit={budget.calls.limit} />
                    <Meter
                      label="scene commits"
                      used={budget.scene_commits.used}
                      limit={budget.scene_commits.limit}
                    />
                    <Meter
                      label="active runtime"
                      used={budget.active_runtime.used_seconds}
                      limit={budget.active_runtime.limit_seconds}
                      suffix="s"
                    />
                  </div>
                  <Rows
                    items={[
                      [
                        'estimated cost',
                        <span class="rec-mono">
                          {budget.estimated_metered_cost.limit_micros == null
                            ? `${money(budget.estimated_metered_cost.used_micros)} · no cost ceiling`
                            : `${money(budget.estimated_metered_cost.used_micros)} of ${money(
                                budget.estimated_metered_cost.limit_micros,
                              )}`}
                        </span>,
                      ],
                      [
                        'ledger',
                        <span
                          class={`rec-mono ${budget.status === 'exhausted' ? 'rec-tone-bad' : 'rec-tone-ok'}`}
                        >
                          {budget.status}
                        </span>,
                      ],
                      [
                        'per-call output limit',
                        <span class="rec-mono">{count(budget.per_call_output_limit)}</span>,
                      ],
                      ['as of', <span class="rec-mono">{clockTime(budget.as_of)}</span>],
                    ]}
                  />
                </>
              )}
            </div>
          </div>

          <div class="lease-actions">
            <ActionButton
              danger
              confirm="Revoke the active lease? The persona loses the display immediately."
              title="End the current lease now"
              onPress={async () => {
                await api.revokeLease();
                onChanged();
              }}
            >
              Revoke lease
            </ActionButton>
            <span class="lease-note">
              Revoking ends the lease immediately; the controller picks the next persona on its own schedule.
            </span>
          </div>
        </div>
      )}
    </Panel>
  );
}
