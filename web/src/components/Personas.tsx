import '../styles/personas.css';

import { api } from '../api/client';
import type { Lease, ModelProfile, Persona, Status } from '../api/types';
import { ASSIGNMENT_JOIN_NOTE, assignmentSourceLabel, effectiveAssignment } from '../lib/assignment';
import { ActionButton, Empty, Failure, Loading, Panel } from './ui';

export interface PersonasProps {
  personas: Persona[];
  profiles: ModelProfile[];
  status?: Status;
  leases?: Lease[];
  loading: boolean;
  error?: unknown;
  onRetry: () => void;
  onOpen: (personaID: string) => void;
  onChanged: () => void;
}

/**
 * Persona roster with the effective inference assignment joined in. The join is
 * labelled on every row: the persona record itself holds no provider, endpoint
 * or credential.
 */
export function Personas(props: PersonasProps) {
  const { personas, profiles, status, leases, loading, error, onRetry, onOpen, onChanged } = props;
  const activePersonaID = status?.lease?.persona_id;
  const enabledCount = personas.filter((persona) => persona.enabled).length;

  const meta = (
    <span class="personas-meta mono">
      {personas.length} configured · {enabledCount} enabled
    </span>
  );

  return (
    <Panel title="Personas" meta={meta} className="personas-panel" scroll>
      {error ? <Failure error={error} retry={onRetry} /> : null}
      {loading && personas.length === 0 ? <Loading>Reading persona roster</Loading> : null}
      {!loading && !error && personas.length === 0 ? (
        <Empty>No personas configured. Add one to the controller config to give the display an owner.</Empty>
      ) : null}

      {personas.length > 0 ? (
        <table class="personas-table">
          <thead>
            <tr>
              <th scope="col" class="col-persona">
                Persona
              </th>
              <th scope="col" class="col-weight num">
                Weight
              </th>
              <th scope="col" class="col-assignment">
                Effective assignment{' '}
                <abbr class="join-note" title={ASSIGNMENT_JOIN_NOTE}>
                  joined
                </abbr>
              </th>
              <th scope="col" class="col-action" aria-label="Availability" />
            </tr>
          </thead>
          <tbody>
            {personas.map((persona) => {
              const assignment = effectiveAssignment({
                personaID: persona.id,
                status,
                profiles,
                recentLeases: leases,
              });
              const active = persona.id === activePersonaID;
              const rowClass = [
                'persona-row',
                active ? 'is-active' : '',
                persona.enabled ? '' : 'is-off',
              ]
                .filter(Boolean)
                .join(' ');

              return (
                <tr key={persona.id} class={rowClass}>
                  <td class="col-persona">
                    <div class="persona-head">
                      <button type="button" class="link" onClick={() => onOpen(persona.id)}>
                        {persona.display_name || persona.id}
                      </button>
                      {active ? <span class="tag tag-active">ACTIVE</span> : null}
                      {persona.enabled ? null : <span class="tag tag-off">DISABLED</span>}
                    </div>
                    <div class="persona-id mono">{persona.id}</div>
                  </td>
                  <td class="col-weight num mono">{persona.weight}</td>
                  <td class="col-assignment">
                    <div class="route-target mono">
                      {assignment.provider}/{assignment.model}
                    </div>
                    <div class="route-meta">
                      <span class="mono">{assignment.thinking}</span>
                      <span class="sep">·</span>
                      <span class="mono">{assignment.profileName || 'no route'}</span>
                      <span class="sep">·</span>
                      <span class={`source source-${assignment.source}`}>
                        {assignmentSourceLabel(assignment.source)}
                      </span>
                    </div>
                  </td>
                  <td class="col-action">
                    <ActionButton
                      danger={persona.enabled}
                      title={
                        persona.enabled
                          ? `Stop scheduling leases for ${persona.id}`
                          : `Allow the scheduler to lease the display to ${persona.id}`
                      }
                      confirm={
                        persona.enabled && active
                          ? `${persona.id} currently holds the display. Disabling it ends the active lease. Continue?`
                          : undefined
                      }
                      onPress={async () => {
                        await api.setPersonaEnabled(persona.id, !persona.enabled);
                        onChanged();
                      }}
                    >
                      {persona.enabled ? 'Disable' : 'Enable'}
                    </ActionButton>
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
