import { useRef } from 'preact/hooks';
import { Empty, Failure, Loading, Panel } from './ui';
import { objectURL } from '../api/client';
import type { Frame } from '../api/types';
import { since } from '../lib/format';

export interface FrameArchiveProps {
  frames: Frame[];
  loading: boolean;
  error?: unknown;
  onRetry: () => void;
  selectedID?: number;
  onSelect: (frame: Frame) => void;
}

export function FrameArchive(props: FrameArchiveProps) {
  const { frames, loading, error, onRetry, selectedID, onSelect } = props;
  const strip = useRef<HTMLDivElement>(null);

  const meta = (
    <span class="rec-meta">
      {frames.length} archived · 64×64 at 2×
    </span>
  );

  return (
    <Panel title="Frame archive" meta={meta}>
      {error === undefined || error === null ? null : <Failure error={error} retry={onRetry} />}
      {loading ? <Loading>Loading frame archive…</Loading> : null}
      {!loading && frames.length === 0 ? <Empty>No archived frames yet.</Empty> : null}
      {frames.length === 0 ? null : (
        <div
          class="frames-strip"
          ref={strip}
          onKeyDown={(event) => {
            if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
            const host = strip.current;
            if (host === null) return;
            const thumbs = Array.from(host.querySelectorAll<HTMLButtonElement>('button.frame-thumb'));
            const current = thumbs.indexOf(document.activeElement as HTMLButtonElement);
            if (current < 0) return;
            const next = thumbs[current + (event.key === 'ArrowRight' ? 1 : -1)];
            if (next === undefined) return;
            event.preventDefault();
            next.focus();
            next.scrollIntoView({ block: 'nearest', inline: 'nearest' });
          }}
        >
          {frames.map((frame) => {
            const source = objectURL(frame.final_object);
            const age = since(frame.created_at);
            const state = frame.publish_error ? ', publish failed' : frame.published ? '' : ', unpublished';
            return (
              <div class="frame-cell" key={frame.id}>
                <button
                  type="button"
                  class="frame-thumb"
                  aria-pressed={selectedID === frame.id}
                  aria-label={`Frame #${frame.sequence} by ${frame.persona_id}, ${age} old${state}`}
                  onClick={() => onSelect(frame)}
                >
                  <img
                    class="frame-img"
                    src={source}
                    width={128}
                    height={128}
                    alt=""
                    loading="lazy"
                    decoding="async"
                  />
                </button>
                <div class="frame-facts">
                  <span class="frame-seq">#{frame.sequence}</span>
                  <span class="frame-persona" title={frame.persona_id}>
                    {frame.persona_id}
                  </span>
                  <span>{age}</span>
                  {frame.publish_error ? (
                    <span class="frame-mark-bad" title={frame.publish_error}>
                      publish failed
                    </span>
                  ) : frame.published ? null : (
                    <span class="frame-mark-warn">unpublished</span>
                  )}
                  <a class="frame-open" href={source} target="_blank" rel="noreferrer">
                    open
                  </a>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </Panel>
  );
}
