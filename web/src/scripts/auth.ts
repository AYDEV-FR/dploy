const TOKEN_KEY = 'dploy_token';

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

export function isAuthenticated(): boolean {
  return !!getToken();
}

export function usernameFromToken(): string {
  const token = getToken();
  if (!token) return '';
  try {
    const payload = JSON.parse(atob(token.split('.')[1] ?? ''));
    return payload.name || payload.preferred_username || payload.email || payload.sub || 'User';
  } catch {
    return 'User';
  }
}

/** Pick up an OIDC token handed back in the URL hash (#token=...). */
export function consumeHashToken(): void {
  const hash = window.location.hash;
  if (hash.startsWith('#token=')) {
    setToken(decodeURIComponent(hash.slice('#token='.length)));
    history.replaceState({}, document.title, window.location.pathname + window.location.search);
  }
}

export function login(returnPath: string = window.location.pathname): void {
  window.location.href = `/auth/login?returnUrl=${encodeURIComponent(returnPath)}`;
}

export function logout(): void {
  clearToken();
  window.location.href = '/';
}
