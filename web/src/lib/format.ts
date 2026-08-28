const numberFormat = new Intl.NumberFormat();

export function count(value: number | undefined | null): string {
  return numberFormat.format(Math.trunc(value ?? 0));
}

export function money(micros: number | undefined | null): string {
  return `$${((micros ?? 0) / 1_000_000).toFixed(4)}`;
}

export function clockTime(value: string | number | Date | undefined): string {
  if (value === undefined) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleTimeString([], { hour12: false });
}

export function stamp(value: string | number | Date | undefined): string {
  if (value === undefined) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString([], { hour12: false });
}

export function since(value: string | number | Date | undefined, now = Date.now()): string {
  if (value === undefined) return '—';
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return '—';
  return relative(Math.round((now - time) / 1000));
}

export function until(value: string | number | Date | undefined, now = Date.now()): string {
  if (value === undefined) return '—';
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return '—';
  return relative(Math.round((time - now) / 1000));
}

function relative(seconds: number): string {
  const abs = Math.abs(seconds);
  if (abs < 10) return 'just now';
  if (abs < 90) return `${abs}s`;
  if (abs < 5400) return `${Math.round(abs / 60)}m`;
  if (abs < 172800) return `${Math.round(abs / 3600)}h`;
  return `${Math.round(abs / 86400)}d`;
}

export function duration(ms: number | undefined): string {
  if (ms === undefined || Number.isNaN(ms)) return '—';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const minutes = Math.floor(ms / 60_000);
  return `${minutes}m ${Math.round((ms % 60_000) / 1000)}s`;
}

export function pretty(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}
