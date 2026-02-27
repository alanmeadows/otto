# Otto

AI-powered PR lifecycle manager with a Copilot session dashboard.

## Overview

Otto is a Go CLI that orchestrates LLM-driven development workflows through the [GitHub Copilot SDK](https://github.com/github/copilot-sdk). It monitors pull requests for review feedback, automatically fixes build failures, resolves merge conflicts, and responds to code review comments — all powered by LLM coding agents.

Otto also includes a **web-based Copilot session dashboard** for managing multiple Copilot CLI sessions from your browser or phone, with Azure DevTunnel support for remote access.

Otto is an **LLM orchestrator, not a logic engine** — it delegates reasoning and code generation to LLMs and keeps its own code focused on plumbing, sequencing, and state management.

## Features

- **PR monitoring** — daemon polls PRs for review comments, auto-fixes issues via LLM, and re-pushes (up to configurable max attempts)
- **PR review** — LLM-powered code review with interactive inline comment posting
- **Copilot dashboard** — web UI for managing Copilot CLI sessions with real-time streaming, session resume, and live activity monitoring
- **Remote access** — Azure DevTunnel integration to access the dashboard from your phone
- **Session discovery** — automatically discovers persisted sessions from `~/.copilot/session-state/` with live activity timestamps
- **Shared server** — optionally connect to an existing headless Copilot server for shared session management
- **Notifications** — Microsoft Teams webhook notifications for PR events
- **Multi-provider** — pluggable PR backends for Azure DevOps and GitHub
- **Repository management** — git worktree and branch strategies for multi-repo workflows
- **All state on disk** — PRs and config stored as human-readable markdown/JSONC — no databases

## Installation

### From Source

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

### 1. Initialize Configuration

```bash
# Set LLM models (defaults shown)
otto config set models.primary "claude-opus-4.6"
otto config set models.secondary "gpt-5.2-codex"

# Configure a PR provider (GitHub example)
otto config set pr.default_provider "github"
otto config set pr.providers.github.token "$GITHUB_TOKEN"

# Or ADO
otto config set pr.default_provider "ado"
otto config set pr.providers.ado.organization "myorg"
otto config set pr.providers.ado.project "myproject"
otto config set pr.providers.ado.pat "$ADO_PAT"
```

### 2. Start the Dashboard

```bash
# Start with the Copilot session dashboard
otto server start --dashboard

# Or with remote access via Azure DevTunnel
otto server start --dashboard --tunnel

# Enable permanently via config
otto config set dashboard.enabled true
```

Open **http://localhost:4098** in your browser. The dashboard shows:
- All persisted Copilot CLI sessions from `~/.copilot/session-state/`
- Live activity timestamps ("30s ago", "5m ago") that update in real time
- Ability to resume any session and continue the conversation
- Create new sessions with model selection
- Real-time streaming of LLM responses, tool calls, and intent changes

### 3. Monitor a PR

```bash
# Add a PR for tracking
otto pr add https://github.com/org/repo/pull/42

# Start the daemon to auto-monitor
otto server start
```

### 4. Review a PR

```bash
otto pr review https://github.com/org/repo/pull/42
```

Otto fetches the diff, runs an LLM review, presents comments in a table, and lets you select which to post as inline comments.

## Copilot Dashboard

The dashboard is a responsive web UI embedded in the otto binary. It connects to the Copilot SDK to manage sessions and streams events in real time via WebSocket.

### Features

- **Session discovery** — automatically lists all persisted sessions from `~/.copilot/session-state/` with summaries and live "last activity" timestamps
- **Session resume** — click any saved session to resume it with full conversation history
- **New sessions** — create sessions with model selection (Claude, GPT, Gemini)
- **Real-time chat** — streaming responses with markdown rendering, tool call indicators, and intent tracking
- **Azure DevTunnel** — one-click tunnel setup for remote access from your phone
- **Mobile responsive** — works on phone browsers with touch-friendly sidebar

### Shared Copilot Server

By default, otto spawns its own Copilot CLI process. You can optionally run a shared headless server that otto connects to:

```bash
# Start a persistent headless copilot server
copilot --headless --port 4321

# Configure otto to connect to it
otto config set dashboard.copilot_server "localhost:4321"
```

This allows otto's dashboard to create and manage sessions through the shared server. Sessions created through the dashboard are accessible to any SDK client connected to the same server.

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
| `pr.providers.ado.create_work_item` | bool | `false` | Create work items for ADO PRs |
| `pr.providers.github.token` | string | | GitHub personal access token |
| `server.poll_interval` | string | `10m` | Daemon PR poll interval (Go duration) |
| `server.port` | int | `4097` | Daemon HTTP API port |
| `server.log_dir` | string | `~/.local/share/otto/logs` | Daemon log directory |
| `dashboard.port` | int | `4098` | Dashboard web server port |
| `dashboard.enabled` | bool | `false` | Enable the Copilot session dashboard |
| `dashboard.auto_start_tunnel` | bool | `false` | Auto-start Azure DevTunnel on dashboard start |
| `dashboard.copilot_server` | string | | Connect to shared headless Copilot server (e.g. `localhost:4321`) |
| `repos[].name` | string | | Repository name |
| `repos[].primary_dir` | string | | Primary checkout directory |
| `repos[].worktree_dir` | string | | Worktree directory |
| `repos[].git_strategy` | string | | Git strategy: `worktree`, `branch`, or `hands-off` |
| `repos[].branch_template` | string | | Branch name template (e.g. `otto/{{.Name}}`) |
| `notifications.teams_webhook_url` | string | | Microsoft Teams webhook URL |
| `notifications.events` | string[] | | Events to notify on |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `OTTO_ADO_PAT` | Azure DevOps personal access token (alternative to config) |
| `OTTO_GITHUB_TOKEN` | GitHub personal access token (alternative to config) |

## Command Reference

```
otto                          LLM-powered PR lifecycle manager
├── pr                        Manage pull requests
│   ├── add <url>             Add a PR for tracking
│   ├── list                  List tracked PRs
│   ├── status [id]           Show PR status
│   ├── remove [id]           Stop tracking a PR
│   ├── fix [id]              Fix PR review issues via LLM
│   ├── log [id]              Show PR activity log
│   └── review <url>          LLM-powered PR code review
├── server                    Manage the otto daemon
│   ├── start                 Start the daemon
│   │   ├── --dashboard       Enable Copilot session dashboard
│   │   ├── --tunnel          Auto-start Azure DevTunnel
│   │   ├── --dashboard-port  Dashboard port (default: 4098)
│   │   └── --foreground      Run in foreground (don't daemonize)
│   ├── stop                  Stop the daemon
│   ├── status                Show daemon status
│   └── install               Install as systemd user service
├── repo                      Manage repositories
│   ├── add [name]            Register a repository (interactive)
│   ├── remove <name>         Remove a tracked repository
│   └── list                  List tracked repositories
├── worktree                  Manage git worktrees
│   ├── add <name>            Create a worktree
│   ├── list                  List worktrees
│   └── remove <name>         Remove a worktree
├── config                    Manage configuration
│   ├── show [--json]         Show merged configuration
│   └── set <key> <value>     Set a config value
└── completion                Generate shell completions
```

Use `otto <command> --help` for detailed usage of any command.

## PR Backend Providers

Otto supports pluggable PR backends:

- **Azure DevOps** — full support for PRs, pipeline status, inline comments, and work items. Requires an organization, project, and PAT.
- **GitHub** — PR tracking, review comments, and inline comments via the GitHub API. Requires a personal access token.

Providers are auto-detected from PR URLs. Configure one or both in the `pr.providers` config section.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ otto server start --dashboard                               │
│                                                             │
│  ┌──────────────────┐   ┌───────────────────────────────┐   │
│  │ PR API (:4097)   │   │ Dashboard (:4098)             │   │
│  │ PR monitoring    │   │ Static UI (embed.FS)          │   │
│  │ Auto-fix/review  │   │ REST API + WebSocket          │   │
│  └──────────────────┘   │ Real-time session streaming   │   │
│                         └───────────────┬───────────────┘   │
│                                         │                   │
│  ┌──────────────────────────────────────┴───────────────┐   │
│  │ Copilot Session Manager (copilot-sdk/go)             │   │
│  │ Create/resume/stream sessions                        │   │
│  │ Persisted session discovery (~/.copilot/session-state)│   │
│  └──────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ DevTunnel Manager (optional)                         │   │
│  │ Azure DevTunnel for remote access                    │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

All LLM interaction goes through the [GitHub Copilot SDK for Go](https://github.com/github/copilot-sdk). Otto manages the Copilot CLI process lifecycle automatically.

## Development

```bash
make build    # Build binary to bin/otto
make test     # Run all tests
make lint     # Run golangci-lint
make vet      # Run go vet
make all      # lint + vet + test + build
make clean    # Remove build artifacts
```

## License

[MIT](LICENSE)
