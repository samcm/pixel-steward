import '../styles/canvas.css';

import { useEffect, useState } from 'preact/hooks';
import type { Frame } from '../api/types';
import { objectURL } from '../api/client';
import type { DisplayState } from '../lib/display-state';
import { since } from '../lib/format';

export interface CanvasProps {
  frame?: Frame;
  state: DisplayState;
  agentRunning: boolean;
  loading: boolean;
}

/**
 * The 64x64 panel mirror. Scale is always a whole multiple of 64 (8x/6x/4x via
 * canvas.css) with pixelated rendering, and a new frame is decoded off-screen
 * before it replaces the visible one so polling never blanks the canvas.
 */
export function Canvas({ frame, state, agentRunning, loading }: CanvasProps) {
  const [shown, setShown] = useState<{ id: number; src: string } | undefined>(undefined);
  const [failedID, setFailedID] = useState<number | undefined>(undefined);
  const id = frame?.id;
  const object = frame?.final_object;

  useEffect(() => {
    // No frame, or the same frame: keep the pixels already on screen.
    if (id === undefined || !object) return;
    const url = objectURL(object);
    const image = new Image();
    let cancelled = false;
    image.onload = () => {
      if (cancelled) return;
      setShown({ id, src: url });
      setFailedID(undefined);
    };
    image.onerror = () => {
      if (!cancelled) setFailedID(id);
    };
    image.src = url;
    return () => {
      cancelled = true;
      image.onload = null;
      image.onerror = null;
    };
  }, [id, object]);

  const blackout = state.code === 'blackout';
  const loadFailed = failedID !== undefined && failedID === id;
  const awaitingNewer = shown !== undefined && id !== undefined && shown.id !== id && !loadFailed;

  let note: string | undefined;
  if (!shown) {
    if (loading) note = 'Reading controller state';
    else if (loadFailed) note = 'The newest frame object could not be loaded from the controller.';
    else if (!frame) note = agentRunning ? 'Agent is thinking. No frame published yet.' : 'No steward frame has been published yet.';
    else note = 'Decoding the newest frame object.';
  }

  let plate = 'canvas-plate';
  if (agentRunning) plate += ' is-live';
  if (blackout) plate += ' is-dark';

  // The image may be the newest frame, an unpublished frame, or an older archive
  // frame pinned from the strip, so the alt text never asserts publication.
  const alt = frame
    ? `Frame #${frame.sequence} from persona ${frame.persona_id}, ${
        frame.published ? 'published to the panel' : 'an unpublished frame from the archive'
      }, 64 by 64 pixels.`
    : 'Steward frame, 64 by 64 pixels.';

  return (
    <figure class="canvas">
      <div class={plate}>
        {shown ? (
          <img
            class="canvas-img"
            src={shown.src}
            width={512}
            height={512}
            decoding="async"
            alt={alt}
          />
        ) : (
          <div class="canvas-grid" aria-hidden="true" />
        )}
        {blackout ? (
          <div class="canvas-note is-dark" aria-live="polite">
            <span class="canvas-note-title">Panel dark: scheduled blackout</span>
            {shown ? <span class="canvas-note-sub">Last published frame shown dimmed; the physical screen is off.</span> : null}
          </div>
        ) : note ? (
          <div class="canvas-note" aria-live="polite">
            <span class="canvas-note-title">{note}</span>
          </div>
        ) : null}
      </div>
      <figcaption class="canvas-meta">
        {frame ? (
          <>
            <span class="canvas-seq">#{frame.sequence}</span>
            <span class="canvas-persona">{frame.persona_id}</span>
            <span class="canvas-age">{since(frame.created_at)}</span>
            {frame.published ? null : <span class="canvas-warn">not published</span>}
            {awaitingNewer ? <span class="canvas-age">decoding newer frame</span> : null}
            {loadFailed ? <span class="canvas-error">frame object could not be loaded</span> : null}
            {frame.publish_error ? <span class="canvas-error">{frame.publish_error}</span> : null}
          </>
        ) : (
          <span class="canvas-age">no frame</span>
        )}
      </figcaption>
    </figure>
  );
}
