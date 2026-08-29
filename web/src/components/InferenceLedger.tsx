import { Fragment } from 'preact';
import { useMemo } from 'preact/hooks';
import { CodeBlock, Disclosure, Empty, Failure, Loading, Panel } from './ui';
import type { InferenceRequest } from '../api/types';
import { clockTime, count, duration, money, pretty, stamp } from '../lib/format';

export interface InferenceLedgerProps {
  requests: InferenceRequest[];
  loading: boolean;
  error?: unknown;
  onRetry: () => void;
}

const COLUMNS = 14;

export function InferenceLedger(props: InferenceLedgerProps) {
  const { requests, loading, error, onRetry } = props;

  const totals = useMemo(() => {
    const sum = { calls: 0, prompt: 0, completion: 0, reasoning: 0, cacheRead: 0, cacheWrite: 0, micros: 0, ms: 0 };
    for (const request of requests) {
      sum.calls += request.model_calls;
      sum.prompt += request.prompt_tokens;
      sum.completion += request.completion_tokens;
      sum.reasoning += request.reasoning_tokens;
      sum.cacheRead += request.cache_read_tokens;
      sum.cacheWrite += request.cache_write_tokens;
      sum.micros += request.estimated_metered_micros;
      if (request.ended_at !== undefined) {
        const span = new Date(request.ended_at).getTime() - new Date(request.started_at).getTime();
        if (!Number.isNaN(span) && span > 0) sum.ms += span;
      }
    }
    return sum;
  }, [requests]);

  const meta = (
    <span class="rec-meta">
      {requests.length} {requests.length === 1 ? 'record' : 'records'} · {count(totals.calls)} model calls ·{' '}
      {money(totals.micros)} estimated
    </span>
  );

  return (
    <Panel title="Inference ledger" meta={meta}>
      {error === undefined || error === null ? null : <Failure error={error} retry={onRetry} />}
      {loading ? <Loading>Loading inference records…</Loading> : null}
      {!loading && requests.length === 0 ? <Empty>No inference requests recorded yet.</Empty> : null}
      {requests.length === 0 ? null : (
        <div class="ledger-scroll">
          <table class="ledger">
            <thead>
              <tr>
                <th class="ledger-sticky">started</th>
                <th>persona</th>
                <th>provider / model</th>
                <th>reasoning</th>
                <th class="rec-num">calls</th>
                <th class="rec-num">prompt</th>
                <th class="rec-num">completion</th>
                <th class="rec-num">reasoning tok</th>
                <th class="rec-num">cache read</th>
                <th class="rec-num">cache write</th>
                <th class="rec-num">duration</th>
                <th class="rec-num">est. cost</th>
                <th>status</th>
                <th>stop reason</th>
              </tr>
            </thead>
            <tbody>
              {requests.map((request) => {
                const span =
                  request.ended_at === undefined
                    ? undefined
                    : new Date(request.ended_at).getTime() - new Date(request.started_at).getTime();
                const tone = /fail|error|reject/i.test(request.status)
                  ? 'rec-tone-bad'
                  : /run|start|pending/i.test(request.status)
                    ? 'rec-tone-warn'
                    : request.status === 'completed'
                      ? 'rec-tone-ok'
                      : 'rec-tone-muted';
                const costTitle = [
                  `estimated ${money(request.estimated_metered_micros)}`,
                  request.provider_reported_micros == null
                    ? null
                    : `provider reported ${money(request.provider_reported_micros)}`,
                  request.actual_billed_micros == null
                    ? null
                    : `actual billed ${money(request.actual_billed_micros)}`,
                  request.allocated_cost_micros == null
                    ? null
                    : `allocated ${money(request.allocated_cost_micros)}`,
                ]
                  .filter((line): line is string => line !== null)
                  .join(' · ');
                const identity = [
                  `request ${request.id}`,
                  `lease ${request.lease_id}`,
                  request.provider_request_id ? `provider ${request.provider_request_id}` : null,
                ]
                  .filter((line): line is string => line !== null)
                  .join(' · ');
                return (
                  <Fragment key={request.id}>
                    <tr>
                      <td class="ledger-sticky rec-mono" title={stamp(request.started_at)}>
                        {clockTime(request.started_at)}
                      </td>
                      <td class="rec-mono" title={identity}>
                        {request.persona_id}
                      </td>
                      <td class="rec-mono">
                        {request.provider}/{request.model}
                      </td>
                      <td class="rec-mono" title={`source: ${request.thinking_source || 'unknown'}`}>
                        {request.thinking || '—'}
                      </td>
                      <td class="rec-num">{count(request.model_calls)}</td>
                      <td class="rec-num">{count(request.prompt_tokens)}</td>
                      <td class="rec-num">{count(request.completion_tokens)}</td>
                      <td class="rec-num">{count(request.reasoning_tokens)}</td>
                      <td class="rec-num">{count(request.cache_read_tokens)}</td>
                      <td class="rec-num">{count(request.cache_write_tokens)}</td>
                      <td class="rec-num">{duration(span)}</td>
                      <td class="rec-num" title={costTitle}>
                        {money(request.estimated_metered_micros)}
                      </td>
                      <td class={`rec-mono ${tone}`}>{request.status}</td>
                      <td class="rec-mono">{request.stop_reason || '—'}</td>
                    </tr>
                    {request.raw_usage === undefined || request.raw_usage === null ? null : (
                      <tr class="ledger-raw">
                        <td colSpan={COLUMNS}>
                          <div class="ledger-raw-inner">
                            <Disclosure summary="raw usage" tone="quiet">
                              <CodeBlock text={pretty(request.raw_usage)} language="json" clamp={320} />
                            </Disclosure>
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
            <tfoot>
              <tr>
                <td class="ledger-sticky rec-mono">
                  {requests.length} {requests.length === 1 ? 'request' : 'requests'}
                </td>
                <td />
                <td />
                <td class="rec-mono rec-tone-muted">totals</td>
                <td class="rec-num">{count(totals.calls)}</td>
                <td class="rec-num">{count(totals.prompt)}</td>
                <td class="rec-num">{count(totals.completion)}</td>
                <td class="rec-num">{count(totals.reasoning)}</td>
                <td class="rec-num">{count(totals.cacheRead)}</td>
                <td class="rec-num">{count(totals.cacheWrite)}</td>
                <td class="rec-num">{duration(totals.ms)}</td>
                <td class="rec-num">{money(totals.micros)}</td>
                <td />
                <td />
              </tr>
            </tfoot>
          </table>
        </div>
      )}
    </Panel>
  );
}
