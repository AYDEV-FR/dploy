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
        window.history.replaceState({}, document.title, window.location.pathname);
    }

    // Check if user has token
    const token = localStorage.getItem(TOKEN_KEY);
    if (token) {
        showMainContent();
        loadPageContent();
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

// Load page-specific content
function loadPageContent() {
    const page = window.currentPage || 'environments';

    if (page === 'catalog') {
        loadAvailableEnvironments();
    } else {
        loadUserEnvironments();
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

// Parse category string into { category, subcategory }
function parseCategory(categoryStr) {
    if (!categoryStr) {
        return { category: 'default', subcategory: null };
    }
    const parts = categoryStr.split(',');
    return {
        category: parts[0] || 'default',
        subcategory: parts[1] || null
    };
}

// Group environments by category and subcategory
function groupEnvironments(envs) {
    const groups = {};

    envs.forEach(env => {
        const { category, subcategory } = parseCategory(env.category);

        if (!groups[category]) {
            groups[category] = { direct: [], subcategories: {} };
        }

        if (subcategory) {
            if (!groups[category].subcategories[subcategory]) {
                groups[category].subcategories[subcategory] = [];
            }
            groups[category].subcategories[subcategory].push(env);
        } else {
            groups[category].direct.push(env);
        }
    });

    return groups;
}

// Render environment card
function renderEnvCard(env) {
    // Build TTL display for catalog
    let ttlBadge = '';
    if (env.isUnlimited) {
        ttlBadge = `
            <span class="catalog-badge catalog-unlimited">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M18.178 8c5.096 0 5.096 8 0 8-5.095 0-7.133-8-12.739-8-4.585 0-4.585 8 0 8 5.606 0 7.644-8 12.74-8z"/>
                </svg>
                Unlimited
            </span>
        `;
    } else {
        const duration = formatDuration(env.ttl);
        ttlBadge = `
            <span class="catalog-badge catalog-ttl">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <circle cx="12" cy="12" r="10"/>
                    <polyline points="12 6 12 12 16 14"/>
                </svg>
                ${duration}
            </span>
        `;
    }

    // Build extend info badge
    let extendBadge = '';
    if (!env.isUnlimited && (env.extendTTL > 0 || env.maxExtends > 0)) {
        const extendDuration = env.extendTTL > 0 ? formatDuration(env.extendTTL) : '';
        const maxInfo = env.maxExtends > 0 ? `${env.maxExtends}x` : '';
        const extendText = extendDuration && maxInfo ? `+${extendDuration} (${maxInfo})` :
                          extendDuration ? `+${extendDuration}` :
                          maxInfo ? `Extend: ${maxInfo}` : '';
        if (extendText) {
            extendBadge = `
                <span class="catalog-badge catalog-extend">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83"/>
                    </svg>
                    ${extendText}
                </span>
            `;
        }
    }

    return `
        <div class="env-card">
            <div class="env-card-icon">
                <span class="env-icon">${ICONS[env.icon] || ICONS.default}</span>
            </div>
            <div class="env-card-content">
                <div class="env-name">${env.name}</div>
                <div class="env-description">${env.description}</div>
                <div class="env-card-badges">
                    ${ttlBadge}
                    ${extendBadge}
                </div>
            </div>
            <div class="env-card-action">
                <a href="/run/${env.name}" class="btn-launch">
                    Launch
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M5 12h14M12 5l7 7-7 7"/>
                    </svg>
                </a>
            </div>
        </div>
    `;
}

// Generate slug for anchor IDs
function slugify(text) {
    return text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/(^-|-$)/g, '');
}

// Render Table of Contents
function renderTOC(groups, categoryNames) {
    const tocContainer = document.querySelector('#catalog-toc .toc-list');
    if (!tocContainer) return;

    let tocHtml = '';

    categoryNames.forEach(categoryName => {
        const group = groups[categoryName];
        const displayName = categoryName === 'default' ? 'Other' : categoryName.charAt(0).toUpperCase() + categoryName.slice(1);
        const categorySlug = slugify(categoryName);

        tocHtml += `<li class="toc-category">`;
        tocHtml += `<a href="#cat-${categorySlug}" class="toc-category-link">${displayName}</a>`;

        // Add subcategories
        const subcategoryNames = Object.keys(group.subcategories).sort();
        if (subcategoryNames.length > 0) {
            tocHtml += `<ul class="toc-subcategories">`;
            subcategoryNames.forEach(subcategoryName => {
                const subDisplayName = subcategoryName.charAt(0).toUpperCase() + subcategoryName.slice(1);
                const subSlug = slugify(subcategoryName);
                tocHtml += `<li><a href="#cat-${categorySlug}-${subSlug}" class="toc-subcategory-link">${subDisplayName}</a></li>`;
            });
            tocHtml += `</ul>`;
        }

        tocHtml += `</li>`;
    });

    tocContainer.innerHTML = tocHtml;
}

// Load Available Environments (Catalog)
async function loadAvailableEnvironments() {
    const container = document.getElementById('available-environments');
    if (!container) return;

    container.innerHTML = '<p class="loading">Loading...</p>';

    try {
        const envs = await apiCall('/api/environments/available');

        if (!envs || envs.length === 0) {
            container.innerHTML = '<p class="empty-state">No templates available</p>';
            return;
        }

        const groups = groupEnvironments(envs);
        let html = '';

        // Sort categories: 'default' last, others alphabetically
        const categoryNames = Object.keys(groups).sort((a, b) => {
            if (a === 'default') return 1;
            if (b === 'default') return -1;
            return a.localeCompare(b);
        });

        // Render TOC
        renderTOC(groups, categoryNames);

        categoryNames.forEach(categoryName => {
            const group = groups[categoryName];
            const displayName = categoryName === 'default' ? 'Other' : categoryName.charAt(0).toUpperCase() + categoryName.slice(1);
            const categorySlug = slugify(categoryName);

            html += `<div id="cat-${categorySlug}" class="category-section">`;
            html += `<h3 class="category-title">${displayName}</h3>`;

            // Render direct items (no subcategory)
            if (group.direct.length > 0) {
                html += `<div class="env-grid">`;
                html += group.direct.map(renderEnvCard).join('');
                html += `</div>`;
            }

            // Render subcategories
            const subcategoryNames = Object.keys(group.subcategories).sort();
            subcategoryNames.forEach(subcategoryName => {
                const items = group.subcategories[subcategoryName];
                const subDisplayName = subcategoryName.charAt(0).toUpperCase() + subcategoryName.slice(1);
                const subSlug = slugify(subcategoryName);

                html += `<div id="cat-${categorySlug}-${subSlug}" class="subcategory-section">`;
                html += `<h4 class="subcategory-title">${subDisplayName}</h4>`;
                html += `<div class="env-grid">`;
                html += items.map(renderEnvCard).join('');
                html += `</div>`;
                html += `</div>`;
            });

            html += `</div>`;
        });

        container.innerHTML = html;
    } catch (error) {
        container.innerHTML = '<p class="error-state">Failed to load templates</p>';
    }
}

// Load User Environments
async function loadUserEnvironments() {
    const container = document.getElementById('active-environments');
    const counterElement = document.getElementById('env-counter');

    if (!container) return;

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
            container.innerHTML = `
                <div class="empty-state">
                    <p>No active environments yet</p>
                    <a href="/catalog" class="btn-nav" style="display: inline-block; margin-top: 1rem;">
                        Browse Catalog
                    </a>
                </div>
            `;
            return;
        }

        container.innerHTML = data.environments.map(env => {
            const statusClass = env.status.toLowerCase();

            // Build TTL display
            let ttlDisplay = '';
            let extendButton = '';

            if (env.isUnlimited) {
                ttlDisplay = `
                    <span class="meta-badge meta-unlimited">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <circle cx="12" cy="12" r="10"/>
                            <path d="M12 6v6l4 2"/>
                        </svg>
                        Unlimited
                    </span>
                `;
            } else {
                const expiresAt = new Date(env.expiresAt);
                const timeLeft = getTimeLeft(expiresAt);
                const isExpiringSoon = (expiresAt - new Date()) < 30 * 60 * 1000; // < 30 min

                ttlDisplay = `
                    <span class="meta-badge ${isExpiringSoon ? 'meta-warning' : ''}">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <circle cx="12" cy="12" r="10"/>
                            <polyline points="12 6 12 12 16 14"/>
                        </svg>
                        ${timeLeft}
                    </span>
                `;

                // Build extend info
                let extendInfo = '';
                if (env.maxExtends > 0) {
                    const remaining = env.maxExtends - env.extendCount;
                    extendInfo = `(${remaining}/${env.maxExtends} left)`;
                } else if (env.extendCount > 0) {
                    extendInfo = `(${env.extendCount}x extended)`;
                }

                // Check if can extend
                const canExtend = env.maxExtends <= 0 || env.extendCount < env.maxExtends;

                extendButton = `
                    <button onclick="extendEnvironment('${env.name}')"
                            class="btn-icon-action ${!canExtend ? 'btn-disabled' : ''}"
                            title="Extend ${extendInfo}"
                            ${!canExtend ? 'disabled' : ''}>
                        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <circle cx="12" cy="12" r="10"/>
                            <polyline points="12 6 12 12 16 14"/>
                        </svg>
                    </button>
                `;
            }

            return `
                <div class="env-list-item">
                    <div class="env-list-main">
                        <div class="env-list-icon">${ICONS[env.icon] || ICONS.default}</div>
                        <div class="env-list-info">
                            <div class="env-list-header">
                                <span class="env-list-name">${env.name}</span>
                                <span class="env-status status-${statusClass}">${env.status}</span>
                            </div>
                            <div class="env-list-description">${env.description}</div>
                            <a href="${env.url}" target="_blank" class="env-list-url">${env.url}</a>
                            <div class="env-list-meta">
                                <span class="meta-badge">
                                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                        <rect x="3" y="3" width="18" height="18" rx="2" ry="2"/>
                                        <line x1="9" y1="3" x2="9" y2="21"/>
                                    </svg>
                                    ${env.uuid}
                                </span>
                                ${ttlDisplay}
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
                        ${extendButton}
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
        container.innerHTML = '<p class="error-state">Failed to load your environments</p>';
    }
}

// Actions
async function launchEnvironment(name) {
    showToast(`Launching ${name}...`, 'success');

    try {
        const result = await apiCall(`/api/run/${name}`);
        showToast(`Environment ${name} is ${result.status}`, 'success');

        // Redirect to environments page if on catalog
        if (window.currentPage === 'catalog') {
            setTimeout(() => {
                window.location.href = '/';
            }, 1000);
            return;
        }

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

// Format duration from seconds to human readable
function formatDuration(seconds) {
    if (seconds < 0) return 'Unlimited';
    if (seconds === 0) return 'Default';

    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);

    if (days > 0) {
        return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
    }
    if (hours > 0) {
        return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
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
