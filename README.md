# Otto

[![CI](https://github.com/alanmeadows/otto/actions/workflows/ci.yml/badge.svg)](https://github.com/alanmeadows/otto/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

AI-powered PR lifecycle manager with a Copilot session dashboard.

## Why Otto?

Working with coding agents is powerful, but the surrounding workflow is full of friction. You submit a PR, walk away, and come back to find a failed pipeline, three review comments, a merge conflict, and a MerlinBot policy violation — each requiring you to context-switch back, diagnose, fix, push, and wait again. Meanwhile, your Copilot CLI sessions are trapped in the terminal where you started them.

Otto solves three problems:

### 📱 Drive Copilot from your phone

Otto's web dashboard discovers all your Copilot CLI sessions from `~/.copilot/session-state/`, shows their live status, and lets you resume and interact with them from any browser. Start a session at your desk, pick it up from your phone on the couch. Protected by Entra ID authentication via Azure DevTunnels — only you (or your team) can access it.

### 🔍 Guided PR reviews with a single command

```bash
otto pr review https://dev.azure.com/org/project/_git/repo/pullrequest/123 \
  "focus on error handling, concurrency safety, and resource cleanup"
```

Not a generic "find bugs" review — you tell otto what to focus on and it applies that lens across the entire diff. The guidance parameter turns a noisy LLM review into a directed expert review that catches the things you actually care about.

### 🤖 Hands-off PR lifecycle management

```bash
otto pr add https://dev.azure.com/org/project/_git/repo/pullrequest/123
```

Otto watches your PR continuously. When a pipeline fails, it reads the build logs, classifies the failure (infrastructure vs code), and fixes it. When a reviewer leaves comments, it evaluates each one, fixes the code if it agrees, and replies with its reasoning. When merge conflicts appear, it rebases and resolves them. When MerlinBot flags policy issues, it addresses them. All while you're working on something else.

The goal: submit a PR and let otto get it to green without you babysitting it.

## Features

- **PR autopilot** — monitors PRs for pipeline failures, review comments, merge conflicts, and MerlinBot feedback; auto-fixes and re-pushes up to configurable max attempts
- **Guided PR review** — LLM-powered code review with focus guidance and interactive inline comment posting
- **Copilot dashboard** — web UI for managing Copilot CLI sessions with real-time streaming, session resume, and live activity monitoring
- **Session sharing** — generate time-limited read-only links to share a single session's live conversation
- **Remote access** — Azure DevTunnel integration with Entra ID, org-scoped, or anonymous access control
- **Session discovery** — automatically discovers persisted sessions with live activity timestamps
- **Notifications** — Microsoft Teams webhook notifications for PR events
- **Multi-provider** — pluggable PR backends for Azure DevOps and GitHub

## Installation

Requires **Go 1.25.6+** and a [GitHub Copilot subscription](https://github.com/features/copilot).

```bash
git clone https://github.com/alanmeadows/otto.git
cd otto
make build        # produces bin/otto
make install      # installs to ~/.local/bin
```

### Prerequisites

- **GitHub Copilot CLI** — `npm install -g @github/copilot`
- **devtunnel** (optional, for remote access) — `curl -sL https://aka.ms/DevTunnelCliInstall | bash`

## Quick Start

### 1. Configure

```bash
# Configure a PR provider
otto config set pr.default_provider "ado"
otto config set pr.providers.ado.organization "myorg"
otto config set pr.providers.ado.project "myproject"
otto config set pr.providers.ado.pat "$ADO_PAT"

# Or GitHub
otto config set pr.default_provider "github"
otto config set pr.providers.github.token "$GITHUB_TOKEN"
```

### 2. Review a PR with guidance

```bash
# Generic review
otto pr review https://github.com/org/repo/pull/42

# Guided review — tell the LLM what to focus on
otto pr review https://github.com/org/repo/pull/42 "focus on error handling and race conditions"
otto pr review https://dev.azure.com/org/proj/_git/repo/pullrequest/123 "check for security issues in input validation"
```

Otto checks out the PR branch locally, analyzes the codebase structure, and creates an LLM session rooted in the repo — so the agent can read any file for context, not just the diff. Your guidance steers its focus. It then presents review comments in a table and lets you interactively select which to post as inline comments on the PR.

### 3. Monitor a PR on autopilot

```bash
# Add a PR for tracking — otto watches it continuously
otto pr add https://dev.azure.com/org/proj/_git/repo/pullrequest/123

# Start the daemon
otto server start
```

Otto will now poll the PR and automatically:
- Fix pipeline failures (classifies as infrastructure vs code, retries or fixes accordingly)
- Respond to review comments (agrees and fixes, or explains why it's by-design)
- Resolve merge conflicts (rebases and resolves via LLM)
- Address MerlinBot policy violations (ADO-specific)
- Send Teams notifications on status changes

### 4. Start the dashboard

```bash
# Local access
otto server start --dashboard

# Remote access via Azure DevTunnel (Entra ID auth)
otto server start --dashboard --tunnel
```

Open **http://localhost:4098** in your browser. The dashboard shows:
- All persisted Copilot CLI sessions with live "last activity" timestamps
- Resume any session and continue the conversation
- Create new sessions with model selection
- Real-time streaming of LLM responses and tool calls
- Share individual sessions via time-limited read-only links

## Copilot Dashboard

The dashboard is a responsive web UI embedded in the otto binary. It connects to the Copilot SDK to manage sessions and streams events in real time via WebSocket.

### Session Sharing

Click **🔗 Share** in any active session to generate a read-only link (1-hour default expiry):

```
https://your-tunnel.devtunnels.ms/shared/6548666f0382549d...
```

The recipient sees the conversation streaming live — tool calls, responses, intent changes — but no ability to send messages or access other sessions.

### Remote Access with Entra ID

To access the dashboard from your phone or share it with your team, use Azure DevTunnels with Entra ID authentication:

```bash
# One-time setup: login to devtunnel with your Microsoft/Entra account
devtunnel user login -e

# Start otto with tunnel
otto server start --dashboard --tunnel
```

Configure access control:

```bash
# Default: only you can access (authenticated via Entra)
otto config set dashboard.tunnel_access "authenticated"

# Share with your Entra tenant (e.g. all @microsoft.com users)
otto config set dashboard.tunnel_access "tenant"

# Share with a GitHub org
otto config set dashboard.tunnel_allow_org "my-github-org"

# Use a persistent tunnel for a stable URL
otto config set dashboard.tunnel_id "my-otto"
```

Access settings are also configurable from the dashboard sidebar under the Tunnel section.

> **Note:** The authentication provider (Entra vs GitHub) is determined by how you logged into the devtunnel CLI. Use `devtunnel user login -e` for Entra (Microsoft accounts), or `devtunnel user login -g` for GitHub accounts.

## Configuration

### Config Files

Otto merges configuration from three layers (later layers override earlier):

1. **Built-in defaults** — sensible starting values
2. **User config** — `~/.config/otto/otto.jsonc`
3. **Repo config** — `.otto/otto.jsonc` (in the repository root)

Both config files use [JSONC](https://code.visualstudio.com/docs/languages/json#_json-with-comments) format (JSON with comments).

Use `otto config show` to inspect the merged result and `otto config set <key> <value>` to write values to the repo-local file.

### Configuration Reference

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `models.primary` | string | `claude-opus-4.6` | Primary LLM model |
| `models.secondary` | string | `gpt-5.2-codex` | Secondary model for multi-model review |
| `pr.default_provider` | string | `ado` | Default PR provider (`ado` or `github`) |
| `pr.max_fix_attempts` | int | `5` | Max auto-fix attempts per PR |
| `pr.providers.ado.organization` | string | | ADO organization name |
| `pr.providers.ado.project` | string | | ADO project name |
| `pr.providers.ado.pat` | string | | ADO personal access token |
| `pr.providers.ado.auto_complete` | bool | `false` | Auto-complete ADO PRs |
| `pr.providers.ado.merlinbot` | bool | `false` | Enable MerlinBot integration |
| `pr.providers.github.token` | string | | GitHub personal access token |
| `server.poll_interval` | string | `10m` | Daemon PR poll interval |
| `server.port` | int | `4097` | Daemon HTTP API port |
| `dashboard.port` | int | `4098` | Dashboard web server port |
| `dashboard.enabled` | bool | `false` | Enable the Copilot session dashboard |
| `dashboard.auto_start_tunnel` | bool | `false` | Auto-start Azure DevTunnel on dashboard start |
| `dashboard.copilot_server` | string | | Connect to shared headless Copilot server (e.g. `localhost:4321`) |
| `dashboard.tunnel_id` | string | | Persistent tunnel name for stable URL across restarts |
| `dashboard.tunnel_access` | string | | Access mode: `anonymous`, `tenant`, or empty (authenticated) |
| `dashboard.tunnel_allow_org` | string | | GitHub org to grant tunnel access |
| `notifications.teams_webhook_url` | string | | Microsoft Teams webhook URL |
| `notifications.events` | string[] | | Events to notify on |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `OTTO_ADO_PAT` | Azure DevOps personal access token |
| `OTTO_GITHUB_TOKEN` | GitHub personal access token |

## Command Reference

```
otto                          LLM-powered PR lifecycle manager
├── pr                        Manage pull requests
│   ├── add <url>             Track a PR for auto-monitoring
│   ├── list                  List tracked PRs
│   ├── status [id]           Show PR status
│   ├── remove [id]           Stop tracking a PR
│   ├── fix [id]              Manually trigger LLM fix
│   ├── log [id]              Show PR activity log
│   └── review <url> [guide]  LLM-powered PR review with optional focus guidance
├── server                    Manage the otto daemon
│   ├── start                 Start the daemon
│   │   ├── --dashboard       Enable Copilot session dashboard
│   │   ├── --tunnel          Auto-start Azure DevTunnel
│   │   ├── --dashboard-port  Dashboard port (default: 4098)
│   │   └── --foreground      Run in foreground
│   ├── stop                  Stop the daemon
│   ├── status                Show daemon status
│   └── install               Install as systemd user service
├── repo                      Manage repositories
│   ├── add [name]            Register a repository
│   ├── remove <name>         Remove a tracked repository
│   └── list                  List tracked repositories
├── config                    Manage configuration
│   ├── show [--json]         Show merged configuration
│   └── set <key> <value>     Set a config value
└── completion                Generate shell completions
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ otto server start --dashboard --tunnel                      │
│                                                             │
│  ┌──────────────────┐   ┌───────────────────────────────┐   │
│  │ PR API (:4097)   │   │ Dashboard (:4098)             │   │
│  │ PR monitoring    │   │ Web UI + WebSocket streaming  │   │
│  │ Auto-fix/review  │   │ Session sharing (token-gated) │   │
│  └──────────────────┘   └───────────────┬───────────────┘   │
│                                         │                   │
│  ┌──────────────────────────────────────┴───────────────┐   │
│  │ Copilot Session Manager (copilot-sdk/go)             │   │
│  │ Create · Resume · Stream · Share                     │   │
│  │ Persisted session discovery (~/.copilot/session-state)│   │
│  └──────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ DevTunnel Manager                                    │   │
│  │ Entra ID / GitHub org / anonymous access control     │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

All LLM interaction goes through the [GitHub Copilot SDK for Go](https://github.com/github/copilot-sdk).

## Development

```bash
make build    # Build binary to bin/otto
make test     # Run all tests
make lint     # Run golangci-lint
make vet      # Run go vet
make all      # lint + vet + test + build
```

## License

[Apache 2.0](LICENSE)
