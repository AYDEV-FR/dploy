const ENTITIES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

/** Escape a value for safe interpolation into HTML (text or quoted attribute). */
export function esc(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, (c) => ENTITIES[c]!);
}

const ICONS: Record<string, string> = {
  terminal: '💻',
  desktop: '🖥️',
  code: '📝',
  book: '📚',
  box: '📦',
  web: '🌍',
  default: '🚀',
};

export function getIcon(icon?: string): string {
  return ICONS[icon || 'default'] || ICONS.default;
}

/** Seconds → "1h 30m" (−1 → ∞). */
export function formatDuration(seconds: number): string {
  if (seconds < 0) return '∞';
  if (seconds === 0) return '0m';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h && m) return `${h}h ${m}m`;
  if (h) return `${h}h`;
  return `${m}m`;
}

/** ISO timestamp → remaining time ("2h 58m", "expired"); empty → ∞. */
export function formatRemaining(iso: string): string {
  if (!iso) return '∞';
  const ms = new Date(iso).getTime() - Date.now();
  if (Number.isNaN(ms)) return '—';
  if (ms <= 0) return 'expired';
  return formatDuration(Math.floor(ms / 1000));
}

export function capitalize(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}

export function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/(^-|-$)/g, '');
}
