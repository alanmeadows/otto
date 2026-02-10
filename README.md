# Otto

AI-powered spec-to-code pipeline and PR lifecycle manager.

## Overview

Otto is a Go CLI that orchestrates LLM-driven development workflows through [OpenCode](https://opencode.ai). It turns natural-language prompts into structured specifications, breaks them into executable tasks, runs them via an LLM coding agent, and monitors pull requests for review feedback — closing the loop automatically.

Otto is an **LLM orchestrator, not a logic engine** — it delegates reasoning and code generation to LLMs and keeps its own code focused on plumbing, sequencing, and state management.

## Features

- **Spec pipeline** — structured workflow from prompt → requirements → research → design → tasks → execution, with multi-model critical review at every stage
- **PR monitoring** — daemon polls PRs for review comments, auto-fixes issues via LLM, and re-pushes (up to configurable max attempts)
- **PR review** — LLM-powered code review with interactive inline comment posting
- **Notifications** — Microsoft Teams webhook notifications for PR events
- **Multi-provider** — pluggable PR backends for Azure DevOps and GitHub
- **Repository management** — git worktree and branch strategies for multi-repo workflows
- **All state on disk** — specs, PRs, and config stored as human-readable markdown/JSONC — no databases

## Installation

### From Source

Requires **Go 1.25.6+**.

```bash
go install github.com/alanmeadows/otto/cmd/otto@latest
```

Or clone and build:

```bash
git clone https://github.com/alanmeadows/otto.git
cd otto
make build        # produces bin/otto
make install      # installs to $GOPATH/bin
```

### Pre-built Binaries

Download from [GitHub Releases](https://github.com/alanmeadows/otto/releases). Binaries are available for Linux, macOS, and Windows on amd64 and arm64.

## Quick Start

### 1. Initialize Configuration

```bash
# Set LLM models (defaults shown)
otto config set models.primary "github-copilot/claude-opus-4.6"
otto config set models.secondary "github-copilot/gpt-5.2-codex"
otto config set models.tertiary "github-copilot/gemini-3-pro-preview"

# Configure a PR provider (GitHub example)
otto config set pr.default_provider "github"
otto config set pr.providers.github.token "$GITHUB_TOKEN"

# Or ADO
otto config set pr.default_provider "ado"
otto config set pr.providers.ado.organization "myorg"
otto config set pr.providers.ado.project "myproject"
otto config set pr.providers.ado.pat "$ADO_PAT"
```

### 2. Create and Execute a Spec

```bash
# Create a spec from a prompt
otto spec add "Add retry logic to the HTTP client"

# Run each phase (each is idempotent — safe to re-run)
otto spec requirements --spec add-retry-logic
otto spec research --spec add-retry-logic
otto spec design --spec add-retry-logic
otto spec task generate --spec add-retry-logic

# Execute all tasks
otto spec execute --spec add-retry-logic
```

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
| `models.primary` | string | `github-copilot/claude-opus-4.6` | Primary LLM model |
| `models.secondary` | string | `github-copilot/gpt-5.2-codex` | Secondary model for multi-model review |
| `models.tertiary` | string | `github-copilot/gemini-3-pro-preview` | Tertiary model for multi-model review |
| `opencode.url` | string | `http://localhost:4096` | OpenCode server URL |
| `opencode.auto_start` | bool | `true` | Auto-start OpenCode server if not running |
| `opencode.username` | string | `opencode` | OpenCode authentication username |
| `opencode.password` | string | | OpenCode authentication password |
| `opencode.permissions` | string | `allow` | OpenCode tool permissions (`allow`/`deny`) |
| `pr.default_provider` | string | `ado` | Default PR provider (`ado` or `github`) |
| `pr.max_fix_attempts` | int | `5` | Max auto-fix attempts per PR |
| `pr.providers.ado.organization` | string | | ADO organization name |
| `pr.providers.ado.project` | string | | ADO project name |
| `pr.providers.ado.pat` | string | | ADO personal access token |
| `pr.providers.ado.auto_complete` | bool | `false` | Auto-complete ADO PRs |
| `pr.providers.ado.merlinbot` | bool | `false` | Enable MerlinBot integration |
| `pr.providers.ado.create_work_item` | bool | `false` | Create work items for ADO PRs |
| `pr.providers.github.token` | string | | GitHub personal access token |
| `server.poll_interval` | string | `2m` | Daemon PR poll interval (Go duration) |
| `server.port` | int | `4097` | Daemon HTTP API port |
| `server.log_dir` | string | `~/.local/share/otto/logs` | Daemon log directory |
| `spec.max_parallel_tasks` | int | `4` | Max tasks to run in parallel |
| `spec.task_timeout` | string | `30m` | Per-task timeout (Go duration) |
| `spec.max_task_retries` | int | `15` | Max retries for a failed task |
| `spec.task_briefing` | bool | `true` | Enable task briefing step before execution |
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
otto                          LLM-powered spec engine, task executor, and PR lifecycle manager
├── spec                      Manage specifications
│   ├── add <prompt>          Create a new spec from a prompt
│   ├── list                  List all specs in the repo
│   ├── requirements [--spec] Generate/refine requirements document
│   ├── research [--spec]     Generate/refine research document
│   ├── design [--spec]       Generate/refine design document
│   ├── execute [--spec]      Execute all pending tasks
│   ├── questions [--spec]    Auto-resolve open questions
│   ├── run <prompt> [--spec] Run ad-hoc prompt with spec context
│   └── task                  Manage spec tasks
│       ├── generate [--spec] Generate tasks from design document
│       ├── list [--spec]     List tasks and their status
│       ├── add <prompt>      Add a manual task
│       └── run [--id]        Run a specific task
├── pr                        Manage pull requests
│   ├── add <url>             Add a PR for tracking
│   ├── list                  List tracked PRs
│   ├── status [id]           Show PR status
│   ├── remove [id]           Stop tracking a PR
│   ├── fix [id]              Fix PR review issues via LLM
│   ├── log [id]              Show PR activity log
│   └── review <url>          LLM-powered PR code review
├── server                    Manage the otto daemon
│   ├── start                 Start the daemon (--foreground, --port)
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
    ├── bash
    ├── zsh
    ├── fish
    └── powershell
```

Use `otto <command> --help` for detailed usage of any command.

### Shell Completion

```bash
# Bash
otto completion bash > /etc/bash_completion.d/otto

# Zsh
otto completion zsh > "${fpath[1]}/_otto"

# Fish
otto completion fish > ~/.config/fish/completions/otto.fish
```

## PR Backend Providers

Otto supports pluggable PR backends:

- **Azure DevOps** — full support for PRs, pipeline status, inline comments, and work items. Requires an organization, project, and PAT.
- **GitHub** — PR tracking, review comments, and inline comments via the GitHub API. Requires a personal access token.

Providers are auto-detected from PR URLs. Configure one or both in the `pr.providers` config section.

## Architecture

Otto follows an LLM-first orchestration pattern:

```
Spec Pipeline → Task Execution → PR Monitoring → Notifications
```

1. **Spec pipeline** — each phase (requirements, research, design, tasks) produces a markdown artifact under `.otto/specs/<slug>/`, reviewed by multiple LLM models
2. **Task execution** — tasks run in dependency order with parallel groups, retries, and an outer protective loop
3. **PR monitoring** — the daemon polls PR backends, detects review comments and pipeline failures, and dispatches LLM fixes
4. **Notifications** — webhook-based alerts for PR events

All LLM interaction goes through [OpenCode](https://opencode.ai) via its Go SDK. Otto manages the OpenCode server lifecycle automatically.

See [docs/design.md](docs/design.md) for the full design document.

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
