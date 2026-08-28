import { Fragment } from 'preact';
import type { ComponentChildren, JSX } from 'preact';
import { useCallback, useEffect, useId, useRef, useState } from 'preact/hooks';

export function Panel(props: {
  title: string;
  meta?: ComponentChildren;
  children: ComponentChildren;
  className?: string;
  scroll?: boolean;
}): JSX.Element {
  return (
    <section class={`panel ${props.className ?? ''}`}>
      <header class="panel-head">
        <span class="label">{props.title}</span>
        {props.meta ? <div class="meta">{props.meta}</div> : null}
      </header>
      <div class={`panel-body ${props.scroll ? 'is-scroll' : ''}`}>{props.children}</div>
    </section>
  );
}

export function Label(props: { children: ComponentChildren }): JSX.Element {
  return <span class="label">{props.children}</span>;
}

export function Empty(props: { children: ComponentChildren }): JSX.Element {
  return <p class="empty">{props.children}</p>;
}

export function Loading(props: { children?: ComponentChildren }): JSX.Element {
  return (
    <p class="loading" aria-live="polite">
      {props.children ?? 'Loading'}
    </p>
  );
}

export function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === 'string') return error;
  return 'Unexpected failure';
}

export function Failure(props: { error: unknown; retry?: () => void }): JSX.Element {
  return (
    <div class="failure" role="status">
      <span class="text">{errorText(props.error)}</span>
      {props.retry ? (
        <button type="button" onClick={props.retry}>
          retry
        </button>
      ) : null}
    </div>
  );
}

export function ActionButton(props: {
  onPress: () => Promise<unknown>;
  children: ComponentChildren;
  danger?: boolean;
  confirm?: string;
  title?: string;
  disabled?: boolean;
}): JSX.Element {
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<string | undefined>(undefined);

  const press = useCallback(async () => {
    if (props.confirm && !window.confirm(props.confirm)) return;
    setPending(true);
    setFailure(undefined);
    try {
      await props.onPress();
    } catch (cause) {
      setFailure(errorText(cause));
    } finally {
      setPending(false);
    }
  }, [props]);

  return (
    <span class="action">
      <button
        type="button"
        title={props.title}
        disabled={pending || props.disabled}
        aria-busy={pending}
        class={`${props.danger ? 'is-danger' : ''} ${pending ? 'is-pending' : ''}`}
        onClick={() => void press()}
      >
        {pending ? 'working' : props.children}
      </button>
      {failure ? (
        <span class="action-error" role="status">
          {failure}
        </span>
      ) : null}
    </span>
  );
}

const FOCUSABLE = 'a[href],button:not([disabled]),select,input,textarea,summary,[tabindex]:not([tabindex="-1"])';

export function Modal(props: {
  open: boolean;
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: ComponentChildren;
  wide?: boolean;
}): JSX.Element | null {
  const surface = useRef<HTMLDivElement>(null);
  const restore = useRef<HTMLElement | null>(null);
  const titleID = useId();
  const { open, onClose } = props;

  useEffect(() => {
    if (!open) return undefined;
    restore.current = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    surface.current?.querySelector<HTMLElement>(FOCUSABLE)?.focus();

    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.stopPropagation();
        onClose();
        return;
      }
      if (event.key !== 'Tab' || !surface.current) return;
      const nodes = Array.from(surface.current.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
        (node) => node.offsetParent !== null,
      );
      if (nodes.length === 0) return;
      const first = nodes[0];
      const last = nodes[nodes.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.body.style.overflow = overflow;
      restore.current?.focus();
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      class="scrim"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div class={`modal ${props.wide ? 'is-wide' : ''}`} role="dialog" aria-modal="true" aria-labelledby={titleID} ref={surface}>
        <header class="modal-head">
          <div>
            <h2 id={titleID}>{props.title}</h2>
            {props.subtitle ? <div class="subtitle mono">{props.subtitle}</div> : null}
          </div>
          <button type="button" onClick={onClose} aria-label="Close dialog">
            close
          </button>
        </header>
        <div class="modal-body">{props.children}</div>
      </div>
    </div>
  );
}

export function Disclosure(props: {
  summary: string;
  children: ComponentChildren;
  tone?: 'default' | 'quiet';
}): JSX.Element {
  return (
    <details class={`disclosure ${props.tone === 'quiet' ? 'is-quiet' : ''}`}>
      <summary>{props.summary}</summary>
      {props.children}
    </details>
  );
}

export function CodeBlock(props: { text: string; clamp?: number; language?: string }): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const clamp = props.clamp ?? 0;
  const lines = props.text.split('\n');
  const overflows = clamp > 0 && lines.length > clamp;
  const shown = overflows && !expanded ? lines.slice(0, clamp).join('\n') : props.text;

  return (
    <>
      <pre class={`code ${expanded ? 'is-expanded' : ''}`} data-language={props.language}>
        {shown}
      </pre>
      {overflows ? (
        <button type="button" class="code-expand" onClick={() => setExpanded(!expanded)}>
          {expanded ? 'collapse' : `show all ${lines.length} lines`}
        </button>
      ) : null}
    </>
  );
}

export function Meter(props: { label: string; used: number; limit: number; suffix?: string }): JSX.Element {
  const limit = props.limit > 0 ? props.limit : 0;
  const ratio = limit > 0 ? props.used / limit : 0;
  const percent = Math.max(0, Math.min(100, ratio * 100));
  const tone = ratio >= 1 ? 'is-bad' : ratio >= 0.8 ? 'is-warn' : '';
  const format = new Intl.NumberFormat();

  return (
    <div class={`meter ${tone}`}>
      <div class="meter-head">
        <span>{props.label}</span>
        <span class="value">
          {format.format(Math.trunc(props.used))}
          {limit > 0 ? ` / ${format.format(Math.trunc(limit))}` : ''}
          {props.suffix ?? ''}
        </span>
      </div>
      <div
        class="meter-track"
        role="meter"
        aria-label={props.label}
        aria-valuenow={Math.trunc(props.used)}
        aria-valuemin={0}
        aria-valuemax={Math.trunc(limit)}
      >
        <div class="meter-fill" style={{ width: `${percent}%` }} />
      </div>
    </div>
  );
}

export function Rows(props: { items: Array<[string, ComponentChildren]> }): JSX.Element {
  return (
    <dl class="rows">
      {props.items.map(([key, value], index) => (
        <Fragment key={`${index}-${key}`}>
          <dt>{key}</dt>
          <dd>{value}</dd>
        </Fragment>
      ))}
    </dl>
  );
}
