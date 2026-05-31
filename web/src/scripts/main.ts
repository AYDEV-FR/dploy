import { api } from './api';
import { consumeHashToken, isAuthenticated, login, logout, setToken, usernameFromToken } from './auth';
import type { Me, UIConfig } from './types';
import { cancelRun, renderCatalog, renderEnvironments, renderManager, runFlow, wireEnvActions } from './views';

type RouteName = 'home' | 'catalog' | 'manager' | 'run' | 'login';

// Defaults reflect a fully-enabled UI so the app stays usable even if the
// /api/ui-config call fails (network glitch, older API). Replaced at bootstrap.
let uiConfig: UIConfig = { catalogEnabled: true, instancesEnabled: true, managerEnabled: true };
// Discovered after auth; admin=false until /api/me succeeds, so the Manager
// link stays hidden by default — fail-closed for an admin affordance.
let me: Me | null = null;

/** Apply the feature flags + admin status to the nav and home-view DOM. Idempotent. */
function applyUIConfig(): void {
  const toggle = (id: string, hidden: boolean) => {
    const el = document.getElementById(id);
    if (el) el.hidden = hidden;
  };
  toggle('nav-catalog', !uiConfig.catalogEnabled);
  toggle('nav-instances', !uiConfig.instancesEnabled);
  toggle('btn-new-instance', !uiConfig.catalogEnabled);
  toggle('env-list', !uiConfig.instancesEnabled);
  toggle('env-list-disabled', uiConfig.instancesEnabled);
  // Manager link is admin-AND-feature-gated.
  toggle('nav-manager', !(uiConfig.managerEnabled && me?.admin));
}

const MOON =
  '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z"/></svg>';
const SUN =
  '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1 12h2M21 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4"/></svg>';

function show(view: RouteName): void {
  for (const name of ['login', 'home', 'catalog', 'manager', 'run'] as const) {
    const el = document.getElementById(`view-${name}`);
    if (el) el.hidden = name !== view;
  }
  document.body.dataset.route = view;
}

function setActiveNav(path: string | null): void {
  document.querySelectorAll<HTMLAnchorElement>('.nav-link').forEach((a) => {
    a.classList.toggle('active', path !== null && a.getAttribute('href') === path);
  });
}

function updateNav(authed: boolean): void {
  const user = document.getElementById('nav-user');
  const logoutBtn = document.getElementById('btn-logout');
  const loginBtn = document.getElementById('nav-login');
  if (user) {
    user.hidden = !authed;
    const label = user.querySelector('.username');
    if (label) label.textContent = usernameFromToken();
  }
  if (logoutBtn) logoutBtn.hidden = !authed;
  if (loginBtn) loginBtn.hidden = authed;
}

function currentRoute(): { name: RouteName; env?: string } {
  const path = window.location.pathname;
  const run = path.match(/^\/run\/(.+)$/);
  if (run) return { name: 'run', env: decodeURIComponent(run[1]!) };
  if (/^\/catalog\/?$/.test(path)) return { name: 'catalog' };
  if (/^\/manager\/?$/.test(path)) return { name: 'manager' };
  return { name: 'home' };
}

function route(): void {
  cancelRun();
  const r = currentRoute();
  const authed = isAuthenticated();
  updateNav(authed);

  if (!authed) {
    if (r.name === 'run') {
      login(window.location.pathname);
      return;
    }
    show('login');
    setActiveNav(null);
    return;
  }

  if (r.name === 'run') {
    show('run');
    setActiveNav(null);
    runFlow(r.env!);
  } else if (r.name === 'catalog') {
    // Catalog disabled by the API → bounce to the home view (which itself may
    // be the run-only fallback if instances are also disabled).
    if (!uiConfig.catalogEnabled) {
      navigate('/');
      return;
    }
    show('catalog');
    setActiveNav('/catalog');
    renderCatalog();
  } else if (r.name === 'manager') {
    // Manager is admin-only AND feature-gated; non-admin requests bounce home
    // before any admin API call (the API would 403, but no need to hit it).
    if (!uiConfig.managerEnabled || !me?.admin) {
      navigate('/');
      return;
    }
    show('manager');
    setActiveNav('/manager');
    renderManager();
  } else {
    show('home');
    setActiveNav('/');
    // Skip the instance fetch when listing is disabled; the static fallback
    // div in the home view (toggled by applyUIConfig) explains the deployment.
    if (uiConfig.instancesEnabled) renderEnvironments();
  }
}

function navigate(href: string): void {
  if (href !== window.location.pathname) history.pushState({}, '', href);
  route();
}

function applyThemeIcon(): void {
  const btn = document.getElementById('btn-theme');
  if (btn) btn.innerHTML = document.documentElement.dataset.theme === 'light' ? SUN : MOON;
}

function initInteractions(): void {
  // Client-side navigation for internal links.
  document.addEventListener('click', (e) => {
    const target = e.target as HTMLElement;
    const link = target.closest('a[data-link]') as HTMLAnchorElement | null;
    if (link) {
      const evt = e as MouseEvent;
      if (evt.metaKey || evt.ctrlKey || evt.shiftKey || link.target === '_blank') return;
      e.preventDefault();
      navigate(link.getAttribute('href')!);
      return;
    }
    if (target.closest('#nav-login, #login-btn')) {
      e.preventDefault();
      login();
    } else if (target.closest('#btn-logout')) {
      e.preventDefault();
      logout();
    } else if (target.closest('#btn-refresh')) {
      e.preventDefault();
      renderEnvironments();
    } else if (target.closest('#btn-manager-refresh')) {
      e.preventDefault();
      renderManager();
    } else if (target.closest('#btn-theme')) {
      e.preventDefault();
      const next = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light';
      document.documentElement.dataset.theme = next;
      localStorage.setItem('dploy-theme', next);
      applyThemeIcon();
    }
  });

  window.addEventListener('popstate', route);
}

consumeHashToken();
// Dev-only mock auth so `astro dev` shows data without manual setup.
// import.meta.env.DEV is false in production builds, so this is stripped out.
// Set localStorage 'dploy-demo'='off' to preview the logged-out (login) screen.
if (import.meta.env.DEV && localStorage.getItem('dploy-demo') !== 'off' && !isAuthenticated()) {
  setToken('eyJhbGciOiJub25lIn0.eyJuYW1lIjoiSm9obiBEb2UifQ==.sig');
}
applyThemeIcon();
wireEnvActions();
initInteractions();

// Fetch UI feature flags + (if authed) the requester's identity before the
// first route so disabled views never flash and admin affordances appear
// without a second paint. /api/me requires auth so it only runs when there's
// a token; otherwise admin stays false (fail-closed).
Promise.allSettled([
  api.getUIConfig().then((cfg) => {
    uiConfig = cfg;
  }),
  isAuthenticated()
    ? api.getMe().then((m) => {
        me = m;
      })
    : Promise.resolve(),
]).finally(() => {
  applyUIConfig();
  route();
});
