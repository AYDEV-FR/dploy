const API_BASE = '';
const TOKEN_KEY = 'dploy_token';

// Icon mapping
const ICONS = {
    terminal: '💻',
    desktop: '🖥️',
    code: '📝',
    book: '📚',
    box: '📦',
    web: '🌍',
    default: '🚀'
};

// Initialize
window.addEventListener('DOMContentLoaded', () => {
    // Check for token in hash fragment (OAuth callback)
    const hash = window.location.hash;
    if (hash && hash.startsWith('#token=')) {
        const token = hash.substring(7); // Remove '#token='
        console.log('Token found in hash, storing in localStorage');
        localStorage.setItem(TOKEN_KEY, token);
        // Clean URL
        window.history.replaceState({}, document.title, '/');
    }

    // Check if user has token
    const token = localStorage.getItem(TOKEN_KEY);
    if (token) {
        showMainContent();
        loadEnvironments();
    } else {
        showLoginForm();
    }
});

function login() {
    window.location.href = '/auth/login';
}

function logout() {
    localStorage.removeItem(TOKEN_KEY);
    window.location.href = '/';
}

function showLoginForm() {
    document.getElementById('login-form').style.display = 'block';
    document.getElementById('user-info').style.display = 'none';
    document.getElementById('main-content').style.display = 'none';
    document.getElementById('login-required').style.display = 'flex';
}

function showMainContent() {
    document.getElementById('login-form').style.display = 'none';
    document.getElementById('user-info').style.display = 'flex';
    document.getElementById('main-content').style.display = 'block';
    document.getElementById('login-required').style.display = 'none';

    // Extract username from JWT
    const token = localStorage.getItem(TOKEN_KEY);
    try {
        const payload = JSON.parse(atob(token.split('.')[1]));
        const username = payload.name || payload.email || payload.sub || 'User';
        document.getElementById('username').textContent = username;
    } catch (e) {
        document.getElementById('username').textContent = 'User';
    }
}

// API Calls
async function apiCall(endpoint, options = {}) {
    const token = localStorage.getItem(TOKEN_KEY);
    const headers = {
        'Content-Type': 'application/json',
        ...options.headers
    };

    // Add Authorization header if token exists and endpoint requires auth
    if (token && !endpoint.includes('/available')) {
        headers['Authorization'] = `Bearer ${token}`;
    }

    try {
        const response = await fetch(API_BASE + endpoint, {
            ...options,
            headers
        });

        if (response.status === 401) {
            showToast('Authentication failed - please login', 'error');
            localStorage.removeItem(TOKEN_KEY);
            setTimeout(() => {
                window.location.href = '/';
            }, 1500);
            return null;
        }

        if (!response.ok && response.status !== 204) {
            const error = await response.json();
            throw new Error(error.error || 'Request failed');
        }

        if (response.status === 204) {
            return null;
        }

        return await response.json();
    } catch (error) {
        showToast(error.message, 'error');
        throw error;
    }
}

// Load Environments
async function loadEnvironments() {
    await Promise.all([
        loadAvailableEnvironments(),
        loadUserEnvironments()
    ]);
}

async function loadAvailableEnvironments() {
    const container = document.getElementById('available-environments');
    container.innerHTML = '<p class="loading">Loading...</p>';

    try {
        const envs = await apiCall('/api/environments/available');

        if (!envs || envs.length === 0) {
            container.innerHTML = '<p class="empty-state">No environments available</p>';
            return;
        }

        container.innerHTML = envs.map(env => `
            <div class="env-card">
                <div class="env-card-header">
                    <div class="env-title">
                        <span class="env-icon">${ICONS[env.icon] || ICONS.default}</span>
                        <span class="env-name">${env.name}</span>
                    </div>
                </div>
                <div class="env-description">${env.description}</div>
                <div class="env-actions">
                    <button onclick="launchEnvironment('${env.name}')" class="btn-small btn-primary">
                        🚀 Launch
                    </button>
                </div>
            </div>
        `).join('');
    } catch (error) {
        container.innerHTML = '<p class="error-state">⚠️ Failed to load environments</p>';
    }
}

