---
sidebar_position: 1
---

# Web UI Guide

Dploy includes an embedded web interface for managing environments without using the API directly.

## Accessing the UI

Open your browser and navigate to:

```
https://dploy.your-domain.com/
```

Or for local development:

```
http://localhost:8080/
```

## Authentication

### Login

1. Click **"Login with SSO"** button
2. You'll be redirected to your OIDC provider (Authentik, Keycloak, etc.)
3. Enter your credentials
4. After successful login, you're redirected back to Dploy

The JWT token is stored in your browser's localStorage and automatically included in API requests.

### Logout

Click the **"Logout"** button in the navigation bar to clear your session.

## Dashboard

The main dashboard shows two sections:

### Active Environments

Displays your currently running environments with:

- **Name**: Environment type (webterm, vscode, etc.)
- **Status**: Health status from ArgoCD
  - 🟢 `Healthy` - Ready to use
  - 🟡 `Progressing` - Still deploying
  - 🟠 `Degraded` - Some issues
  - ⚪ `Pending` - Just created
- **URL**: Direct link to access the environment
- **UUID**: Unique identifier
- **Time Left**: TTL countdown

**Actions:**
- 🔗 **Open**: Open environment in new tab
- ⏱️ **Extend**: Add more time to TTL
- 🗑️ **Delete**: Remove the environment

### Available Templates

Grid of environment templates you can launch:

- Click **"Launch"** to create a new environment
- Shows icon, name, and description

## Environment Counter

The header shows your quota usage:

```
2 / 5 environments
```

When approaching your limit (80%+), the counter turns orange as a warning.

## Direct URLs

You can access environments directly via URL:

```
https://dploy.your-domain.com/run/webterm
```

This URL:
1. Checks for authentication (redirects to login if needed)
2. Creates the environment if it doesn't exist
3. Shows a progress indicator while deploying
4. Automatically redirects to the environment when ready

Useful for:
- Bookmarking specific environment types
- Sharing links with students/users
- Embedding in learning management systems

## Status Polling

When launching an environment:

1. The UI shows a spinner and status message
2. Polls `/api/run/:env/status` every 2 seconds
3. When status becomes `Healthy`, redirects to the environment URL
4. If status is `Degraded` or takes too long, shows an error

## Toast Notifications

Actions show toast messages at the bottom of the screen:

- 🟢 **Success**: Green background
- 🔴 **Error**: Red background

Toasts auto-dismiss after 3 seconds.

## Icons

Environments display icons based on their `icon` field:

| Icon | Emoji | Type |
|------|-------|------|
| terminal | 💻 | Terminals |
| desktop | 🖥️ | VNC/Desktop |
| code | 📝 | IDEs |
| book | 📚 | Notebooks |
| database | 🗄️ | Databases |
| box | 📦 | Generic |
| web | 🌍 | Web apps |
| default | 🚀 | Fallback |

## Browser Compatibility

The Web UI works with modern browsers:

- Chrome 80+
- Firefox 75+
- Safari 13+
- Edge 80+

JavaScript must be enabled (uses fetch API and localStorage).

## Dark Theme

The UI uses a dark theme by default with:

- Dark background (`#0a0a0f`)
- Indigo accent color (`#6366f1`)
- Glassmorphism navbar

No light mode toggle is currently available.

## Troubleshooting

### "Authentication failed - please login"

Your token has expired or is invalid. Click Login to get a new token.

### Environment stuck in "Pending"

1. Check ArgoCD UI for sync status
2. Verify Helm chart is accessible
3. Check cluster resources (CPU, memory)

### "Maximum N environments allowed"

You've reached your quota. Delete unused environments or contact your administrator to increase the limit.

### Browser shows blank page

1. Check browser console for JavaScript errors
2. Ensure JavaScript is enabled
3. Try clearing localStorage: `localStorage.clear()`
