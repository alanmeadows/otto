# Remote Access via Azure DevTunnels

Otto's dashboard can be exposed securely over the internet using [Azure DevTunnels](https://learn.microsoft.com/en-us/azure/developer/dev-tunnels/), allowing you to manage Copilot sessions from your phone or share them with colleagues.

## Prerequisites

Install the DevTunnel CLI and authenticate:

```bash
# Install
curl -sL https://aka.ms/DevTunnelCliInstall | bash

# Login with Entra ID (Microsoft account) — recommended
devtunnel user login -e

# Or login with GitHub
devtunnel user login -g
```

The authentication provider you choose here determines how tunnel visitors authenticate. Use `-e` for Entra if you want Microsoft/AAD login, `-g` for GitHub login.

## Quick Start

```bash
# Start the dashboard with a tunnel
otto server start --dashboard --tunnel
```

On tunnel start, otto generates a secret access key and logs the full access URL:

```
INFO dashboard access URL url=https://xxxx-4098.usw3.devtunnels.ms?key=a8f3b2c1d4e5...
```

Copy this URL and open it on your phone or any browser. The key is embedded in the URL so you can share it with yourself via Teams/iMessage/email without needing to remember a passcode.

The first visit with `?key=` sets a browser cookie (30 days) and redirects to a clean URL — subsequent visits don't need the key.

## Dashboard Access Control

Otto uses a URL-based access key to protect the dashboard:

- **With key** (`?key=<secret>` or cookie): full dashboard access
- **Without key**: passcode prompt page
- **Local access** (localhost): always allowed, no key needed
- **Session share links** (`/shared/{token}`): bypass dashboard auth — the token is the auth

The key is shown in:
- The server logs on tunnel start
- The dashboard sidebar tunnel URL field (for easy copying)

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

### Auto-Start

Start the tunnel automatically whenever the dashboard starts:

```bash
otto config set dashboard.auto_start_tunnel true
```

### Process Lifecycle

The devtunnel process is bound to otto's lifecycle:
- `Pdeathsig` ensures the kernel kills devtunnel if otto crashes
- `Setpgid` allows killing the entire process group on shutdown
- No orphaned tunnel processes after otto exits

## All Config Keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `dashboard.tunnel_id` | string | | Persistent tunnel name for stable URL |
| `dashboard.tunnel_access` | string | `authenticated` | `anonymous`, `tenant`, or `authenticated` |
| `dashboard.tunnel_allow_org` | string | | GitHub org to grant tunnel access |
| `dashboard.auto_start_tunnel` | bool | `false` | Auto-start tunnel with dashboard |
