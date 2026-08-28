import { describe, it, expect } from 'vitest';
import type { StewardEvent } from '../api/types';
import type { ToolCall, TranscriptItem } from './transcript';
import { groupByLease, toolTone, toTranscript } from './transcript';

let sequence = 0;

function event(type: string, payload: unknown, overrides: Partial<StewardEvent> = {}): StewardEvent {
  sequence += 1;
  return {
    id: sequence,
    at: new Date(Date.UTC(2026, 0, 1, 0, 0, sequence)).toISOString(),
    actor: 'agent',
    type,
    correlation_id: 'corr',
    payload,
    lease_id: 'lease_a',
    persona_id: 'persona_a',
    ...overrides,
  };
}

/** Asserts a row carries a tool call and narrows it for the assertions below. */
function toolOf(item: TranscriptItem | undefined): ToolCall {
  const tool = item?.tool;
  if (tool === undefined) throw new Error('expected a tool call on the row');
  return tool;
}

describe('toTranscript', () => {
  it('keeps model text verbatim, including newlines and HTML-looking characters', () => {
    const text = 'line one\n\n  indented <b>not markup</b> & "quoted"\n';
    const [item] = toTranscript([event('runtime.text', { type: 'text', part: { text } })]);
    expect(item.kind).toBe('text');
    expect(item.body).toBe(text);
    expect(item.title).toBe('model');
    expect(item.demoted).toBe(false);
  });

  it('drops empty text parts', () => {
    expect(toTranscript([event('runtime.text', { part: { text: '   \n' } })])).toHaveLength(0);
  });

  it('hoists the complete bash command and keeps other input keys', () => {
    const command = `for file in $(ls -1 /workspace/scenes); do printf '%s\\n' "$file"; done # ${'x'.repeat(400)}`;
    const [item] = toTranscript([
      event('runtime.tool_use', {
        part: { tool: 'bash', state: { status: 'running', input: { command, description: 'list scenes' } } },
      }),
    ]);
    expect(item.kind).toBe('tool');
    expect(item.tool?.name).toBe('bash');
    expect(item.tool?.command).toBe(command);
    expect(item.tool?.input).toEqual([['description', 'list scenes']]);
    expect(item.tool?.status).toBe('running');
  });

  it('splits stdout and stderr, surfaces the exit code and the duration', () => {
    const [item] = toTranscript([
      event('runtime.tool_use', {
        part: {
          tool: 'bash',
          state: {
            status: 'completed',
            input: { command: 'go test ./...' },
            output: 'ignored blob',
            metadata: { stdout: 'ok  pixel-steward\n', stderr: 'FAIL cache\n', exit: 1 },
            time: { start: 1_700_000_000_000, end: 1_700_000_002_500 },
          },
        },
      }),
    ]);
    expect(item.tool?.output).toBe('ok  pixel-steward\n');
    expect(item.tool?.stderr).toBe('FAIL cache\n');
    expect(item.tool?.status).toBe('completed exit 1');
    expect(item.tool?.durationMs).toBe(2500);
  });

  it('splits tagged stream output and reads flat pre-state tool shapes', () => {
    const [item] = toTranscript([
      event('runtime.tool_use', {
        part: {
          name: 'shell',
          status: 'completed',
          input: { command: 'echo hi' },
          output: '<stdout>\nhi\n</stdout>\n<stderr>\nwarn\n</stderr>',
        },
      }),
    ]);
    expect(item.tool?.name).toBe('shell');
    expect(item.tool?.output).toBe('hi');
    expect(item.tool?.stderr).toBe('warn');
  });

  it('reports tool errors and nested input as pretty JSON', () => {
    const [item] = toTranscript([
      event('runtime.tool_use', {
        part: { tool: 'draw', state: { input: { pixels: [1, 2] }, error: { message: 'canvas locked' } } },
      }),
    ]);
    expect(item.tool?.error).toBe('canvas locked');
    expect(item.tool?.status).toBe('error');
    expect(item.tool?.input).toEqual([['pixels', '[\n  1,\n  2\n]']]);
  });

  it('demotes step lifecycle events and keeps the token summary in the title', () => {
    const items = toTranscript([
      event('runtime.step_start', { part: {} }),
      event('runtime.step_finish', { part: { tokens: { input: 120, output: 340, reasoning: 12 }, cost: 0.0021 } }),
    ]);
    expect(items.map((item) => item.kind)).toEqual(['lifecycle', 'lifecycle']);
    expect(items.every((item) => item.demoted)).toBe(true);
    expect(items[1].title).toContain('340 out');
    expect(items[1].title).toContain('12 reasoning');
  });

  it('orders newest-first input chronologically with stable per-event keys', () => {
    const first = event('journal.entry', { entry: 'first' });
    const second = event('journal.entry', { entry: 'second' });
    const third = event('journal.entry', { entry: 'third' });
    const items = toTranscript([third, second, first]);
    expect(items.map((item) => item.body)).toEqual(['first', 'second', 'third']);
    expect(items.map((item) => item.key)).toEqual([String(first.id), String(second.id), String(third.id)]);
    expect(items[0].kind).toBe('journal');
    expect(items[0].demoted).toBe(false);

    const appended = toTranscript([event('journal.entry', { entry: 'fourth' }), third, second, first]);
    expect(appended.slice(0, 3).map((item) => item.key)).toEqual(items.map((item) => item.key));
  });

  it('degrades hostile payloads instead of throwing', () => {
    const items = toTranscript([
      event('runtime.tool_use', null),
      event('runtime.output', '{"broken'),
      event('journal.entry', 'not an object'),
      event('mystery.thing_happened', { whatever: true }),
      event('agent.wake.failed', { error: 'opencode exited: signal killed' }),
    ]);
    expect(items).toHaveLength(5);
    expect(items[0].tool).toEqual({ name: 'tool', input: [], status: 'pending' });
    expect(items[1].body).toBe('{"broken');
    expect(items[1].demoted).toBe(true);
    expect(items[2].body).toBe('');
    expect(items[3].kind).toBe('controller');
    expect(items[3].title).toBe('mystery · thing happened');
    expect(items[4].kind).toBe('error');
    expect(items[4].title).toBe('agent wake failed: opencode exited: signal killed');
    expect(items[4].raw).toBeDefined();
  });

  it('keeps controller detail such as the executed sandbox command', () => {
    const [item] = toTranscript([event('sandbox.exec', { command: 'convert scene.png -resize 64x64 out.png' })]);
    expect(item.kind).toBe('controller');
    expect(item.body).toBe('convert scene.png -resize 64x64 out.png');
  });

  it('decodes the live JSON-encoded exec result of a successful call', () => {
    const [item] = toTranscript([
      event('runtime.tool_use', {
        type: 'tool_use',
        part: {
          tool: 'studio_studio_exec',
          callID: 'c1',
          state: {
            status: 'completed',
            input: { command: 'python3 /w/scene.py', timeout_ms: 120000 },
            output: '{"exit_code":0,"stdout":"ok\\n","stderr":"","duration_ms":120}',
            metadata: { truncated: false },
            time: { start: 1787929290455, end: 1787929290886 },
          },
        },
      }),
    ]);
    const tool = toolOf(item);
    expect(tool.name).toBe('studio_studio_exec');
    expect(tool.command).toBe('python3 /w/scene.py');
    expect(tool.input).toEqual([['timeout_ms', '120000']]);
    expect(tool.output).toBe('ok\n');
    expect(tool.stderr).toBeUndefined();
    expect(tool.exitCode).toBe(0);
    expect(tool.durationMs).toBe(120);
    expect(tool.truncated).toBeUndefined();
    expect(tool.status).toBe('completed exit 0');
    expect(toolTone(tool)).toBe('ok');
    // The encoded envelope must never survive as the rendered output.
    expect(tool.output).not.toContain('exit_code');
    expect(JSON.stringify(tool)).not.toContain('exit_code');
  });

  it('decodes the live JSON-encoded exec result of a failing call and tones it bad', () => {
    const [item] = toTranscript([
      event('runtime.tool_use', {
        type: 'tool_use',
        part: {
          tool: 'studio_studio_exec',
          callID: 'c1',
          state: {
            status: 'completed',
            input: { command: 'python3 /w/scene.py', timeout_ms: 120000 },
            output:
              '{"exit_code":1,"stdout":"","stderr":"Traceback (most recent call last):\\n  File \\"/w/scene.py\\", line 3\\nValueError: bad palette\\n","duration_ms":409}',
            metadata: { truncated: false },
            time: { start: 1787929290455, end: 1787929290886 },
          },
        },
      }),
    ]);
    const tool = toolOf(item);
    expect(tool.exitCode).toBe(1);
    expect(tool.stderr).toContain('Traceback (most recent call last):');
    expect(tool.stderr).toContain('ValueError: bad palette');
    expect(tool.output).toBeUndefined();
    expect(tool.durationMs).toBe(409);
    expect(tool.status).toBe('completed exit 1');
    expect(toolTone(tool)).toBe('bad');
    expect(JSON.stringify(tool)).not.toContain('exit_code');
  });

  it('surfaces result keys beyond exit, stdout, stderr and duration', () => {
    const [item] = toTranscript([
      event('runtime.tool_use', {
        part: {
          tool: 'studio_studio_exec',
          state: {
            status: 'completed',
            input: { command: 'ls' },
            output: '{"exit_code":0,"stdout":"ok","duration_ms":5,"cwd":"/w","dropped_bytes":42}',
          },
        },
      }),
    ]);
    const tool = toolOf(item);
    expect(tool.output).toBe('ok');
    expect(tool.result).toEqual([
      ['cwd', '/w'],
      ['dropped_bytes', '42'],
    ]);
    expect(tool.input).toEqual([]);
  });

  it('keeps non-JSON tool output exactly as it arrived', () => {
    const items = toTranscript([
      event('runtime.tool_use', {
        part: { tool: 'bash', state: { status: 'completed', input: { command: 'echo hi' }, output: 'hi\nthere\n' } },
      }),
      event('runtime.tool_use', {
        part: { tool: 'bash', state: { status: 'completed', input: { command: 'echo hi' }, output: '{"exit_code":1' } },
      }),
    ]);
    expect(toolOf(items[0]).output).toBe('hi\nthere\n');
    expect(toolOf(items[0]).exitCode).toBeUndefined();
    expect(toolOf(items[0]).status).toBe('completed');
    expect(toolOf(items[1]).output).toBe('{"exit_code":1');
  });

  it('reports output the tool itself truncated', () => {
    const [item] = toTranscript([
      event('runtime.tool_use', {
        part: {
          tool: 'studio_studio_exec',
          state: {
            status: 'completed',
            input: { command: 'cat big.log' },
            output: '{"exit_code":0,"stdout":"first line\\n","stderr":"","duration_ms":8}',
            metadata: { truncated: true },
          },
        },
      }),
    ]);
    expect(toolOf(item).truncated).toBe(true);
    expect(toolOf(item).output).toBe('first line\n');
  });

  it('shows runtime errors as visible error rows instead of demoted lifecycle', () => {
    const [item] = toTranscript([
      event('runtime.error', {
        type: 'error',
        part: { error: 'model stream aborted: 502 from upstream\nrequest_id=abc123\nretrying' },
      }),
    ]);
    expect(item.kind).toBe('error');
    expect(item.demoted).toBe(false);
    expect(item.title).toBe('runtime error: model stream aborted: 502 from upstream');
    expect(item.body).toContain('request_id=abc123');
  });

  it('reads a runtime error message from every shape the runtime uses', () => {
    const items = toTranscript([
      event('runtime.error', { error: { message: 'sandbox unreachable' } }),
      event('runtime.error', { part: { message: 'tool bridge closed' } }),
      event('runtime.error', 'opencode exited with status 1'),
      event('runtime.error', { part: {} }),
    ]);
    expect(items.map((entry) => entry.kind)).toEqual(['error', 'error', 'error', 'error']);
    expect(items.every((entry) => !entry.demoted)).toBe(true);
    expect(items[0].title).toBe('runtime error: sandbox unreachable');
    expect(items[1].title).toBe('runtime error: tool bridge closed');
    expect(items[2].title).toBe('runtime error: opencode exited with status 1');
    expect(items[3].title).toBe('runtime error');
  });

  it('folds the authoritative sandbox result into the adjacent tool row', () => {
    const command = 'python3 /w/scene.py';
    const items = toTranscript([
      event('runtime.tool_use', {
        part: { tool: 'studio_studio_exec', state: { status: 'completed', input: { command }, output: '' } },
      }),
      event('sandbox.exec', {
        command,
        timeout_ms: 120000,
        result: { exit_code: 2, stdout: 'partial\n', stderr: 'boom\n', duration_ms: 409 },
      }),
    ]);
    expect(items).toHaveLength(1);
    const tool = toolOf(items[0]);
    expect(tool.name).toBe('studio_studio_exec');
    expect(tool.exitCode).toBe(2);
    expect(tool.output).toBe('partial\n');
    expect(tool.stderr).toBe('boom\n');
    expect(tool.durationMs).toBe(409);
    expect(tool.status).toBe('completed exit 2');
    expect(toolTone(tool)).toBe('bad');
  });

  it('renders an unmatched sandbox exec result as its own readable tool row', () => {
    const items = toTranscript([
      event('runtime.tool_use', {
        part: { tool: 'bash', state: { status: 'completed', input: { command: 'ls /w' }, output: 'scene.py\n' } },
      }),
      event('sandbox.exec', {
        command: 'convert scene.png -resize 64x64 out.png',
        timeout_ms: 30000,
        result: { exit_code: 0, stdout: 'done\n', stderr: '', duration_ms: 12, cwd: '/w' },
      }),
    ]);
    expect(items).toHaveLength(2);
    const tool = toolOf(items[1]);
    expect(items[1].kind).toBe('tool');
    expect(tool.name).toBe('sandbox.exec');
    expect(tool.command).toBe('convert scene.png -resize 64x64 out.png');
    expect(tool.input).toEqual([['timeout_ms', '30000']]);
    expect(tool.output).toBe('done\n');
    expect(tool.stderr).toBeUndefined();
    expect(tool.exitCode).toBe(0);
    expect(tool.durationMs).toBe(12);
    expect(tool.result).toEqual([['cwd', '/w']]);
    expect(tool.status).toBe('completed exit 0');
    expect(toolTone(tool)).toBe('ok');
  });

  it('keeps a failed sandbox exec error and tones it bad', () => {
    const [item] = toTranscript([
      event('sandbox.exec', {
        command: 'python3 /w/scene.py',
        result: { exit_code: 127, stdout: '', stderr: 'python3: not found\n', duration_ms: 3 },
        error: 'exec transport failed',
      }),
    ]);
    const tool = toolOf(item);
    expect(item.title).toBe('sandbox exec failed: exec transport failed');
    expect(tool.error).toBe('exec transport failed');
    expect(tool.stderr).toBe('python3: not found\n');
    expect(tool.exitCode).toBe(127);
    expect(tool.status).toBe('error exit 127');
    expect(toolTone(tool)).toBe('bad');
  });
});

describe('groupByLease', () => {
  it('splits on lease boundaries and keeps lease-less rows separate', () => {
    const items = toTranscript([
      event('journal.entry', { entry: 'a' }, { lease_id: 'lease_1', persona_id: 'p1' }),
      event('frame.submitted', { sequence: 3, published: true }, { lease_id: 'lease_1', persona_id: 'p1' }),
      event('controller.tick.error', { error: 'boom' }, { lease_id: undefined, persona_id: undefined }),
      event('journal.entry', { entry: 'b' }, { lease_id: 'lease_2', persona_id: 'p2' }),
    ]);
    const groups = groupByLease(items);
    expect(groups.map((group) => group.leaseID)).toEqual(['lease_1', undefined, 'lease_2']);
    expect(groups[0].items).toHaveLength(2);
    expect(groups[0].personaID).toBe('p1');
    expect(groups[0].startedAt).toBe(items[0].at);
    expect(groups[0].endedAt).toBe(items[1].at);
    expect(groups[2].items[0].body).toBe('b');
  });
});
