import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { api, onUnauthorized } from './api/client';
import { tokenStore } from './api/auth';
import type { TokenScope } from './api/auth';
import { usePoll } from './api/poll';
import type { Frame } from './api/types';
import { deriveDisplayState } from './lib/display-state';
import { since } from './lib/format';
import { Canvas } from './components/Canvas';
import { DisplayPanel } from './components/DisplayPanel';
import { Transcript } from './components/Transcript';
import { LeaseCard } from './components/LeaseCard';
import { Journal } from './components/Journal';
import { Personas } from './components/Personas';
import { Routes } from './components/Routes';
import { FrameArchive } from './components/FrameArchive';
import { InferenceLedger } from './components/InferenceLedger';
import { PersonaDeepDive } from './components/PersonaDeepDive';
import { AuthGate, AuthStatus } from './components/AuthGate';
import { EventLog } from './components/EventLog';

const RECORDS = [
  { id: 'journal', label: 'Journal' },
  { id: 'events', label: 'Event log' },
  { id: 'personas', label: 'Personas' },
  { id: 'routes', label: 'Inference routes' },
  { id: 'frames', label: 'Frame archive' },
  { id: 'inference', label: 'Inference ledger' },
] as const;

type RecordTab = (typeof RECORDS)[number]['id'];

