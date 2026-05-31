import { api } from './api';
import { capitalize, esc, formatDuration, formatRemaining, getIcon, slugify } from './format';
import type { AvailableEnvironment, Environment } from './types';

const SVG = {
  open: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><path d="M15 3h6v6"/><path d="M10 14 21 3"/></svg>',
  clock: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>',
  trash: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m2 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/></svg>',
  arrow: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14M12 5l7 7-7 7"/></svg>',
  copy: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>',
};

/** True when the connection is a copyable command rather than a browser URL. */
function isInstructions(env: { connectionType?: string }): boolean {
  return env.connectionType === 'instructions';
}

/** The text to display/copy: the rendered instructions, falling back to the URL. */
function connectionText(env: { connectionMessage?: string; url?: string }): string {
  return env.connectionMessage || env.url || '';
}

/** Copy text to the clipboard and toast the outcome. */
async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    toast('Copied to clipboard.', 'success');
  } catch {
    toast('Copy failed — select the command and copy manually.', 'error');
  }
}

function $(sel: string): HTMLElement | null {
  return document.querySelector(sel);
}

/* ---------- Toasts ---------- */
export function toast(message: string, type: 'success' | 'error' = 'success'): void {
  const host = $('#toasts');
  if (!host) return;
  const el = document.createElement('div');
  el.className = `toast ${type}`;
  el.textContent = message;
  host.appendChild(el);
  setTimeout(() => el.remove(), 4000);
}

/* ---------- Environments ---------- */
export async function renderEnvironments(): Promise<void> {
  const list = $('#env-list');
  const counter = $('#env-counter');
  if (!list) return;
  list.innerHTML = '<div class="state">Loading…</div>';

  try {
    const data = await api.getUserEnvironments();
    const envs = data.environments || [];
    if (counter) counter.innerHTML = `<b>${envs.length}</b><span>/ ${esc(data.limit)} instances</span>`;

    if (envs.length === 0) {
      list.innerHTML = '<div class="state">No instances yet. Pick one from the catalog.</div>';
      return;
    }

    list.innerHTML = envs
      .map((env) => {
        const status = String(env.status || 'Unknown');
        const extendable = !env.isUnlimited && env.maxExtends > 0 && env.extendCount < env.maxExtends;
        const instructions = isInstructions(env);
        const conn = connectionText(env);
        const connBody = instructions
          ? `<code class="env-cmd">${esc(conn)}</code>`
          : `<a class="env-url" href="${esc(env.url)}" target="_blank" rel="noopener">${esc(env.url)}</a>`;
        const connAction = instructions
          ? `<button class="btn-icon" data-action="copy" data-copy="${esc(conn)}" title="Copy command">${SVG.copy}</button>`
          : `<a class="btn-icon" href="${esc(env.url)}" target="_blank" rel="noopener" title="Open">${SVG.open}</a>`;
        return `
        <div class="env-item">
          <div class="env-emoji">${getIcon(env.icon)}</div>
          <div class="env-main">
            <div class="env-row">
              <span class="env-name">${esc(env.name)}</span>
              <span class="status ${esc(status.toLowerCase())}">${esc(status)}</span>
              ${env.shared ? `<span class="badge accent">owned by ${esc(env.owner)}</span>` : ''}
            </div>
            ${connBody}
            <div class="env-meta">
              <span class="badge">${SVG.clock} ${esc(formatRemaining(env.expiresAt))}</span>
              ${env.isUnlimited ? '<span class="badge accent">∞ unlimited</span>' : ''}
              ${!env.isUnlimited && env.maxExtends > 0 ? `<span class="badge">${esc(env.extendCount)}/${esc(env.maxExtends)} extends</span>` : ''}
            </div>
          </div>
          <div class="env-actions">
            ${connAction}
            <button class="btn-icon" data-action="extend" data-name="${esc(env.name)}" ${extendable ? '' : 'disabled'} title="Extend TTL">${SVG.clock}</button>
            <button class="btn-icon danger" data-action="delete" data-name="${esc(env.name)}" title="Delete">${SVG.trash}</button>
          </div>
        </div>`;
      })
      .join('');
  } catch (err) {
    list.innerHTML = `<div class="state error">${esc((err as Error).message)}</div>`;
  }
}

async function onEnvAction(action: string, name: string): Promise<void> {
  try {
    if (action === 'extend') {
      await api.extend(name);
      toast(`Extended “${name}”.`, 'success');
    } else if (action === 'delete') {
      await api.remove(name);
      toast(`Deleted “${name}”.`, 'success');
    }
    await renderEnvironments();
  } catch (err) {
    toast((err as Error).message, 'error');
  }
}

/* ---------- Manager (admin) ---------- */