async function loadUserEnvironments() {
    const container = document.getElementById('active-environments');
    const counterElement = document.getElementById('env-counter');
    container.innerHTML = '<p class="loading">Loading...</p>';

    try {
        const data = await apiCall('/api/environments');

        // Update counter
        if (counterElement) {
            const percentage = data.limit > 0 ? (data.count / data.limit * 100) : 0;
            const isNearLimit = percentage >= 80;
            counterElement.innerHTML = `
                <div class="counter-content ${isNearLimit ? 'near-limit' : ''}">
                    <span class="counter-value">${data.count}</span>
                    <span class="counter-separator">/</span>
                    <span class="counter-limit">${data.limit}</span>
                    <span class="counter-label">environments</span>
                </div>
            `;
        }

        if (!data.environments || data.environments.length === 0) {
            container.innerHTML = '<p class="empty-state">🚀 No active environments yet. Launch one below!</p>';
            return;
        }

        container.innerHTML = data.environments.map(env => {
            // Parse ISO 8601 timestamp
            const expiresAt = new Date(env.expiresAt);
            const timeLeft = getTimeLeft(expiresAt);
            const statusClass = env.status.toLowerCase();

            return `
                <div class="env-list-item">
                    <div class="env-list-main">
                        <div class="env-list-icon">${ICONS[env.icon] || ICONS.default}</div>
                        <div class="env-list-info">
                            <div class="env-list-header">
                                <span class="env-list-name">${env.name}</span>
                                <span class="env-status status-${statusClass}">${env.status}</span>
                            </div>
                            <a href="${env.url}" target="_blank" class="env-list-url">${env.url}</a>
                            <div class="env-list-meta">
                                <span class="meta-badge">
                                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                        <rect x="3" y="3" width="18" height="18" rx="2" ry="2"/>
                                        <line x1="9" y1="3" x2="9" y2="21"/>
                                    </svg>
                                    ${env.uuid}
                                </span>
                                <span class="meta-badge">
                                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                        <circle cx="12" cy="12" r="10"/>
                                        <polyline points="12 6 12 12 16 14"/>
                                    </svg>
                                    ${timeLeft}
                                </span>
                            </div>
                        </div>
                    </div>
                    <div class="env-list-actions">
                        <button onclick="openEnvironment('${env.url}')" class="btn-icon-action" title="Open">
                            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>
                                <polyline points="15 3 21 3 21 9"/>
                                <line x1="10" y1="14" x2="21" y2="3"/>
                            </svg>
                        </button>
                        <button onclick="extendEnvironment('${env.name}')" class="btn-icon-action" title="Extend">
                            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                <circle cx="12" cy="12" r="10"/>
                                <polyline points="12 6 12 12 16 14"/>
                            </svg>
                        </button>
                        <button onclick="deleteEnvironment('${env.name}')" class="btn-icon-action btn-danger-icon" title="Delete">
                            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                <polyline points="3 6 5 6 21 6"/>
                                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                            </svg>
                        </button>
                    </div>
                </div>
            `;
        }).join('');
    } catch (error) {
        container.innerHTML = '<p class="error-state">⚠️ Failed to load your environments</p>';
    }
}

// Actions
async function launchEnvironment(name) {
    showToast(`Launching ${name}...`, 'success');

    try {
        const result = await apiCall(`/api/run/${name}`);
        showToast(`Environment ${name} is ${result.status}`, 'success');

        // Wait a bit and reload if pending
        if (result.status === 'pending') {
            setTimeout(() => {
                pollEnvironmentStatus(name);
            }, 2000);
        }

        await loadUserEnvironments();
    } catch (error) {
        showToast(`Failed to launch ${name}`, 'error');
    }
}

async function pollEnvironmentStatus(name) {
    let attempts = 0;
    const maxAttempts = 30; // 1 minute

    const interval = setInterval(async () => {
        attempts++;

        try {
            const status = await apiCall(`/api/run/${name}/status`);

            if (status.status === 'healthy') {
                clearInterval(interval);
                showToast(`${name} is ready!`, 'success');
                await loadUserEnvironments();
            } else if (status.status === 'error' || status.status === 'degraded') {
                clearInterval(interval);
                showToast(`${name} failed to start`, 'error');
                await loadUserEnvironments();
            } else if (attempts >= maxAttempts) {
                clearInterval(interval);
                showToast(`${name} is taking longer than expected`, 'error');
                await loadUserEnvironments();
            }
        } catch (error) {
            clearInterval(interval);
        }
    }, 2000);
}

function openEnvironment(url) {
    window.open(url, '_blank');
}

async function extendEnvironment(name) {
    try {
        const result = await apiCall(`/api/run/${name}/extend`, { method: 'POST' });
        // Parse ISO 8601 timestamp
        const newExpiresAt = new Date(result.expiresAt);
        showToast(`Extended until ${newExpiresAt.toLocaleString()}`, 'success');
        await loadUserEnvironments();
    } catch (error) {
        // Error already shown by apiCall
    }
}

async function deleteEnvironment(name) {
    if (!confirm(`Delete environment ${name}?`)) {
        return;
    }

    try {
        await apiCall(`/api/run/${name}`, { method: 'DELETE' });
        showToast(`Environment ${name} deleted`, 'success');
        await loadUserEnvironments();
    } catch (error) {
        // Error already shown by apiCall
    }
}

// Helpers
function getTimeLeft(expiresAt) {
    const now = new Date();
    const diff = expiresAt - now;

    if (diff < 0) {
        return 'Expired';
    }

    const hours = Math.floor(diff / (1000 * 60 * 60));
    const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));

    if (hours > 0) {
        return `${hours}h ${minutes}m`;
    }
    return `${minutes}m`;
}

function showToast(message, type = 'success') {
    const toast = document.getElementById('toast');
    toast.textContent = message;
    toast.className = `toast ${type} show`;

    setTimeout(() => {
        toast.classList.remove('show');
    }, 3000);
}