export function App() {
  const status = usePoll(() => api.status(), 2000);
  const events = usePoll(() => api.events(250), 2500);
  const frames = usePoll(() => api.frames(40), 5000);
  const personas = usePoll(() => api.personas(), 15000);
  const profiles = usePoll(() => api.modelProfiles(), 60000);
  const leases = usePoll(() => api.leases(40), 20000);
  const inference = usePoll(() => api.inference(80), 10000);

  const [personaFilter, setPersonaFilter] = useState('');
  const journal = usePoll(() => api.journal(80, personaFilter || undefined), 10000, [personaFilter]);

  const [tab, setTab] = useState<RecordTab>('journal');
  const tabRefs = useRef<Partial<Record<RecordTab, HTMLButtonElement | null>>>({});
  const [openPersona, setOpenPersona] = useState<string | undefined>(undefined);
  const [pinnedFrame, setPinnedFrame] = useState<Frame | undefined>(undefined);

  const latestFrame = frames.data?.[0];
  const shownFrame = pinnedFrame ?? latestFrame;

  const displayState = useMemo(
    () =>
      deriveDisplayState({
        status: status.data,
        latestFrame,
        fetchError: status.error !== undefined,
        now: Date.now(),
      }),
    [status.data, status.error, latestFrame],
  );

  const refreshAll = useCallback(() => {
    status.refresh();
    events.refresh();
    frames.refresh();
    personas.refresh();
    profiles.refresh();
    leases.refresh();
    inference.refresh();
    journal.refresh();
  }, [status, events, frames, personas, profiles, leases, inference, journal]);

  const onOpenPersona = useCallback((id: string) => setOpenPersona(id), []);

  const [authNeeded, setAuthNeeded] = useState(false);
  const [authRejected, setAuthRejected] = useState(false);
  useEffect(
    () =>
      onUnauthorized(() => {
        // A rejection while a token is stored means that token is wrong; with no
        // token stored the controller is simply in bearer mode and wants one.
        setAuthRejected(tokenStore.get() !== undefined);
        setAuthNeeded(true);
      }),
    [],
  );

  const onToken = useCallback(
    (token: string, scope: TokenScope) => {
      tokenStore.set(token, scope);
      setAuthNeeded(false);
      setAuthRejected(false);
      refreshAll();
    },
    [refreshAll],
  );

  const staleMs = status.updatedAt ? Date.now() - status.updatedAt : undefined;
  const freshness =
    status.failures > 0 ? 'is-error' : staleMs !== undefined && staleMs > 15000 ? 'is-stale' : 'is-live';

  return (
    <div class="app">
      <header class="topbar">
        <div class="wordmark">
          <span class="glyph" aria-hidden="true" />
          Pixel Steward
        </div>
        <div class="state">
          <span class={`headline tone-${displayState.tone}`} aria-live="polite">
            {displayState.headline}
          </span>
          <span class="muted">{displayState.detail}</span>
        </div>
        <div class="spacer" />
        <div class={`freshness ${freshness}`}>
          <span class="dot" aria-hidden="true" />
          <span>
            {status.failures > 0
              ? `controller unreachable, ${status.failures} failed ${status.failures === 1 ? 'poll' : 'polls'}`
              : `updated ${since(status.updatedAt)}`}
          </span>
          <button type="button" onClick={refreshAll} title="Refresh every surface now">
            refresh
          </button>
          <AuthStatus onChange={refreshAll} />
        </div>
      </header>

      <div class="workspace">
        <div class="rail">
          <Canvas
            frame={shownFrame}
            state={displayState}
            agentRunning={status.data?.agent_running ?? false}
            loading={status.loading && frames.loading}
          />
          <DisplayPanel
            state={displayState}
            status={status.data}
            nextTransition={status.data?.next_transition}
          />
          <LeaseCard
            status={status.data}
            profiles={profiles.data ?? []}
            loading={status.loading}
            error={status.error}
            onRetry={status.refresh}
            onChanged={refreshAll}
            onOpenPersona={onOpenPersona}
          />
        </div>

        <div class="stage">
          <Transcript
            activeLeaseID={status.data?.lease?.id}
            agentRunning={status.data?.agent_running ?? false}
            leases={leases.data ?? []}
          />
        </div>
      </div>

      <div class="records">
        <div class="tabstrip" role="tablist" aria-label="Records">
          {RECORDS.map((entry) => (
            <button
              key={entry.id}
              type="button"
              role="tab"
              id={`tab-${entry.id}`}
              aria-selected={tab === entry.id}
              aria-controls={`panel-${entry.id}`}
              tabIndex={tab === entry.id ? 0 : -1}
              ref={(node) => {
                tabRefs.current[entry.id] = node as HTMLButtonElement | null;
              }}
              onKeyDown={(event) => {
                const index = RECORDS.findIndex((item) => item.id === tab);
                let next: RecordTab | undefined;
                if (event.key === 'ArrowRight') next = RECORDS[(index + 1) % RECORDS.length].id;
                if (event.key === 'ArrowLeft') next = RECORDS[(index - 1 + RECORDS.length) % RECORDS.length].id;
                if (event.key === 'Home') next = RECORDS[0].id;
                if (event.key === 'End') next = RECORDS[RECORDS.length - 1].id;
                if (!next) return;
                event.preventDefault();
                setTab(next);
                tabRefs.current[next]?.focus();
              }}
              onClick={() => setTab(entry.id)}
            >
              {entry.label}
            </button>
          ))}
        </div>

        <div class="records-body" role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
          {tab === 'journal' ? (
            <Journal
              entries={journal.data ?? []}
              personas={personas.data ?? []}
              personaFilter={personaFilter}
              onPersonaFilter={setPersonaFilter}
              loading={journal.loading}
              error={journal.error}
              onRetry={journal.refresh}
              onOpenPersona={onOpenPersona}
            />
          ) : null}
          {tab === 'events' ? (
            <EventLog
              events={events.data ?? []}
              personas={personas.data ?? []}
              loading={events.loading}
              error={events.error}
              onRetry={events.refresh}
              onOpenPersona={onOpenPersona}
            />
          ) : null}
          {tab === 'personas' ? (
            <Personas
              personas={personas.data ?? []}
              profiles={profiles.data ?? []}
              status={status.data}
              leases={leases.data ?? []}
              loading={personas.loading}
              error={personas.error}
              onRetry={personas.refresh}
              onOpen={onOpenPersona}
              onChanged={refreshAll}
            />
          ) : null}
          {tab === 'routes' ? (
            <Routes
              profiles={profiles.data ?? []}
              activeProfile={status.data?.lease?.model_profile}
              loading={profiles.loading}
              error={profiles.error}
              onRetry={profiles.refresh}
            />
          ) : null}
          {tab === 'frames' ? (
            <FrameArchive
              frames={frames.data ?? []}
              loading={frames.loading}
              error={frames.error}
              onRetry={frames.refresh}
              selectedID={pinnedFrame?.id}
              onSelect={(frame) => setPinnedFrame(pinnedFrame?.id === frame.id ? undefined : frame)}
            />
          ) : null}
          {tab === 'inference' ? (
            <InferenceLedger
              requests={inference.data ?? []}
              loading={inference.loading}
              error={inference.error}
              onRetry={inference.refresh}
            />
          ) : null}
        </div>
      </div>

      <PersonaDeepDive
        personaID={openPersona}
        onClose={() => setOpenPersona(undefined)}
        profiles={profiles.data ?? []}
        status={status.data}
      />

      <AuthGate needed={authNeeded} rejected={authRejected} onSubmit={onToken} />
    </div>
  );
}
