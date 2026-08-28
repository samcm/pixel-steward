import type { ModelProfile } from '../api/types';
import { endpointHost } from '../lib/assignment';
import { money } from '../lib/format';
import { Empty, Failure, Loading, Panel } from './ui';

export interface RoutesProps {
  profiles: ModelProfile[];
  activeProfile?: string;
  loading: boolean;
  error?: unknown;
  onRetry: () => void;
}

const CREDENTIAL_NOTE =
  'Configuration stores only the environment variable name. The controller reads the value at ' +
  'process start; it is never returned by the API and never rendered here.';

/** Configured inference routes. Routes are independent of persona identity. */
export function Routes(props: RoutesProps) {
  const { profiles, activeProfile, loading, error, onRetry } = props;
  const defaultRoute = profiles.find((profile) => profile.selected);

  const meta = (
    <span class="routes-meta mono">
      {profiles.length} routes · default {defaultRoute ? defaultRoute.name : 'unset'}
    </span>
  );

  return (
    <Panel title="Inference routes" meta={meta} className="routes-panel" scroll>
      {error ? <Failure error={error} retry={onRetry} /> : null}
      {loading && profiles.length === 0 ? <Loading>Reading route table</Loading> : null}
      {!loading && !error && profiles.length === 0 ? <Empty>No inference routes configured.</Empty> : null}

      {profiles.length > 0 ? (
        <table class="routes-table">
          <thead>
            <tr>
              <th scope="col" class="col-mark" aria-label="Binding" />
              <th scope="col" class="col-route">
                Route
              </th>
              <th scope="col" class="col-model">
                Model
              </th>
              <th scope="col" class="col-provider">
                Provider
              </th>
              <th scope="col" class="col-credential">
                Credential{' '}
                <abbr class="join-note" title={CREDENTIAL_NOTE}>
                  env name only
                </abbr>
              </th>
              <th scope="col" class="col-billing">
                Billing
              </th>
              <th scope="col" class="col-reasoning">
                Reasoning
              </th>
            </tr>
          </thead>
          <tbody>
            {profiles.map((profile) => {
              const bound = activeProfile !== undefined && profile.name === activeProfile;
              const host = endpointHost(profile.endpoint);
              const inRate = profile.billing?.input_micros_per_mtok;
              const outRate = profile.billing?.output_micros_per_mtok;
              const allowed = profile.thinking?.allowed ?? [];

              return (
                <tr key={profile.name} class={bound ? 'route-row is-bound' : 'route-row'}>
                  <td class="col-mark">
                    {bound ? (
                      <span class="tag tag-active" title="Bound to the lease running right now">
                        ACTIVE
                      </span>
                    ) : null}
                    {profile.selected ? (
                      <span class="tag tag-default" title="Config default: used when the next lease opens">
                        DEFAULT
                      </span>
                    ) : null}
                  </td>
                  <td class="col-route mono">{profile.name}</td>
                  <td class="col-model mono">{profile.model}</td>
                  <td class="col-provider">
                    <div class="mono">{profile.provider}</div>
                    {host ? <div class="endpoint mono">{host}</div> : <div class="endpoint muted">provider default</div>}
                  </td>
                  <td class="col-credential" title={CREDENTIAL_NOTE}>
                    {profile.credential_env ? (
                      <>
                        <div class="env-name mono">${profile.credential_env}</div>
                        <div class="hint">value never exposed</div>
                      </>
                    ) : (
                      <span class="muted">none required</span>
                    )}
                  </td>
                  <td class="col-billing">
                    <div class="mono">{profile.billing?.mode || 'unknown'}</div>
                    {typeof inRate === 'number' || typeof outRate === 'number' ? (
                      <div class="rates mono">
                        {typeof inRate === 'number' ? `${money(inRate)}/Mtok in` : 'in n/a'}
                        <span class="sep">·</span>
                        {typeof outRate === 'number' ? `${money(outRate)}/Mtok out` : 'out n/a'}
                      </div>
                    ) : (
                      <div class="hint">no metered rates configured</div>
                    )}
                  </td>
                  <td class="col-reasoning">
                    <div class="mono">{profile.thinking?.default || 'unset'}</div>
                    <div class="hint mono">
                      {allowed.length > 0 ? allowed.join(' / ') : 'no variants'}
                    </div>
                    <div class="hint">cache: {profile.thinking?.cache_impact || 'unknown'}</div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}
    </Panel>
  );
}
