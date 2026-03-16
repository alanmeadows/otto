# Remote Access via Azure DevTunnels

Otto's dashboard can be exposed securely over the internet using [Azure DevTunnels](https://learn.microsoft.com/en-us/azure/developer/dev-tunnels/), allowing you to manage Copilot sessions from your phone or share them with colleagues.

## Prerequisites

Install the DevTunnel CLI and authenticate:

```bash
# Install (via winget on Windows — also available from WSL)
winget install Microsoft.devtunnel

# Login with Entra ID (Microsoft account) — recommended
devtunnel user login -e

# Or login with GitHub
devtunnel user login -g
```

The authentication provider you choose here determines how tunnel visitors authenticate. Use `-e` for Entra if you want Microsoft/AAD login, `-g` for GitHub login.

## Quick Start

```bash
# Start the server — dashboard and tunnel are enabled by default
otto server start
```

On tunnel start, otto generates a secret access key and logs the full access URL:

```
INFO dashboard access URL url=https://xxxx-4098.usw3.devtunnels.ms?key=a8f3b2c1d4e5...
```

Copy this URL and open it on your phone or any browser. The key is embedded in the URL so you can share it with yourself via Teams/iMessage/email without needing to remember a passcode.

The first visit with `?key=` sets a browser cookie (30 days) and redirects to a clean URL — subsequent visits don't need the key.

### QR Code

When the tunnel is active, the **Tunnel** section at the bottom of the dashboard sidebar shows a scannable QR code. The QR encodes the full authenticated tunnel URL (`?key=` included), so scanning it with your phone camera opens the dashboard and logs you in — no copy-pasting needed.

## Dashboard Access Control

Otto uses a URL-based access key to protect the dashboard:

- **With key** (`?key=<secret>` or cookie): full dashboard access
- **Without key**: passcode prompt page
- **Local access** (localhost): always allowed, no key needed
- **Session share links** (`/shared/{token}`): bypass dashboard auth — the token is the auth

The key is shown in:
- The server logs on tunnel start
- The dashboard sidebar tunnel URL field (for easy copying)

## Insecure Modes

For convenience on trusted networks, otto offers two opt-in flags to relax security:

### `--insecure-tunnel`

Launches the DevTunnel with anonymous access — no Azure AD / GitHub login required to reach the tunnel URL. Anyone with the URL can connect.

```bash
otto server start --insecure-tunnel
```

This is equivalent to setting `dashboard.tunnel_access: "anonymous"` in config.

### `--insecure-dashboard`

Disables the dashboard passcode (`?key=`) requirement. Anyone who can reach the dashboard URL has full access without authentication.

```bash
otto server start --insecure-dashboard
```

This is equivalent to setting `dashboard.require_key: false` in config.

### Combined

For a fully open dashboard accessible to anyone on the internet (no tunnel auth, no passcode):

```bash
otto server start --insecure-tunnel --insecure-dashboard
```

> ⚠️ **Warning:** This gives anyone with the URL full access to your Copilot sessions, including the ability to send messages and create new sessions. Only use this on trusted networks or for demos.

Both flags produce warning messages at startup and in the server logs.

## Configuration

### Persistent Tunnel ID

By default, otto creates an ephemeral tunnel with a random URL each time. Set a tunnel ID for a stable URL across restarts:

```bash
otto config set dashboard.tunnel_id "yourname-otto"
```

The URL is still a random subdomain (e.g. `https://0mwbqhhp-4098.usw3.devtunnels.ms`) — the tunnel ID is a local label that ensures the same URL is reused.

### Tunnel Access Mode

Control who can reach the tunnel URL (before hitting otto's key check):

```bash
# Entra tenant — any user in your AAD tenant (recommended)
otto config set dashboard.tunnel_access "tenant"

# Owner only (default) — only your devtunnel account
otto config set dashboard.tunnel_access "authenticated"

# Anonymous — anyone with the URL (no DevTunnel login)
otto config set dashboard.tunnel_access "anonymous"
```

### Dashboard Passcode

Control whether remote access requires the `?key=` parameter:

```bash
# Require passcode (default)
otto config set dashboard.require_key true

# Disable passcode — fully open dashboard
otto config set dashboard.require_key false
```

### Process Lifecycle

The devtunnel runs as an independent [bgtask](https://github.com/philsphicas/bgtask) with `--restart always`:

- The tunnel **survives otto restarts** — remote connections stay up across `otto server restart` and `otto server upgrade`
- On restart, otto discovers and attaches to the existing tunnel via `bgtask status`
- Explicit stop from the dashboard or `otto server stop` will not kill the tunnel — use `bgtask stop otto-tunnel` to stop it manually
- bgtask must be installed: `go install github.com/philsphicas/bgtask/cmd/bgtask@latest`

## All Config Keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `dashboard.tunnel_id` | string | | Persistent tunnel name for stable URL |
| `dashboard.tunnel_access` | string | `authenticated` | `anonymous`, `tenant`, or `authenticated` |
| `dashboard.tunnel_allow_org` | string | | GitHub org to grant tunnel access |
| `dashboard.require_key` | bool | `true` | Require passcode for remote dashboard access |
| `dashboard.allowed_users` | string[] | | Emails allowed full dashboard access |
