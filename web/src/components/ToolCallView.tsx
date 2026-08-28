import '../styles/transcript.css';

import type { JSX } from 'preact';
import { useState } from 'preact/hooks';

import { duration } from '../lib/format';
import type { ToolCall } from '../lib/transcript';
import { toolTone } from '../lib/transcript';
import { CodeBlock, Label } from './ui';

/** Stream clamp: enough to read a failure without burying the rest of the row. */
const CLAMP_LINES = 14;
/** Input values longer than this collapse behind an expand control. */
const INLINE_VALUE = 160;

export interface ToolCallViewProps {
  tool: ToolCall;
  /** Trimmed padding for the deep dive, where the surrounding rows are denser. */
  compact?: boolean;
}

/**
 * The one tool-call renderer. The live transcript and the persona deep dive
 * share it so a command, its exit code and its streams read identically
 * wherever the operator finds them. The exit code rides in the status text
 * (`completed exit 1`) and tones the whole header through `toolTone`.
 */
export function ToolCallView(props: ToolCallViewProps): JSX.Element {
  const call = props.tool;
  return (
    <div class={props.compact === true ? 'tr-tool is-compact' : 'tr-tool'}>
      <div class="tr-tool-head">
        <span class="tr-tool-name">{call.name}</span>
        <span class={`tr-tool-status tr-tone-${toolTone(call)}`}>{call.status}</span>
        {call.durationMs === undefined ? null : <span class="tr-tool-time">{duration(call.durationMs)}</span>}
        {call.title === undefined ? null : <span class="tr-tool-title">{call.title}</span>}
      </div>
      {call.command === undefined ? null : (
        <div class="tr-command">
          <span class="tr-command-mark" aria-hidden="true">
            $
          </span>
          <pre>{call.command}</pre>
        </div>
      )}
      {call.input.length === 0 ? null : <Pairs rows={call.input} />}
      {call.result === undefined ? null : <Pairs rows={call.result} label="result" />}
      {call.error === undefined ? null : <p class="tr-tool-error">{call.error}</p>}
      {call.output === undefined ? null : <Stream label="output" text={call.output} />}
      {call.stderr === undefined ? null : <Stream label="stderr" text={call.stderr} bad />}
      {call.truncated === true ? <p class="tr-tool-note">the tool truncated this output</p> : null}
    </div>
  );
}

function Pairs(props: { rows: Array<[string, string]>; label?: string }): JSX.Element {
  return (
    <>
      {props.label === undefined ? null : <Label>{props.label}</Label>}
      <dl class="tr-kv">
        {props.rows.map(([key, value], index) => (
          <div class="tr-kv-row" key={`${index}-${key}`}>
            <dt>{key}</dt>
            <dd>
              <Value text={value} />
            </dd>
          </div>
        ))}
      </dl>
    </>
  );
}

function Stream(props: { label: string; text: string; bad?: boolean }): JSX.Element {
  return (
    <div class={props.bad === true ? 'tr-stream tr-stream-bad' : 'tr-stream'}>
      <Label>{props.label}</Label>
      <CodeBlock text={props.text} clamp={CLAMP_LINES} />
    </div>
  );
}

function Value(props: { text: string }): JSX.Element {
  const [open, setOpen] = useState(false);
  const long = props.text.length > INLINE_VALUE || props.text.includes('\n');
  if (!long) return <span class="tr-kv-inline">{props.text}</span>;
  if (open) {
    return (
      <div class="tr-kv-block">
        <CodeBlock text={props.text} clamp={CLAMP_LINES} />
        <button type="button" class="tr-more" onClick={() => setOpen(false)}>
          hide
        </button>
      </div>
    );
  }
  return (
    <button type="button" class="tr-more tr-kv-clamp" onClick={() => setOpen(true)}>
      <span class="tr-kv-preview">{props.text.replace(/\s+/g, ' ').trim().slice(0, INLINE_VALUE)}</span>
      <span class="tr-kv-expand">expand</span>
    </button>
  );
}