/** Compact, kubectl-style "since createdAt" age (e.g. "3h12m", "2d", "47s"). */
function ageSince(iso: string): string {
  if (!iso) return '—';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '—';
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h${m % 60 ? (m % 60) + 'm' : ''}`;
  return `${Math.floor(h / 24)}d`;
}

export async function renderManager(): Promise<void> {
  const list = $('#manager-list');
  const counter = $('#manager-counter');
  if (!list) return;
  list.innerHTML = '<div class="state">Loading…</div>';

  try {
    const data = await api.getAllInstances();
    const rows = data.instances || [];
    if (counter) counter.innerHTML = `<b>${rows.length}</b><span>total</span>`;

    if (rows.length === 0) {
      list.innerHTML = '<div class="state">No instances anywhere on the cluster.</div>';
      return;
    }
    list.innerHTML = `
      <div class="kube-table-wrap">
        <table class="kube-table">
          <thead>
            <tr>
              <th>NAME</th>
              <th>TEMPLATE</th>
              <th>OWNER</th>
              <th>PHASE</th>
              <th>URL</th>
              <th>EXPIRES</th>
              <th>AGE</th>
              <th>NAMESPACE</th>
              <th>UUID</th>
            </tr>
          </thead>
          <tbody>
            ${rows
              .map((r) => {
                const phaseCls = r.phase.toLowerCase();
                const owner = r.owner ? esc(r.owner) : '<span class="muted">—</span>';
                const url = r.url
                  ? `<a href="${esc(r.url)}" target="_blank" rel="noopener">${esc(r.url)}</a>`
                  : '<span class="muted">—</span>';
                const expires = r.isUnlimited
                  ? '<span class="badge accent">∞</span>'
                  : r.expiresAt
                    ? `<span title="${esc(r.expiresAt)}">${esc(formatRemaining(r.expiresAt))}</span>`
                    : '<span class="muted">—</span>';
                return `<tr>
                  <td><code>${esc(r.name)}</code></td>
                  <td>${esc(r.template)}</td>
                  <td>${owner}</td>
                  <td><span class="status ${esc(phaseCls)}">${esc(r.phase)}</span></td>
                  <td class="url-cell">${url}</td>
                  <td>${expires}</td>
                  <td><span title="${esc(r.createdAt)}">${esc(ageSince(r.createdAt))}</span></td>
                  <td>${r.namespace ? `<code>${esc(r.namespace)}</code>` : '<span class="muted">—</span>'}</td>
                  <td>${r.uuid ? `<code>${esc(r.uuid)}</code>` : '<span class="muted">—</span>'}</td>
                </tr>`;
              })
              .join('')}
          </tbody>
        </table>
      </div>`;
  } catch (err) {
    list.innerHTML = `<div class="state error">${esc((err as Error).message)}</div>`;
  }
}

/* ---------- Catalog ---------- */
interface Group {
  direct: AvailableEnvironment[];
  subs: Record<string, AvailableEnvironment[]>;
}

function groupByCategory(envs: AvailableEnvironment[]): Record<string, Group> {
  const groups: Record<string, Group> = {};
  for (const env of envs) {
    const [category = 'default', sub] = (env.category || '').split(',');
    (groups[category] ??= { direct: [], subs: {} });
    if (sub) (groups[category].subs[sub] ??= []).push(env);
    else groups[category].direct.push(env);
  }
  return groups;
}

function sortCategories(names: string[]): string[] {
  return [...names].sort((a, b) => (a === 'default' ? 1 : b === 'default' ? -1 : a.localeCompare(b)));
}

function cardHtml(env: AvailableEnvironment): string {
  const ttlBadge = env.isUnlimited
    ? '<span class="badge accent">∞ unlimited</span>'
    : `<span class="badge">${SVG.clock} ${esc(formatDuration(env.ttl))}</span>`;
  const extendBadge =
    !env.isUnlimited && (env.extendTTL > 0 || env.maxExtends > 0)
      ? `<span class="badge success">+${esc(formatDuration(env.extendTTL))}${env.maxExtends > 0 ? ` (${esc(env.maxExtends)}×)` : ''}</span>`
      : '';
  return `
    <div class="card">
      <div class="card-icon">${getIcon(env.icon)}</div>
      <div class="card-body">
        <div class="name">${esc(env.name)}</div>
        <div class="desc">${esc(env.description)}</div>
        <div class="card-badges">${ttlBadge}${extendBadge}</div>
      </div>
      <a class="btn btn-primary" data-link href="/run/${encodeURIComponent(env.name)}">Launch ${SVG.arrow}</a>
    </div>`;
}

export async function renderCatalog(): Promise<void> {
  const toc = $('#catalog-toc');
  const main = $('#catalog-main');
  if (!main) return;
  main.innerHTML = '<div class="state">Loading…</div>';
  if (toc) toc.innerHTML = '';

  try {
    const envs = (await api.getAvailable()) || [];
    if (envs.length === 0) {
      main.innerHTML = '<div class="state">No templates available.</div>';
      return;
    }

    const groups = groupByCategory(envs);
    const names = sortCategories(Object.keys(groups));

    if (toc) {
      toc.innerHTML =
        '<div class="toc-title">Categories</div>' +
        names
          .map((name) => {
            const slug = slugify(name);
            const display = name === 'default' ? 'Other' : capitalize(name);
            const subs = Object.keys(groups[name]!.subs).sort();
            return (
              `<a href="#cat-${slug}">${esc(display)}</a>` +
              subs.map((s) => `<a class="sub" href="#cat-${slug}-${slugify(s)}">${esc(capitalize(s))}</a>`).join('')
            );
          })
          .join('');
    }

    main.innerHTML = names
      .map((name) => {
        const group = groups[name]!;
        const slug = slugify(name);
        const display = name === 'default' ? 'Other' : capitalize(name);
        const subs = Object.keys(group.subs).sort();
        const direct = group.direct.length
          ? `<div class="card-grid">${group.direct.map(cardHtml).join('')}</div>`
          : '';
        const subSections = subs
          .map(
            (s) =>
              `<div class="cat-section" id="cat-${slug}-${slugify(s)}"><h4 class="sub-title">${esc(capitalize(s))}</h4><div class="card-grid">${group.subs[s]!.map(cardHtml).join('')}</div></div>`,
          )
          .join('');
        return `<div class="cat-section" id="cat-${slug}"><h3 class="cat-title">${esc(display)}</h3>${direct}${subSections}</div>`;
      })
      .join('');
  } catch (err) {
    main.innerHTML = `<div class="state error">${esc((err as Error).message)}</div>`;
  }
}

/* ---------- Run flow ---------- */
let pollTimer: number | null = null;

export function cancelRun(): void {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function runContent(html: string): void {
  const el = $('#run-content');
  if (el) el.innerHTML = html;
}

const deployingHtml = (uuid?: string) => `
  <div class="spinner"></div>
  <div class="status-text">Deploying…${uuid ? `<small>UUID: ${esc(uuid)}</small>` : ''}</div>
  <div class="progress"><div class="progress-fill"></div></div>`;

// Connection instructions (e.g. "ssh root@host -p 22000") shown when the instance
// is reachable by command rather than a browser URL — no redirect.
const instructionsHtml = (msg: string) => `
  <div class="status-text">✅ Instance ready! Connect with:</div>
  <div class="conn-cmd">
    <code>${esc(msg)}</code>
    <button class="btn-icon" data-action="copy" data-copy="${esc(msg)}" title="Copy command">${SVG.copy}</button>
  </div>`;

export async function runFlow(name: string): Promise<void> {
  cancelRun();
  const nameEl = $('#run-env-name');
  if (nameEl) nameEl.textContent = name;
  runContent(deployingHtml());

  try {
    const res = await api.run(name);
    runContent(deployingHtml(res.uuid));
  } catch (err) {
    runContent(`<div class="error-box">${esc((err as Error).message)}</div>
      <button class="btn btn-primary" onclick="location.reload()">Retry</button>`);
    return;
  }

  const poll = async () => {
    try {
      const res = await api.status(name);
      const s = res.status.toLowerCase();
      if (s === 'healthy') {
        cancelRun();
        if (isInstructions(res)) {
          runContent(instructionsHtml(connectionText(res)));
        } else {
          runContent('<div class="status-text">✅ Instance ready! Redirecting…</div>');
          if (res.url) setTimeout(() => (window.location.href = res.url), 1200);
        }
      } else if (s === 'degraded' || s === 'missing' || s === 'deleting') {
        cancelRun();
        runContent(`<div class="error-box">Instance is ${esc(res.status)}. Check the Flux logs.</div>
          <button class="btn btn-primary" onclick="location.reload()">Retry</button>`);
      }
    } catch {
      /* transient — keep polling */
    }
  };
  await poll();
  pollTimer = window.setInterval(poll, 2000);
}

/* ---------- Delegated actions ---------- */
export function wireEnvActions(): void {
  const list = $('#env-list');
  list?.addEventListener('click', (e) => {
    const btn = (e.target as HTMLElement).closest('button[data-action]') as HTMLButtonElement | null;
    if (!btn || btn.disabled || btn.dataset.action === 'copy') return;
    onEnvAction(btn.dataset.action!, btn.dataset.name!);
  });

  // Copy buttons may appear in the instances list and in the run view, so bind
  // their handler once at the document level.
  document.addEventListener('click', (e) => {
    const btn = (e.target as HTMLElement).closest('button[data-action="copy"]') as HTMLButtonElement | null;
    if (!btn) return;
    e.preventDefault();
    copyText(btn.dataset.copy ?? '');
  });
}
