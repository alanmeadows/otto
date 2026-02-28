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

The tunnel URL is printed in the logs and shown in the dashboard sidebar. Access it from any browser — you'll be prompted to authenticate via Entra ID (or GitHub, depending on your `devtunnel user login` choice).

## Configuration

### Persistent Tunnel ID

By default, otto creates an ephemeral tunnel with a random URL each time. Set a tunnel ID for a stable URL across restarts:

```bash
otto config set dashboard.tunnel_id "yourname-otto"
```

The URL is still a random subdomain (e.g. `https://0mwbqhhp-4098.usw3.devtunnels.ms`) — the tunnel ID is a local label that ensures the same URL is reused.

### Access Control

Control who can reach the tunnel:

```bash
# Owner only (default) — only your devtunnel account
otto config set dashboard.tunnel_access "authenticated"

# Entra tenant — any user in your AAD tenant (e.g. all @microsoft.com)
otto config set dashboard.tunnel_access "tenant"

# Anonymous — anyone with the URL (no login required)
otto config set dashboard.tunnel_access "anonymous"

# GitHub org — members of a specific org
otto config set dashboard.tunnel_allow_org "my-github-org"
```

### Dashboard-Level Access Control

Even if the tunnel is set to `tenant` (allowing any FTE to reach the URL), otto can restrict who actually sees the dashboard using JWT identity from the DevTunnel headers:

```bash
# Set your email as the dashboard owner
otto config set dashboard.owner_email "you@microsoft.com"

# Grant access to specific colleagues
otto config set dashboard.allowed_users '["alice@microsoft.com", "bob@microsoft.com"]'
```

Allowed users can also be managed live from the dashboard sidebar without restarting. Session share links (`/shared/{token}`) always bypass dashboard access control — the token is the auth.

### Auto-Start

Start the tunnel automatically whenever the dashboard starts:

```bash
otto config set dashboard.auto_start_tunnel true
```

## All Config Keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `dashboard.tunnel_id` | string | | Persistent tunnel name for stable URL |
| `dashboard.tunnel_access` | string | `authenticated` | `anonymous`, `tenant`, or `authenticated` |
| `dashboard.tunnel_allow_org` | string | | GitHub org to grant access |
| `dashboard.auto_start_tunnel` | bool | `false` | Auto-start tunnel with dashboard |
| `dashboard.owner_email` | string | | Dashboard owner email |
| `dashboard.allowed_users` | string[] | | Emails allowed full dashboard access |
