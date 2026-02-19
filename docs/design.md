# Otto - Design Document

> LLM-powered specification engine, task executor, and PR lifecycle manager.

Otto is a Go-based CLI tool with an optional long-running server (daemon). It orchestrates LLM-driven workflows through the [OpenCode](https://opencode.ai) server and its [Go SDK](https://github.com/anomalyco/opencode-sdk-go) (`github.com/sst/opencode-sdk-go`). Otto is an **LLM orchestrator, not a logic engine** — it delegates as much work as possible to LLMs and keeps its own code focused on plumbing, sequencing, and state management. Otto's core capabilities are:

1. **Specification workflow** (`otto spec`) — a structured pipeline for turning a prompt into requirements, research, design, tasks, and executed code — with multi-model critical review at every stage.
2. **PR lifecycle management** (`otto pr`) — globally tracking pull requests across extendable backends (ADO, GitHub), watching pipelines, auto-fixing failures, and re-pushing.
3. **Repository management** (`otto repo`) — tracking git worktrees and branch patterns for multi-repo workflows.

---

## Goals

- Provide a CLI-first tool for orchestrating multi-step LLM-backed coding workflows
- Delegate all reasoning, evaluation, and content generation to LLMs — otto is the orchestrator, not the brain
- Keep otto's own code simple: sequencing, state management, file I/O, and process lifecycle — not domain logic
- Implement a full specification pipeline: requirements → research → design → tasks → execution
- Apply multi-model critical review (primary/secondary) to all LLM-generated artifacts
- Track and manage pull requests across extendable backends (ADO primary, GitHub planned)
- Automatically detect and fix pipeline failures, then re-push
- Execute task plans reliably with an outer non-LLM protective loop, parallelism, and isolated sessions
- Store all state as human-readable markdown/JSONC on disk — no databases
- Be idempotent everywhere: every command can be re-run safely — to resume, refine, or incorporate upstream changes
- Run as a persistent daemon for continuous PR monitoring

## Non-Goals

- Replacing OpenCode or reimplementing an LLM agent
- Building complex domain logic in Go that an LLM can handle — when in doubt, farm it to the LLM
- Providing a web UI or TUI (otto is CLI-only; the LLM work happens inside OpenCode)
- Supporting every possible forge (start with ADO + GitHub via an extensible backend interface)

---

## Architecture Overview

```
+------------------+       +-------------------+       +------------------+
|                  |       |                   |       |                  |
|   otto (CLI)     +------>+   ottod (daemon)  +------>+ OpenCode Server  |
|  - spec execute  |       |   - PR monitor    |       | (opencode serve) |
+--------+---------+       +--------+----------+       +------------------+
         |                          |                          ^
         |                          |                          |
         +---------------------------------------------------------+
         |                          |
         v                          v
+--------+---------+       +--------+----------+
|                  |       |                   |
|  .otto/ (repo)   |       | PR Backend (ADO,  |
|  ~/.config/otto/ |       | GitHub, ...)      |
|  (user config)   |       +-------------------+
+------------------+
```

### Components

1. **`otto` CLI** — Primary user interface. Dispatches all commands (spec, pr, repo, server, config).
2. **`ottod` daemon** — Long-running server. Polls PR backends for status, triggers LLM fixes, and monitors reviewer comments. Communicates with CLI over a local HTTP API. The daemon does **not** run spec execution — that runs in the CLI process (see `otto spec execute`).
3. **OpenCode server** — External dependency. Otto manages a single `opencode serve` process and interacts via the Go SDK (`github.com/sst/opencode-sdk-go`). The SDK's per-request `directory` parameter allows one server to operate across multiple worktrees simultaneously — each session, file operation, and command is scoped to a specific directory. Multiple sessions can run in parallel against one server for task parallelism.
4. **PR Backend** — Pluggable interface for ADO, GitHub, and future providers. Each backend understands how to create PRs, check pipeline status, extract build logs, read/post comments, and run provider-specific workflows.

### Design Philosophy: LLM-First Orchestration

Otto's Go code handles **plumbing**: process management, file I/O, concurrency, state persistence, CLI parsing, config merging, git operations, and API calls to PR backends. Everything that involves **judgment, reasoning, evaluation, or content generation** is delegated to an LLM via OpenCode sessions.

This means otto avoids building complex logic for tasks an LLM can handle — e.g., evaluating where a new task fits in a dependency graph, deciding if a question has been answered, or determining which files a task should touch. Instead, otto constructs a well-crafted prompt, provides the relevant context, and lets the LLM produce the result.

Rigid code is appropriate where determinism and reliability matter: the outer execution loop, file locking, status tracking, phase sequencing, git commits, and health checks. The rule of thumb: **if it requires understanding intent or content, use an LLM; if it requires reliability or system integration, write Go code.**

---

## Prompt Template System

All LLM prompts are stored as **markdown files** in the codebase at `internal/prompts/`. This makes them easy to read, edit, version, and review alongside the code.

### Template Files

| File | Used by | Purpose |
|------|---------|---------|
| `requirements.md` | `otto spec requirements` | Requirements refinement and analysis |
| `research.md` | `otto spec research` | Technical research with tool-use mandates |
| `design.md` | `otto spec design` | Architecture and implementation design |
| `tasks.md` | `otto spec task generate` | Task decomposition with parallel grouping |
| `review.md` | Multi-model review pipeline | Critical review of any artifact |
| `task-briefing.md` | `otto spec execute` | Pre-execution task context distillation |
| `phase-review.md` | `otto spec execute` | Inter-phase review gate |
| `external-assumptions.md` | `otto spec execute` | Post-review external assumption validation & repair (primary model) |
| `domain-hardening.md` | `otto spec execute` | Post-review domain hardening & polishing (primary model) |
| `documentation-alignment.md` | `otto spec execute` | End-of-execution documentation alignment (primary + review) |
| `question-harvest.md` | `otto spec execute` | Extract questions from execution logs |
| `question-resolve.md` | `otto spec questions` | Auto-resolution of open questions |
| `pr-review.md` | `otto pr review` | Third-party PR review with inline comments |
| `pr-comment-respond.md` | PR comment monitoring | Evaluate and respond to reviewer comments |

### Template Variables

Prompts use Go's standard `text/template` syntax. Otto's Go code parses the template, executes it with a data map of variable values, and sends the assembled prompt to the LLM.

Template syntax: `{{.variable}}` for substitution, `{{if .variable}}...{{end}}` for conditionals, `{{with .variable}}...{{end}}` for scoped blocks.

Common variables:

| Variable | Content |
|----------|---------|
| `{{.requirements_md}}` | Contents of the spec's requirements.md |
| `{{.research_md}}` | Contents of research.md |
| `{{.design_md}}` | Contents of design.md |
| `{{.existing_design_md}}` | Current design.md (for refinement mode) |
| `{{.existing_research_md}}` | Current research.md (for refinement mode) |
| `{{.existing_tasks_md}}` | Current tasks.md (for re-run mode) |
| `{{.questions_md}}` | Contents of questions.md |
| `{{.existing_artifacts}}` | All other spec documents that exist |
| `{{.codebase_summary}}` | Analysis of existing repository (structure, patterns, conventions) |
| `{{.artifact}}` | The artifact under review (for review.md) |
| `{{.context}}` | Upstream context for review |
| `{{.execution_logs}}` | Dialog logs from task executions |
| `{{.task_id}}` | Target task ID (for task-briefing.md) |
| `{{.task_title}}` | Target task title (for task-briefing.md) |
| `{{.task_description}}` | Target task description (for task-briefing.md) |
| `{{.task_files}}` | Files the target task will create/modify (for task-briefing.md) |
| `{{.task_depends_on}}` | Task IDs the target task depends on (for task-briefing.md) |
| `{{.tasks_md}}` | Full contents of the spec's tasks.md (for task-briefing.md) |
| `{{.phase_number}}` | Current phase number |
| `{{.pr_title}}` | PR title (for pr-review and pr-comment-respond) |
| `{{.pr_description}}` | PR description body |
| `{{.target_branch}}` | Target branch for PR review (e.g., `origin/main`) |
| `{{.comment_author}}` | Author of the reviewer comment |
| `{{.comment_file}}` | File path the comment targets |
| `{{.comment_line}}` | Line number the comment targets |
| `{{.comment_body}}` | The reviewer's comment text |
| `{{.comment_thread}}` | Full thread history for the comment |
| `{{.code_context}}` | Code surrounding the commented line |

### Override Mechanism

Users can override any built-in prompt by placing a file with the same name in `~/.config/otto/prompts/`:

```
~/.config/otto/prompts/
  requirements.md      # Overrides built-in requirements prompt
  design.md            # Overrides built-in design prompt
  review.md            # Overrides built-in review prompt
  ...                  # Any template file can be overridden
```

**Resolution order:**
1. `~/.config/otto/prompts/<name>.md` — user override (wins if present)
2. `internal/prompts/<name>.md` — built-in default (embedded in binary via `go:embed`)

This is rigid Go code — a simple file existence check, then read. The template loading logic is in `internal/prompts/loader.go`.

### Embedding

Built-in prompts are embedded into the otto binary using Go's `embed` package:

```go
package prompts

import (
    "embed"
    "text/template"
)

//go:embed *.md
var builtinFS embed.FS

// Load returns the prompt template for the given name.
// Checks user override at ~/.config/otto/prompts/<name>.md first.
func Load(name string) (*template.Template, error) {
    userPath := filepath.Join(configDir, "prompts", name)
    if data, err := os.ReadFile(userPath); err == nil {
        return template.New(name).Parse(string(data))
    }
    data, err := builtinFS.ReadFile(name)
    if err != nil {
        return nil, err
    }
    return template.New(name).Parse(string(data))
}

// Execute loads a template and executes it with the given data map.
func Execute(name string, data map[string]string) (string, error) {
    tmpl, err := Load(name)
    if err != nil {
        return "", err
    }
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", err
    }
    return buf.String(), nil
}
```

### Codebase Analysis

When the repository is non-empty, otto analyzes it before running any pipeline stage. The analysis produces a `codebase_summary` that is injected into the requirements, research, design, and task prompts via the `{{.codebase_summary}}` template variable.

The analysis discovers:
- **Project archetype** — REST API, gRPC service, Kubernetes controller/operator, CLI tool, library, event-driven system, or hybrid
- **Logging** — library (slog, zap, logr, zerolog), format, contextual field conventions
- **Metrics** — instrumentation library, collector/exporter, naming conventions
- **Tracing** — library, context propagation, span naming
- **Error handling** — wrapping style, custom types, sentinel errors, error codes
- **Configuration** — loading mechanism (env vars, flags, config files, CRDs), validation
- **Testing** — framework (table-driven, testify, ginkgo, envtest), fixtures, mocks/fakes
- **Project layout** — directory structure, package naming conventions
- **Dependency injection** — manual wiring, wire, fx, controller-runtime manager
- **Dependencies** — key libraries with versions from manifest (go.mod, package.json, etc.)

This ensures prompts are tailored to the project type. A Kubernetes operator gets controller-specific guidance (reconciliation loops, CRD schemas, status conditions), not generic REST API advice. An existing codebase using `logr` won't get recommendations to switch to `zap`.

The analysis is generated by otto's Go code reading the repository — not an LLM call. It feeds into LLM prompts as context.

### Design Rationale

- **Markdown files, not Go string constants** — prompts are long, nuanced, and benefit from proper formatting. Editing a `.md` file is far better than editing a Go string literal.
- **Embedded in the binary** — no runtime dependency on file paths. The binary is self-contained.
- **User-overridable** — power users can tune prompts without recompiling. This is critical for adapting to different codebases and team preferences.
- **Version-controlled** — prompts change over time as we learn what works. They should be reviewed in PRs like any other code.

---

## Configuration

Otto uses JSONC (JSON with Comments) for configuration. Two layers, deep-merged with repo-level overriding user-level:

| Location | Purpose |
|----------|---------|
| `~/.config/otto/otto.jsonc` | User-level defaults (models, credentials, repos) |
| `.otto/otto.jsonc` (in CWD / repo root) | Repo-specific overrides |

### Merge Semantics

Configuration uses **deep merge**: repo-level values override user-level values at every nesting level, not just the top level. This means you can override a single nested key without replacing the entire parent object.

Example — user config sets all models; repo config overrides only the primary model:

```jsonc
// ~/.config/otto/otto.jsonc (user)
{
  "models": {
    "primary": "github-copilot/claude-opus-4.6",
    "secondary": "github-copilot/gpt-5.2-codex"
  }
}

// .otto/otto.jsonc (repo)
{
  "models": {
    "primary": "github-copilot/claude-opus-4.6"  // Override only primary
  }
}

// Merged result:
{
  "models": {
    "primary": "github-copilot/claude-opus-4.6",  // From repo
    "secondary": "github-copilot/gpt-5.2-codex"                           // From user
  }
}
```

Arrays are **replaced**, not merged — e.g., if repo config defines `repos`, it replaces the entire user-level `repos` array. Deep merge applies only to objects/maps.

### Configuration Schema

```jsonc
{
  // --- Models ---
  // All LLM interactions use the multi-model review pipeline.
  // Models are specified in OpenCode provider/model format.
  "models": {
    "primary": "github-copilot/claude-opus-4.6",   // Main workhorse
    "secondary": "github-copilot/gpt-5.2-codex"                           // Critical reviewer (required)
  },

  // --- OpenCode ---
  "opencode": {
    "url": "http://localhost:4096",          // Default server URL
    "auto_start": true,                      // Start opencode serve if not running
    "password": "",                          // OPENCODE_SERVER_PASSWORD env preferred
    "permissions": "allow"                   // Auto-approve all tool calls (yolo mode)
  },

  // --- PR Backends ---
  "pr": {
    "default_provider": "ado",               // Used when `otto pr add <id>` (no URL)
    "max_fix_attempts": 5,
    "providers": {
      "ado": {
        "organization": "myorg",
        "project": "myproject",
        "pat": "",                           // OTTO_ADO_PAT env preferred
        // ADO-specific workflow: auto-complete, MerlinBot, work items
        "auto_complete": true,
        "merlinbot": true,
        "create_work_item": true
      },
      "github": {
        "token": ""                          // GITHUB_TOKEN env preferred
      }
    }
  },

  // --- Repos ---
  // Tracked repos for worktree-based and branch-based workflows.
  // git_strategy controls how otto manages branches/worktrees:
  //   "worktree"  — use git worktrees for isolation (default)
  //   "branch"    — use branches in the primary directory (no worktrees)
  //   "hands-off" — otto does not create/manage branches or worktrees;
  //                 only commits and diffs within whatever is already checked out
  "repos": [
    {
      "name": "my-service",
      "primary_dir": "/home/user/repos/my-service",       // Main clone directory
      "worktree_dir": "/home/user/repos/my-service-wt",   // Where worktrees live (worktree strategy only)
      "git_strategy": "worktree",                          // "worktree" | "branch" | "hands-off"
      "branch_template": "users/alanmeadows/{{.name}}",   // Naming schema for branches/worktrees
      "branch_patterns": ["feature/*", "fix/*", "users/alanmeadows/*"]
    }
  ],

  // --- Server ---
  "server": {
    "poll_interval": "2m",
    "port": 4097,
    "log_dir": "~/.local/share/otto/logs"
  },

  // --- Spec ---
  "spec": {
    "max_parallel_tasks": 4,                 // Concurrent task execution limit
    "task_timeout": "30m",                   // Per-task timeout
    "max_task_retries": 15,                  // Max retry attempts per failed task
    "task_briefing": true                    // Pre-execution task briefing via LLM (default: true)
  }
}
```

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `OTTO_ADO_PAT` | Azure DevOps personal access token |
| `GITHUB_TOKEN` | GitHub token for PR backend |
| `OPENCODE_SERVER_PASSWORD` | OpenCode server basic auth password |
| `OPENCODE_SERVER_USERNAME` | OpenCode server basic auth username (default: `opencode`) |

---

## OpenCode Integration

Otto delegates **all** LLM work to OpenCode. It does not call model APIs directly.

### Architecture Decision: Single Server, Directory-Scoped Sessions

After researching the OpenCode SDK and server architecture, the integration model leverages a critical SDK feature: **every API method accepts a `directory` query parameter** that scopes the operation to a specific working directory. This means one OpenCode server can operate across many worktrees simultaneously without spawning per-worktree processes.

1. **Otto manages one `opencode serve` instance** — launched automatically if `opencode.auto_start` is true and no server is reachable at the configured URL. Otto tracks the child process via PID and shuts it down on exit. The server is started via `os/exec.Command("opencode", "serve")` from the primary repository root.
2. **Directory-scoped sessions** — every SDK call (sessions, file operations, commands, events) accepts a `Directory` parameter that tells OpenCode which worktree to operate in. When creating a session for a PR fix in worktree `/home/user/repos/my-service-wt/fix-auth`, otto passes `Directory: opencode.F("/home/user/repos/my-service-wt/fix-auth")`. OpenCode resolves its project configuration, file access, and git context for that directory.
3. **No per-worktree server processes** — because the `directory` parameter is per-request, a single OpenCode server handles all worktrees. Otto does not need a process pool, port allocation, or routing registry. This dramatically simplifies lifecycle management.
4. **Each LLM task gets its own session** — `client.Session.New()` creates an isolated session with clean context, scoped to the target directory. This is critical for task execution where each task must start without accumulated context pollution.
5. **Parallelism via concurrent sessions** — OpenCode's server handles multiple sessions concurrently, even across different directories. Otto can run N tasks in parallel (configured by `spec.max_parallel_tasks`), each in its own directory-scoped session, all against the same server. A PR fix in one worktree runs concurrently with spec execution in another.
6. **Synchronous prompts for most operations** — `client.Session.Prompt()` blocks until the LLM completes. This is the primary interaction mode for spec commands and PR fixes.
7. **SSE events for monitoring** — `client.Event.ListStreaming()` provides real-time progress for long-running operations, also directory-scoped.

### Server Lifecycle

Otto's OpenCode server lifecycle is managed by the `internal/opencode/` package:

```go
type ServerManager struct {
    cmd     *exec.Cmd      // The opencode serve child process
    client  *opencode.Client
    baseURL string         // e.g. http://localhost:4096
}

// EnsureRunning starts opencode serve if not already reachable.
// Called before any operation that needs LLM access.
func (m *ServerManager) EnsureRunning(ctx context.Context) error {
    // 1. Health check: GET /global/health
    if m.isHealthy(ctx) {
        return nil // Already running (otto started it, or it was external)
    }

    // 2. Start: opencode serve (from repo root)
    m.cmd = exec.CommandContext(ctx, "opencode", "serve")
    m.cmd.Dir = repoRoot  // CWD for default config resolution
    m.cmd.Env = append(os.Environ(),
        "OPENCODE_SERVER_PASSWORD="+cfg.OpenCode.Password,
    )
    if err := m.cmd.Start(); err != nil {
        return fmt.Errorf("failed to start opencode serve: %w", err)
    }

    // 3. Wait for healthy (poll /global/health with backoff)
    return m.waitForHealthy(ctx, 30*time.Second)
}

// Shutdown gracefully stops the child process ONLY if otto started it.
// If the server was already running externally, otto does not touch it.
func (m *ServerManager) Shutdown() error {
    if m.cmd != nil && m.cmd.Process != nil {
        // Otto started this process — shut it down
        m.cmd.Process.Signal(syscall.SIGTERM)
        m.cmd.Wait()
    }
    // If m.cmd is nil, the server was external — leave it alone
    return nil
}
```

### Directory-Scoped SDK Usage

Otto uses the official Go SDK at `github.com/sst/opencode-sdk-go`. The key pattern is passing the target `Directory` on every call:

```go
import opencode "github.com/sst/opencode-sdk-go"

// Create client pointed at the single running opencode serve instance
client := opencode.NewClient(
    option.WithBaseURL("http://localhost:4096"),
)

// --- Spec execution: session scoped to the repo worktree ---
worktreeDir := "/home/user/repos/my-service/"  // or a git worktree path

session, _ := client.Session.New(ctx, opencode.SessionNewParams{
    Title:     opencode.F("Implement JWT middleware"),
    Directory: opencode.F(worktreeDir),  // <-- scoped to this worktree
})

response, _ := client.Session.Prompt(ctx, session.ID, opencode.SessionPromptParams{
    Parts: opencode.F([]opencode.SessionPromptParamsPartUnion{
        opencode.TextPartInputParam{
            Type: opencode.F(opencode.TextPartInputTypeText),
            Text: opencode.F("Implement the JWT validation middleware..."),
        },
    }),
    Model: opencode.F(opencode.SessionPromptParamsModel{
        ProviderID: opencode.F("github-copilot"),
        ModelID:    opencode.F("claude-opus-4.6"),
    }),
    Directory: opencode.F(worktreeDir),  // <-- same directory scope
})

// --- PR fix: session scoped to the PR's worktree ---
prWorktree := "/home/user/repos/my-service-wt/fix-auth/"

fixSession, _ := client.Session.New(ctx, opencode.SessionNewParams{
    Title:     opencode.F("Fix failing unit tests"),
    Directory: opencode.F(prWorktree),   // <-- scoped to PR worktree
})

// Both sessions run concurrently against the same server,
// but OpenCode resolves files, config, and git context per directory.
```

### Key SDK Methods

All methods accept a `Directory` parameter for worktree scoping:

| Method | Purpose | Directory Scoping |
|--------|---------|-------------------|
| `client.Session.New()` | Create a fresh isolated session | Session bound to directory |
| `client.Session.Prompt()` | Send prompt, wait for LLM to complete | Operations scoped to session's directory |
| `client.Session.Messages()` | Retrieve all messages in a session | Per-directory |
| `client.Session.Abort()` | Cancel a running prompt | Per-directory |
| `client.Session.Delete()` | Clean up a session | Per-directory |
| `client.Session.List()` | List all sessions | Filterable by directory |
| `client.Event.ListStreaming()` | SSE event stream for monitoring | Per-directory |
| `client.File.Read()` | Read a file | Resolved relative to directory |
| `client.File.Status()` | Git status | For directory's repo |
| `client.Find.Files()` | Find files by query | Within directory's project |
| `client.Project.Current()` | Get current project info | For directory |
| `client.Path.Get()` | Get config/state/worktree paths | For directory |

### Multi-Worktree Concurrency Model

Otto's parallel operations share a single OpenCode server. The concurrency looks like:

```
┌─────────────────────────────────────────────────────┐
│              Single opencode serve                  │
│              (http://localhost:4096)                 │
│                                                     │
│  ┌──────────────┐  ┌──────────────┐                 │
│  │ Session A     │  │ Session B     │                │
│  │ dir: /repo/wt1│  │ dir: /repo/wt2│                │
│  │ (spec task 1) │  │ (PR fix #123) │                │
│  └──────────────┘  └──────────────┘                 │
│  ┌──────────────┐  ┌──────────────┐                 │
│  │ Session C     │  │ Session D     │                │
│  │ dir: /repo/wt1│  │ dir: /repo/wt3│                │
│  │ (spec task 2) │  │ (PR fix #456) │                │
│  └──────────────┘  └──────────────┘                 │
└─────────────────────────────────────────────────────┘

Sessions A & C share the same worktree (parallel tasks in same spec).
Sessions B & D operate on different PR worktrees.
All four run concurrently against one server.
```

### Permissions

For automated operation, otto configures OpenCode to auto-approve all tool operations. This is equivalent to OpenCode's `--yolo` mode. The `opencode.json` placed in each working directory by otto sets:

```json
{
  "permission": {
    "edit": "allow",
    "bash": "allow",
    "webfetch": "allow"
  }
}
```

Otto writes this file into each worktree directory before starting LLM operations. Since different worktrees may be different git checkouts, otto ensures the permission config exists in each target directory.

### Session Cleanup

Otto deletes OpenCode sessions after they are no longer needed — but **only after collecting dialog logs** where the workflow calls for it. This prevents session accumulation while preserving the audit trail.

**Cleanup rules:**

| Context | Cleanup Behavior |
|---------|-----------------|
| Task briefing | Session deleted immediately after brief is generated |
| Task execution | Session deleted **after** dialog log is extracted for question harvesting and run summary |
| Phase review gate | Session deleted after review feedback is captured |
| External assumptions validation | Session deleted after fixes are applied |
| Domain hardening | Session deleted after improvements are applied |
| Documentation alignment | Session deleted after doc updates are committed |
| PR fix | Session deleted after fix result (commit hash or failure) is recorded in PR document |
| PR comment response | Session deleted after reply is posted and PR document updated |
| PR review (`pr review`) | Session deleted after comments are posted (or user declines) |
| Question auto-resolution | Session deleted after answer is validated and written to questions.md |
| Ad-hoc `spec run` | Session **not** deleted — user may want to continue the conversation |

The pattern in Go:

```go
// After task execution completes and logs are saved:
dialog, _ := client.Session.Messages(ctx, session.ID, opencode.SessionMessagesParams{
    Directory: opencode.F(worktreeDir),
})
saveDialogLog(dialog)  // Write to history/run-NNN.md
client.Session.Delete(ctx, session.ID, opencode.SessionDeleteParams{
    Directory: opencode.F(worktreeDir),
})
```

---

## Multi-Model Review Pipeline

A core requirement: almost all LLM-generated artifacts pass through a multi-model critical review process before being finalized. This is **internal and silent** — the user sees only the final refined output.

### Pipeline Flow

```
┌─────────────────────────────────────────────────────────┐
│                  Multi-Model Review                     │
│                                                         │
│  1. Primary model generates initial artifact            │
│     (requirements, design, tasks, code fix, etc.)       │
│                                                         │
│  2. Secondary model (clean session) reviews:            │
│     "Critically review this artifact. Identify gaps,    │
│      errors, inconsistencies, missing considerations."  │
│                                                         │
│  3. Primary model (new session) incorporates all        │
│     critical feedback and produces final version        │
│                                                         │
│  4. If pass 3 differs substantially from pass 1,       │
│     optionally repeat steps 2-3 (max 2 cycles)         │
└─────────────────────────────────────────────────────────┘
```

### Implementation

```go
type ReviewPipeline struct {
    client    *opencode.Client
    primary   ModelRef   // e.g. github-copilot/claude-opus-4.6
    secondary ModelRef   // e.g. github-copilot/gpt-5.2-codex
}

// Review runs the multi-model pipeline, returns the final artifact
func (p *ReviewPipeline) Review(ctx context.Context, prompt string, contextFiles []string) (string, error) {
    // Pass 1: Primary generates
    initial := p.generate(ctx, p.primary, prompt, contextFiles)

    // Pass 2: Secondary critiques (clean session)
    critique := p.critique(ctx, p.secondary, initial)

    // Pass 3: Primary incorporates feedback (clean session)
    final := p.refine(ctx, p.primary, initial, critique)

    return final, nil
}
```

Each step uses a **fresh OpenCode session** — directory-scoped to the target worktree — to avoid context contamination between the generator and reviewers.

### Where Multi-Model Review is Applied

| Command | What gets reviewed |
|---------|--------------------|
| `otto spec requirements` | requirements.md |
| `otto spec research` | research.md |
| `otto spec design` | design.md |
| `otto spec task generate` | tasks.md |
| `otto spec execute` | Each phase's uncommitted work (inter-phase review gate) |
| `otto spec execute` | External assumption validation & repair (primary model, per-phase, after review gate) |
| `otto spec execute` | Domain hardening & polishing (primary model, per-phase, after external assumptions) |
| `otto spec execute` | Documentation alignment (primary model with secondary review, end of all phases) |
| `otto pr fix` | Each fix attempt prompt and result |

### Where it is NOT Applied

- `otto spec run` — ad-hoc prompt, user controls the flow
- `otto spec questions` — interactive Q&A, not a generated artifact
- Routine status checks and listing commands

---

## Specification System (`otto spec`)

The spec system is Otto's core value proposition. It provides a structured pipeline for turning a natural language prompt into researched, designed, and executed code changes.

### Spec Directory Structure

Each specification lives under `.otto/specs/<slug>/`:

```
.otto/specs/add-authentication/
  requirements.md        # What we're building (user-editable, LLM-refineable)
  research.md            # Technical research, API docs, prior art
  design.md              # Architecture and implementation approach
  tasks.md               # Ordered task list with parallelization hints
  questions.md           # Questions raised during any stage
  history/               # Execution history
    run-001.md           # Summary of each task run
    run-002.md
```

### Enforced Pipeline Flow

Otto enforces a strict serial progression through the spec pipeline. Each phase has prerequisites that must be satisfied before it can run:

```
requirements  →  research  →  design  →  task generate  →  execute
     1              2           3             4               5
```

| Command | Requires |
|---------|----------|
| `otto spec requirements` | requirements.md exists (created by `spec add`) |
| `otto spec research` | requirements.md |
| `otto spec design` | requirements.md, research.md |
| `otto spec task generate` | requirements.md, research.md, design.md |
| `otto spec execute` | requirements.md, research.md, design.md, tasks.md |

**Artifact gates only**: otto checks that prerequisite files exist before allowing a command to run. This is rigid Go code — simple file existence checks. Questions are **never** a gate — they are captured as advisory context but do not block any pipeline stage.

When a user tries to skip ahead, otto tells them exactly what to do:

```
$ otto spec design --spec add-auth
Error: cannot run design — research has not been completed yet.
  Next step: otto spec research --spec add-auth
```

If unanswered questions exist, they are included as context in subsequent LLM prompts (so the LLM is aware of open uncertainties), but they never prevent execution. The user can address them at any time via `otto spec questions`.

The `otto spec run` command is exempt from pipeline enforcement — it's an ad-hoc escape hatch for freeform interaction with spec context.

### Idempotency Principle

Idempotency is a core design principle across all of otto, not just spec artifacts. Every command is safe to re-run and produces a meaningful result:

**Spec artifact commands** (`requirements`, `research`, `design`, `task generate`) are refinement operations, not just creation operations. On first run they create the artifact; on subsequent runs they refine it by reading the existing artifact alongside all other spec documents and producing an improved version. This means:

- `otto spec design` with an existing design.md reads the current design, requirements.md, research.md, tasks.md, and questions.md, and produces a refined design that incorporates any changes to upstream artifacts or answers to questions since the last run.
- `otto spec research` with an existing research.md reads the current research alongside updated requirements and re-researches, filling gaps or correcting outdated information.
- Editing any file (requirements, design, tasks) and re-running the downstream commands automatically picks up those changes.

**`otto spec execute`** is resumable. It reads task statuses from tasks.md and only runs tasks with `status: pending`. If execution was interrupted (crash, timeout, user abort), re-running `otto spec execute` picks up exactly where it left off — completed phases stay committed, the current phase resumes any pending tasks, and the review gates run as normal.

**`otto pr fix`** is naturally idempotent — it reads the current pipeline state and only acts if there are failures to fix.

**`otto server install`** can be re-run safely — it overwrites the systemd unit file and reloads the daemon.

**`otto config set`** is inherently idempotent.

The general rule: **re-running any otto command should never make things worse, and should always incorporate the latest state.**

### Spec Commands

#### `otto spec add <prompt>`

Creates a new spec with an initial requirements.md derived from the prompt.

```
$ otto spec add "Add JWT-based authentication to the API"
Created: .otto/specs/add-jwt-authentication/
  requirements.md: Initial requirements generated
  slug: add-jwt-authentication
```

1. Generate slug from prompt
2. Create directory
3. Primary model generates initial requirements.md from prompt
4. Run multi-model review on requirements.md
5. Write final requirements.md
6. Print slug and path

#### `otto spec list`

Lists all specs in `.otto/specs/` with their current state (which artifacts exist, task completion status).

#### `otto spec requirements [--spec <slug>]`

**Prerequisites**: requirements.md must exist (created by `spec add`).

Refines and analyzes requirements.md. The LLM evaluates whether the requirements are sufficient to begin research, identifies ambiguities, and suggests improvements. Applies multi-model review.

If `--spec` is omitted and only one spec exists, it is used automatically.

The LLM is prompted to assess:
- Completeness: are all user stories / use cases covered?
- Clarity: are there ambiguous terms or conflicting requirements?
- Feasibility: are there obvious technical blockers?
- Testability: can each requirement be verified?

If issues are found, they are added to questions.md.

#### `otto spec research [--spec <slug>]`

**Prerequisites**: requirements.md must exist.

Creates or refines research.md. On first run, the LLM researches the technical landscape needed to implement the requirements. On subsequent runs, it reads the existing research.md and updates it — filling gaps, correcting outdated information, and incorporating any changes to upstream artifacts.

Context provided: requirements.md, existing research.md, design.md, and tasks.md if present.

#### `otto spec design [--spec <slug>]`

**Prerequisites**: requirements.md and research.md must exist.

Creates or refines design.md. On first run, the LLM produces an architecture and implementation approach. On subsequent runs, it reads the existing design.md and refines it — incorporating updated requirements, new research findings, answered questions, and any manual edits.

Context provided: requirements.md, research.md, existing design.md, tasks.md, and questions.md.

#### `otto spec task generate [--spec <slug>]`

**Prerequisites**: requirements.md, research.md, and design.md must exist.

Creates or refines tasks.md from the full spec context. On first run, generates the full task breakdown. On subsequent runs, reads the existing tasks.md and updates it — adding tasks for new requirements, adjusting descriptions for design changes, and preserving the status of any already-completed tasks. Tasks are structured for the execution engine:

```markdown
# Tasks

## Task 1: Create JWT middleware
- **id**: task-001
- **status**: pending          <!-- pending | running | completed | failed | skipped -->
- **parallel_group**: 1        <!-- tasks in the same group can run concurrently -->
- **depends_on**: []
- **description**: Create the JWT validation middleware...
- **files**: ["internal/auth/jwt.go", "internal/auth/jwt_test.go"]

## Task 2: Add login endpoint
- **id**: task-002
- **status**: pending
- **parallel_group**: 1
- **depends_on**: []
- **description**: Implement POST /auth/login...

## Task 3: Integrate auth into routes
- **id**: task-003
- **status**: pending
- **parallel_group**: 2
- **depends_on**: [task-001, task-002]
- **description**: Wire the JWT middleware into existing routes...
```

#### `otto spec task list [--spec <slug>]`

Displays the task list with current status, parallelization groups, and progress summary.

#### `otto spec task add [--spec <slug>] <prompt>`

Adds a task to tasks.md. This is a good example of the LLM-first philosophy: otto does **not** implement logic to parse the task graph, evaluate dependencies, or assign parallel groups. Instead, it sends the full current tasks.md plus the new task prompt to the LLM with instructions to:

1. Evaluate the new task in the context of existing tasks
2. Assign an appropriate ID following the existing convention
3. Determine which tasks it depends on
4. Place it in the correct parallel group (or create a new one)
5. Splice it into tasks.md in the right position

Otto then writes the LLM's output back to tasks.md. The Go code's only job is to read the file, build the prompt, call the LLM, and write the result.

#### `otto spec task run [--spec <slug>] [--id <taskid>]`

Runs a single task in isolation. Creates a new OpenCode session, provides the task description plus relevant spec context, and captures the result. Updates the task status in tasks.md.

If `--id` is omitted and only one pending task (with satisfied dependencies) exists, it runs automatically.

#### `otto spec execute [--spec <slug>]`

**Prerequisites**: requirements.md, research.md, design.md, and tasks.md must all exist.

**The execution engine.** This is the most critical and carefully designed component. Runs locally in the CLI process (not in the daemon).

See [Task Execution Engine](#task-execution-engine) below.

#### `otto spec questions [--spec <slug>]`

Presents open questions to the user, but first attempts to auto-resolve as many as possible via LLM research.

**Auto-elimination flow:**

1. Read questions.md, filter to `status: unanswered`
2. For each unanswered question, create an LLM session that attempts to research and answer it — using the full spec context, the codebase, and web research tools
3. For each question the primary model was able to answer:
   - Secondary model (clean session) validates: "Is this answer correct, complete, and well-supported? Or does this genuinely need human input?"
   - If reviewers agree the answer is solid → auto-answer the question in questions.md with `status: auto-answered` and the source reasoning
   - If any reviewer disagrees → keep the question for the user
4. Present remaining unanswered questions to the user one at a time
5. Record user answers inline in questions.md with `status: answered`

Questions can come from any stage: requirements refinement, design, research, or — most commonly — the question harvesting pass that scans task execution dialog logs after each phase.

questions.md format:
```markdown
# Questions

## Q1: Database migration strategy
- **source**: design (2026-02-06)
- **status**: answered
- **question**: Should we use a migration framework or raw SQL?
- **answer**: Use golang-migrate. We already have it as a dependency.

## Q2: Token expiration policy
- **source**: task-003 (2026-02-06)
- **status**: unanswered
- **question**: What should the JWT token expiration time be?

## Q3: Error response format
- **source**: phase-1-harvest (2026-02-06)
- **status**: auto-answered
- **question**: Should auth errors return 401 with a JSON body or just a status code?
- **answer**: Return 401 with a JSON body containing an `error` field and `message` field, consistent with existing API error responses in handlers.go.
- **validated_by**: secondary
```

#### `otto spec run [--spec <slug>] <prompt>`

Runs an ad-hoc prompt with the full spec context loaded. All files in `.otto/specs/<slug>/` are included as context. This lets users do things like:

```bash
otto spec run --spec add-auth "Refine and clean up requirements.md"
otto spec run --spec add-auth "What are the security implications of this design?"
```

No multi-model review is applied — the user drives this interaction directly.

---

## Task Execution Engine

`otto spec execute` is the engine for reliably running tasks from tasks.md. It is an **outer non-LLM loop** that orchestrates LLM calls but is itself deterministic and crash-resilient. It runs **locally in the CLI process** — not in the daemon — and spawns its own OpenCode server via `ServerManager.EnsureRunning()` if one isn't already available.

### Design Principles

1. **The outer loop never dies** — errors are caught, logged, and the loop continues. This is an example of where rigid Go code is the right choice: the loop must be deterministic and resilient regardless of what the LLM does.
2. **Each task runs in an isolated OpenCode session** — clean context, no bleed between tasks
3. **Task briefing** — before executing each task, a preparatory LLM call reads all spec artifacts (requirements, research, design, tasks) and distills a focused implementation brief specific to that task. This replaces the naive approach of dumping all context documents into every task prompt. The executor receives a crisp, task-relevant brief plus pointers to the raw spec files so it can explore further if needed. Briefing is enabled by default (`spec.task_briefing: true`) and gracefully falls back to the static context dump if the briefing call fails.
4. **Parallel execution** — tasks in the same phase (`parallel_group`) run concurrently, bounded by `spec.max_parallel_tasks`
5. **Phase review gate** — after all tasks in a phase complete, the secondary model produces a review report (no file changes), then the primary model reads the report and applies fixes before the phase is committed
6. **Post-review hardening** — after the phase review gate, two additional primary-model passes run sequentially: (a) External Assumption Validator & Repair — finds and fixes invalid, fragile, or unverifiable assumptions about external systems; (b) Domain Hardening & Polishing — improves resilience, clarity, and operability. Both modify the working tree directly.
7. **Clean phase boundaries** — each phase starts from a committed state, so the next phase builds on reviewed work
8. **Status persistence** — task status is written to tasks.md after each task completes, providing crash recovery
9. **Resumability** — `otto spec execute` can be re-run at any time. It reads task statuses from tasks.md and resumes from the first incomplete phase. Completed phases (already committed) are skipped. A partially-completed phase resumes its pending tasks.
10. **Question harvesting (non-blocking)** — after each phase, a dedicated LLM pass scans the complete dialog logs of all task executions in that phase, extracting any uncertainties, assumptions, open questions, or concerns. These are appended to questions.md but **never halt execution**. They serve as advisory context — subsequent phases and LLM prompts receive the questions as input so the LLM is aware of open uncertainties, but the outer loop continues regardless. The user can review and address questions at any time via `otto spec questions`.
11. **Summary chaining** — each phase's commit summary is fed into the next phase's task prompts as context, so the LLM knows what changed
12. **Task retry on failure** — a failed task is retried until it succeeds, up to `spec.max_task_retries` (default 15). Tasks should never permanently block progress. On each retry, a fresh OpenCode session is created with the task description, the error/output from the previous attempt, and additional instruction to correct the failure. Only after exhausting all retries is a task marked `failed` — and even then the outer loop continues to the next phase if possible.
13. **Parallel task conflict acceptance** — multiple tasks in the same phase may run concurrently in the same worktree. This means file conflicts, merge issues, or stale reads are possible. Otto accepts this: some error rate is tolerable and will be caught by the phase review gate or by task retries. The system is eventually consistent. This is a pragmatic tradeoff — perfect isolation (separate worktrees per task) would add enormous complexity for marginal benefit.
14. **Documentation alignment** — after all phases complete, a final documentation alignment pass ensures all docs in the repo accurately reflect the current behavior of the branch. This uses the primary model with secondary review, and only modifies documentation — never runtime behavior.

### Execution Flow

Tasks are organized into **phases** (parallel groups). All tasks in a phase run concurrently, then the phase's output is reviewed before committing and advancing to the next phase.

```
otto spec execute [--spec <slug>]

┌─────────────────────────────────────────────────────────┐
│  Outer Loop (deterministic, non-LLM)                    │
│                                                         │
│  1. Parse tasks.md                                      │
│  2. Build dependency graph                              │
│  3. Identify next phase (parallel_group with all        │
│     deps satisfied and status=pending)                  │
│  4. Launch all tasks in the phase concurrently           │
│     (bounded by max_parallel_tasks)                     │
│  5. For each task in the phase:                          │
│     a. Create fresh OpenCode session                    │
│        (directory-scoped to the spec's worktree)        │
│     b. Task Briefing (if enabled):                      │
│        i.  Create briefing session (primary model)      │
│        ii. Send task-briefing.md prompt with ALL spec   │
│            artifacts + target task details               │
│        iii.LLM distills a focused implementation brief  │
│        iv. Delete briefing session                      │
│        v.  On failure: fall back to static prompt       │
│     c. Build executor prompt:                           │
│        - If briefed: brief + reference doc paths        │
│        - If not briefed: raw spec docs (requirements,   │
│          research, design) + task description            │
│        - Summaries of completed prior phases             │
│        - If retry: previous attempt error/output        │
│     d. Send prompt, wait for completion                 │
│     e. Evaluate result:                                 │
│        - Success → capture summary, update status       │
│        - Failure → increment retry count                │
│          - retries < max_task_retries → retry (goto a)  │
│          - retries >= max_task_retries → mark failed    │
│     f. Write run summary to history/run-NNN.md          │
│     g. Delete OpenCode session (after collecting logs)  │
│     h. Scan output for questions → append to questions  │
│        (non-blocking — execution continues regardless)  │
│  6. Wait for ALL tasks in the phase to complete          │
│                                                         │
│  ── Phase Review Gate ──────────────────────────────────│
│                                                         │
│  7. Two-step review of the phase's work:                │
│     a. Secondary model (clean session, no tools):       │
│        Produces a markdown review report identifying    │
│        bugs, edge cases, inconsistencies, incomplete    │
│        implementations. Does NOT modify files.          │
│     b. Primary model (clean session, tools enabled):    │
│        Reads the review report and applies fixes        │
│                                                         │
│  ── Post-Review Hardening (Primary Model) ──────────── │
│                                                         │
│  7d. Primary model (clean session):                     │
│      External Assumption Validator & Repair             │
│      - Enumerate external boundaries from diff          │
│      - Fix/guard invalid or fragile assumptions         │
│  7e. Primary model (clean session):                     │
│      Domain Hardening & Polishing                       │
│      - Improve resilience, clarity, operability         │
│      - Idempotency, retry discipline, error handling    │
│                                                         │
│  8. Commit all changes for this phase                   │
│     (git add -A && git commit)                          │
│                                                         │
│  ── Next Phase ──────────────────────────────────────── │
│                                                         │
│  9. Print progress: X/Y completed, Z running, W failed  │
│  10. If all tasks done → proceed to step 12             │
│      If only exhausted-retries tasks remain: warn, exit │
│  11. Otherwise: goto 3                                  │
│                                                         │
│  ── Documentation Alignment (End of Execution) ──────── │
│                                                         │
│  12. After ALL phases complete:                          │
│      a. Primary model (clean session):                  │
│         Documentation Alignment — update docs to        │
│         reflect current branch behavior                 │
│      b. Secondary model reviews doc changes             │
│      c. Primary incorporates feedback, finalizes docs   │
│      e. Commit documentation changes                    │
│         (git add -A && git commit)                      │
└─────────────────────────────────────────────────────────┘
```

The phase review gate ensures that each batch of parallel work is critically evaluated before being committed. After the review gate, two additional primary-model passes harden the phase's output: the **External Assumption Validator** finds and fixes invalid or fragile assumptions about external systems (APIs, infrastructure, auth, async behavior, rate limits), and the **Domain Hardening** pass improves resilience, error handling, and operability. Both modify the working tree directly before the phase is committed.

After all phases complete, a final **Documentation Alignment** pass ensures all documentation in the repo accurately reflects the current state of the branch. This uses the primary model to update docs, with secondary review to validate the changes. Only documentation is modified — never runtime behavior. The documentation changes are committed separately.

### Concurrent Access to tasks.md

Since multiple tasks can run in parallel but tasks.md is a shared file:

- **Locking strategy**: use a file-based mutex (`flock`) when updating tasks.md. Each parallel task worker acquires the lock before reading/modifying the file and releases it immediately after.
- **Minimal critical section**: only the status field update requires serialization. The task description itself is read before execution begins and doesn't change.
- **Alternative considered**: an in-memory task state with periodic flush was considered but rejected — file-based state provides crash recovery and is observable by the user mid-execution.

### Progress Output

During execution, otto prints a live status table showing phase-based progress:

```
otto spec execute --spec add-jwt-authentication

  Phase 1:
  task-001   completed   2m 14s    Created JWT middleware
  task-002   completed   2m 10s    Added login endpoint
  Phase 1 review:
    secondary: 2 issues found (missing error case, no input validation)
    primary: incorporating feedback... done
  Phase 1 committed: a1b2c3d

  Phase 2:
  task-003   running     0m 45s    ...
  task-004   pending     -         Waiting on task-003

  Progress: 2/4 completed | 1 running | 1 pending | 0 failed
```

---

## PR Backend System

Otto supports an extendable backend for PR lifecycle management. Each backend implements a common interface but can define provider-specific workflows.

### Backend Interface

```go
type PRBackend interface {
    // Identity
    Name() string                                    // "ado", "github"
    MatchesURL(url string) bool                      // Can this backend handle this URL?

    // Core operations
    GetPR(ctx context.Context, id string) (*PRInfo, error)
    GetPipelineStatus(ctx context.Context, pr *PRInfo) (*PipelineStatus, error)
    GetBuildLogs(ctx context.Context, pr *PRInfo, buildID string) (string, error)
    GetComments(ctx context.Context, pr *PRInfo) ([]Comment, error)
    PostComment(ctx context.Context, pr *PRInfo, body string) error
    PostInlineComment(ctx context.Context, pr *PRInfo, comment InlineComment) error
    ReplyToComment(ctx context.Context, pr *PRInfo, commentID string, body string) error
    ResolveComment(ctx context.Context, pr *PRInfo, commentID string, resolution CommentResolution) error

    // Workflow — optional provider-specific lifecycle
    RunWorkflow(ctx context.Context, pr *PRInfo, action WorkflowAction) error
}

type InlineComment struct {
    FilePath string
    Line     int
    Body     string
    Side     string  // "LEFT" (old) or "RIGHT" (new)
}

type CommentResolution int

const (
    ResolutionResolved  CommentResolution = iota  // Comment addressed, mark resolved
    ResolutionWontFix                              // Acknowledged but won't change
    ResolutionByDesign                             // Intentional, explain rationale
)

type WorkflowAction int

const (
    WorkflowSubmit       WorkflowAction = iota  // Create/submit PR
    WorkflowAutoComplete                         // Set auto-complete (ADO)
    WorkflowCreateWorkItem                       // Create work item via comment (ADO)
    WorkflowAddressBot                           // Address bot comments (ADO MerlinBot)
)
```

### URL-Based Backend Detection

When adding a PR via URL, otto detects the backend from the hostname:

| Pattern | Backend |
|---------|---------|
| `*.visualstudio.com`, `dev.azure.com` | ADO |
| `github.com` | GitHub |

When adding by numeric ID, the `pr.default_provider` config value is used.

### ADO-Specific Workflow

ADO PRs follow a specific lifecycle that otto automates:
1. Create PR → set auto-complete
2. Watch for MerlinBot comments → address them automatically
3. Create work item (via PR comment convention)
4. Monitor pipeline → fix failures → re-push

### GitHub Workflow

GitHub PRs follow a simpler lifecycle:
1. Track PR → monitor checks
2. Fix failures → push to branch
3. No equivalent of MerlinBot or auto-complete

---

## PR Review Command (`otto pr review <url>`)

Reviews a **third-party** PR — one that is NOT tracked in otto's global PR store. The URL is always required; the PR is never added to the monitoring loop.

### Design: Local Checkout, Not Diff Injection

Instead of fetching the PR diff via API and injecting it into a prompt, otto checks out the PR branch locally and lets the LLM examine the code through OpenCode's tools. This gives the LLM full file context — it can read surrounding functions, check type definitions, follow imports, and understand the change in the context of the entire codebase. A raw diff strips all of that away.

### Flow

```
otto pr review <url>
  1. Detect backend from URL (ADO / GitHub)
  2. Fetch PR metadata via GetPR() — extract repo, source branch, target branch
  3. Map the PR to a local repo (from repos config, or CWD if it matches)
  4. Fetch and checkout the PR's source branch:
     - git fetch origin <source_branch>
     - Use an existing worktree or create a temporary one
     - git checkout <source_branch>
  5. Create OpenCode session (directory-scoped to the checked-out worktree)
  6. Send pr-review prompt with:
     - PR description/title
     - Target branch name (e.g., origin/main)
     - Instruction to run `git diff origin/<target>...HEAD` to see what changed
     - Instruction to examine the full files around each change for context
     - Repository conventions (from codebase_summary if available)
  7. LLM explores the changes using OpenCode tools:
     - Runs git diff to identify changed files and lines
     - Reads surrounding code to understand context
     - Follows references across files as needed
  8. LLM generates structured review: list of inline comments
     Each comment has: file, line, severity, body
  9. Present comments to user for interactive approval
  10. Post approved comments to backend via PostInlineComment()
  11. Optionally post a summary comment via PostComment()
  12. Clean up: if a temporary worktree was created, remove it
```

### Repo Resolution

Otto must find a local clone of the PR's repository. This uses the same [PR-to-Worktree Mapping](#pr-to-worktree-mapping) logic described in Repo Management:

1. **Repos config match** — check `repos` in config for a repo whose remote URL matches the PR's repo. Use its configured `primary_dir` and `worktree_dir` based on `git_strategy`.
2. **CWD match** — if the current working directory is a git repo whose remote matches the PR's repo, use it directly.
3. **No match** — error: "Cannot find local repo for <repo URL>. Add it with `otto repo add` or run from within the repo."

Otto never clones repos automatically. The user must have a local clone already.

### Branch Checkout Strategy

Once the repo is located, otto uses the repo's `git_strategy` to check out the PR's source branch:

- **worktree**: fetch and checkout the source branch in a worktree under `worktree_dir`. If the worktree already exists (e.g., otto created it earlier), reuse it.
- **branch**: fetch and checkout the source branch in the `primary_dir`.
- **hands-off**: verify the current branch matches; error if not.
- **Fallback (no strategy / CWD match)**: create a temporary worktree (`git worktree add /tmp/otto-review-<pr_id> <source_branch>`), use it for the review, and remove it afterward.

### Interactive Approval

After the LLM generates review comments, otto presents them in a table for the user to approve, skip, or edit before posting:

```
$ otto pr review https://github.com/org/repo/pull/42
Fetching PR #42... done
Mapping to repo: /home/user/repos/my-service
Checking out branch feature/add-auth...
Reviewing changes (8 files, git diff origin/main...HEAD)...
Generated 5 review comments.

  #  File                    Line  Severity  Comment
  1  src/auth/handler.go     45    warning   Missing error check on db.Query return...
  2  src/auth/handler.go     78    nitpick   Consider using constants for HTTP statu...
  3  src/auth/middleware.go   12    error     Race condition: shared map accessed with...
  4  src/models/user.go       34    warning   SQL injection risk: user input concatena...
  5  src/models/user.go       56    nitpick   Unused parameter 'ctx' in helper function

Approve comments to post (space to toggle, enter to confirm):
  [x] 1. handler.go:45 — Missing error check
  [x] 2. handler.go:78 — HTTP status constants
  [x] 3. middleware.go:12 — Race condition
  [x] 4. user.go:34 — SQL injection risk
  [ ] 5. user.go:56 — Unused parameter (excluded)

Post 4 comments? [y/N] y
Posted 4 inline comments on PR #42.
Cleaning up temporary worktree... done
```

The interactive picker uses `charmbracelet/huh` or a simple prompt loop. Each comment is individually toggleable. By default all are selected; the user deselects the ones they don't want.

### Review Prompt

The `pr-review.md` prompt template instructs the LLM to:
- Run `git diff origin/<target>...HEAD` to see what changed
- Read the full files around each changed area — not just the diff lines
- Focus on bugs, security issues, race conditions, and correctness problems
- Flag style/convention violations only when they contradict the project's own patterns
- Return structured output (JSON array of comments with file, line, severity, body)
- Avoid generic praise or noise — every comment should be actionable
- Follow imports, type definitions, and callers when evaluating a change's correctness

This is fundamentally superior to diff-based review because the LLM can:
- See the full function surrounding a change, not just the changed lines
- Check if a new function duplicates existing functionality elsewhere
- Verify that error types, constants, and interfaces are used correctly
- Understand the call chain and data flow through the changed code

### No Tracking

Unlike `otto pr add`, this command is **fire-and-forget**. The PR is not added to the global store, not monitored by the daemon, and not persisted. It is purely a CLI-driven one-shot review.

---

## PR Comment Monitoring

For **tracked** PRs (added via `otto pr add`), otto monitors for reviewer comments and addresses them automatically.

### Comment Lifecycle

When a reviewer posts a comment on one of otto's tracked PRs:

```
Comment detected on tracked PR
  │
  ├─ Is this a new, unresolved comment targeting otto's code?
  │    │
  │    ├─ Yes → Create OpenCode session with:
  │    │         - Comment body and thread context
  │    │         - The file and line being commented on
  │    │         - The full diff context around that line
  │    │         - pr-comment-respond prompt
  │    │
  │    │    LLM evaluates and decides:
  │    │    ├─ AGREE: Comment is valid
  │    │    │   → Fix the code in the worktree
  │    │    │   → Commit and push
  │    │    │   → Reply: "Fixed in <commit>" (via ReplyToComment)
  │    │    │   → Resolve the comment (via ResolveComment with ResolutionResolved)
  │    │    │
  │    │    ├─ BY_DESIGN: The code is intentional
  │    │    │   → Reply with rationale explaining why
  │    │    │   → Resolve the comment (via ResolveComment with ResolutionByDesign)
  │    │    │
  │    │    └─ WONT_FIX: Valid point but out of scope or too risky
  │    │        → Reply explaining why it won't be changed now
  │    │        → Resolve the comment (via ResolveComment with ResolutionWontFix)
  │    │
  │    └─ No (already resolved, or not targeting tracked code) → skip
  │
  └─ Update PR document with comment handling history
```

### Comment Tracking in PR Documents

The PR document model is extended with a comment history section:

```markdown
## Comment History

### Comment #1 — @reviewer at 2026-02-06T15:00:00Z
- **File**: src/auth/handler.go:45
- **Body**: "Missing error check on the db.Query return value"
- **LLM Decision**: AGREE
- **Action**: Fixed in commit e7f8g9h
- **Resolution**: Resolved

### Comment #2 — @reviewer at 2026-02-06T15:05:00Z
- **File**: src/auth/middleware.go:12
- **Body**: "Why not use sync.RWMutex here?"
- **LLM Decision**: BY_DESIGN
- **Action**: Replied with rationale (the map is only written during init)
- **Resolution**: Resolved (by design)
```

### Polling Strategy

Comment checking is integrated into the existing PR monitoring loop (same `poll_interval`). On each poll:

1. Fetch all comments via `GetComments()`
2. Diff against the set of previously-seen comment IDs (tracked in the PR document's frontmatter)
3. For each new, unresolved comment:
   - Create an OpenCode session (directory-scoped to the PR's worktree)
   - Send the `pr-comment-respond` prompt with the comment context
   - Apply the LLM's decision (fix + reply + resolve, or just reply + resolve)
4. Update the PR document with the new comment entries and seen-comment IDs

### Comment Response Prompt

The `pr-comment-respond.md` prompt template provides:
- The reviewer's comment body and any thread history
- The code being commented on (file, line range, surrounding context)
- The PR's overall purpose (from PR description)
- Instructions to decide: AGREE (fix it), BY_DESIGN (explain rationale), or WONT_FIX (explain scope)
- If AGREE: generate the fix and a concise reply message
- If BY_DESIGN or WONT_FIX: generate a respectful, technical reply explaining the reasoning

### Batching

If multiple new comments are found on the same PR in one poll cycle, they are processed sequentially within a single polling iteration. Each comment gets its own OpenCode session to avoid context cross-contamination. If a comment's fix conflicts with a previous comment's fix (both touch the same lines), the later comment's session will see the updated code from the earlier fix.

---

## PR Document Model

Each tracked PR is stored as markdown with YAML frontmatter:

```markdown
---
id: "12345"
provider: ado
repo: org/my-repo
branch: feature/add-auth
target: main
status: watching          # watching | fixing | green | failed | abandoned
url: https://dev.azure.com/org/project/_git/my-repo/pullrequest/12345
created: 2026-02-06T10:00:00Z
last_checked: 2026-02-06T14:30:00Z
fix_attempts: 2
max_fix_attempts: 5
seen_comment_ids:          # Track which comments have been processed
  - "comment-abc123"
  - "comment-def456"
---

# PR #12345: Add authentication module

## Current State
Pipeline run #78 **failed** at 2026-02-06T14:25:00Z.

### Failed Checks
- `unit-tests`: exit code 1
- `lint`: exit code 1

## Fix History

### Attempt 1 - 2026-02-06T12:00:00Z
- **Trigger**: Pipeline failure (unit-tests)
- **Action**: "Fix failing unit tests in auth module"
- **Result**: Partial fix, lint still failing
- **Commit**: a1b2c3d

### Attempt 2 - 2026-02-06T14:30:00Z
- **Trigger**: Pipeline failure (lint)
- **Action**: "Fix lint errors in auth module"
- **Result**: Pending
- **Commit**: d4e5f6g
```

PR documents are stored globally at `~/.local/share/otto/prs/` — not in a repo-local `.otto/` directory. All `otto pr` commands operate on this global store. When adding a PR, otto may read the CWD's `.otto/otto.jsonc` for repo context (e.g., default provider, worktree mapping), but the PR itself is tracked globally by the daemon across all repos.

### PR Fix Strategy: Two-Phase LLM Approach

Pipeline logs are often enormous. Rather than feeding raw logs to the fix LLM, otto uses a two-phase approach:

1. **Phase 1 — Log analysis session**: Create an OpenCode session that has access to the downloaded log files. Prompt it to distill the logs into a concise failure summary: which tests failed, which lint rules were violated, the exact error messages, and file locations.
2. **Phase 2 — Fix session**: Create a fresh OpenCode session with the distilled failure summary (not the raw logs). Prompt it to fix the identified issues, commit, and push.

This prevents context pollution from massive log files and focuses the fix LLM on actionable information.

---

## Repo Management

Otto tracks repositories for multi-repo workflows. Repos are stored in `~/.config/otto/otto.jsonc` under `repos`.

### Repo Commands

```
otto repo add     # Interactive: prompts for primary dir, worktree dir, git strategy, branch template
otto repo remove  # Remove a repo by name
otto repo list    # List all tracked repos with strategy and directory info
```

Repo configuration tells otto where repos live, how to manage branches/worktrees, and which branch naming conventions to follow.

### Worktree Commands

```
otto worktree add <name> [--repo <repo>]    # Create a new worktree for a tracked repo
otto worktree list [--repo <repo>]           # List worktrees for a repo
otto worktree remove <name> [--repo <repo>]  # Remove a worktree
```

**`otto worktree add <name>`** creates a worktree using the repo's configured strategy:

1. Look up the repo (from `--repo` flag, or CWD if it matches a tracked repo).
2. Derive the branch name from the repo's `branch_template`: e.g., with template `users/alanmeadows/{{.name}}` and name `fix-auth`, the branch is `users/alanmeadows/fix-auth`.
3. Create the worktree:
   - **worktree strategy**: `git worktree add <worktree_dir>/<name> -b <branch_name>` — creates a new directory under the repo's `worktree_dir`.
   - **branch strategy**: `git checkout -b <branch_name>` in the primary directory — no new directory, just a new branch.
   - **hands-off strategy**: error — otto does not create worktrees or branches in hands-off mode.

**`otto worktree list`** shows all worktrees or branches managed by otto for a repo, including their current status (clean, dirty, active spec).

**`otto worktree remove <name>`** removes the worktree directory and optionally deletes the branch. Refuses to remove if there are uncommitted changes unless `--force` is passed.

### Git Strategies

Each repo defines a `git_strategy` that controls how otto manages branches and working directories:

| Strategy | Branch creation | Working directory | Use case |
|----------|----------------|-------------------|----------|
| `worktree` | Creates branch per `branch_template` | New directory under `worktree_dir` | Multi-task isolation, parallel work on same repo |
| `branch` | Creates branch per `branch_template` | Primary directory (switches branches) | Simple single-directory workflow |
| `hands-off` | **None** — otto never creates/switches branches | Whatever is currently checked out | User manages git entirely; otto only commits and diffs |

The `branch_template` uses Go `text/template` syntax. The `.name` variable is the worktree/branch name passed to `otto worktree add`:

```jsonc
// Template examples:
"branch_template": "users/alanmeadows/{{.name}}"    // → users/alanmeadows/fix-auth
"branch_template": "otto/{{.name}}"                  // → otto/fix-auth
"branch_template": "feature/{{.name}}"               // → feature/fix-auth
```

### Dirty State Handling

If otto encounters a dirty worktree or branch (uncommitted changes from a previous session, manual edits, or interrupted execution):

1. **Otto treats dirty state as its own responsibility.** It does not refuse to work or prompt the user for cleanup.
2. During phase execution, the LLM sees the dirty files as part of its context and can fix, incorporate, or discard them as appropriate.
3. At phase completion, **all** changes (including pre-existing dirty state) are committed together as part of the phase's commit.
4. If the dirty state causes task failures, the retry loop handles it — the LLM gets the error context and can correct the issue on the next attempt.

This means otto is resilient to partial state: a crashed execution, a manual edit mid-run, or leftover uncommitted files are all handled gracefully by treating them as part of the working context.

### PR-to-Worktree Mapping

When otto needs to operate on a tracked PR (fixing pipeline failures, responding to comments), it must find the local working directory for that PR. The mapping works as follows:

1. **Extract PR metadata** — from the PR backend, get the repo URL and source branch name (e.g., `users/alanmeadows/fix-auth`).
2. **Find the matching repo** — look up `repos` config for a repo whose git remote URL matches the PR's repo URL.
3. **Derive the working directory** based on the repo's `git_strategy`:

| Strategy | Mapping Logic |
|----------|--------------|
| `worktree` | Parse the branch name against `branch_template` to extract the worktree name. The working directory is `<worktree_dir>/<name>`. If the worktree doesn't exist locally, otto creates it: `git worktree add <worktree_dir>/<name> <branch>`. |
| `branch` | The working directory is `primary_dir`. Otto checks out the PR's branch: `git fetch origin <branch> && git checkout <branch>`. |
| `hands-off` | The working directory is `primary_dir`. Otto verifies the current branch matches the PR's branch. If not, it errors — the user must check out the correct branch manually. |

4. **No match** — error: "Cannot find local repo for `<repo URL>`. Add it with `otto repo add` or run from within the repo."

Otto never clones repos automatically. The user must have a local clone already.

---

## CLI Design

Commands follow a noun-verb pattern organized into subcommand groups.

### Commands

```
otto spec add <prompt>                           # Create new spec from prompt
otto spec list                                   # List all specs
otto spec requirements [--spec <slug>]           # Refine/analyze requirements.md
otto spec research [--spec <slug>]               # Create/refresh research.md
otto spec design [--spec <slug>]                 # Create/refresh design.md
otto spec task generate [--spec <slug>]          # Generate tasks.md
otto spec task list [--spec <slug>]              # List tasks with status
otto spec task add [--spec <slug>] <prompt>      # Add a task
otto spec task run [--spec <slug>] [--id <id>]   # Run one task
otto spec execute [--spec <slug>]                # Run all tasks (the engine)
otto spec questions [--spec <slug>]              # Interactive Q&A for open questions
otto spec run [--spec <slug>] <prompt>           # Ad-hoc prompt with spec context

otto pr add <url or id> [--provider ado|github]  # Track a PR (global)
otto pr list                                     # List ALL tracked PRs (global)
otto pr status [<id>]                            # Show detailed PR status (global)
otto pr remove <id>                              # Stop tracking (global)
otto pr fix [<id>]                               # Manually trigger fix (global)
otto pr log [<id>]                               # Show fix history (global)
otto pr review <url>                             # Review a third-party PR (one-shot)

otto repo add                                    # Add a repo
otto repo remove                                 # Remove a repo
otto repo list                                   # List repos

otto worktree add <name> [--repo <repo>]         # Create worktree/branch
otto worktree list [--repo <repo>]               # List worktrees/branches
otto worktree remove <name> [--repo <repo>]      # Remove worktree/branch

otto server start                                # Start ottod daemon
otto server stop                                 # Stop daemon
otto server status                               # Show daemon status
otto server install                              # Install as systemd service

otto config set <key> <value>                    # Set configuration value
otto config show                                 # Show current merged config
```

### Parameter Inference

Otto makes CLI parameters optional wherever a sensible default can be inferred, reducing typing for common cases:

| Parameter | Required when | Inferred when |
|-----------|--------------|---------------|
| `--spec <slug>` | Multiple specs exist in `.otto/specs/` | Only one spec exists → use it automatically |
| `--id <taskid>` (task run) | Multiple runnable tasks | Only one pending task with satisfied deps → use it |
| `<id>` (pr fix/log/status) | Multiple tracked PRs | Only one tracked PR → use it |
| `--provider` (pr add) | Adding by numeric ID without config | `pr.default_provider` is set in config |

When inference is ambiguous (multiple candidates), otto prints the options and exits with an error rather than guessing:

```
$ otto spec design
Error: multiple specs found, specify one with --spec:
  add-jwt-authentication  (3 artifacts)
  rate-limiting           (1 artifact)
```

The `--spec` flag can also accept a unique prefix:

```
$ otto spec design --spec rate
# resolves to "rate-limiting" if unambiguous
```

### Example Workflows

```bash
# --- Spec workflow ---
$ otto spec add "Add rate limiting to the API gateway"
Created: .otto/specs/rate-limiting/
  requirements.md generated (reviewed by 3 models)

$ otto spec research --spec rate-limiting
Researching...
  pass 1/3: primary generating research.md
  pass 2/3: secondary reviewing (clean session)
  pass 3/3: primary incorporating feedback (clean session)
Written: .otto/specs/rate-limiting/research.md

$ otto spec design --spec rate-limiting
Designing... (incorporating requirements.md + research.md)
  pass 1/3: primary generating design.md
  pass 2/3: secondary reviewing (clean session)
  pass 3/3: primary incorporating feedback (clean session)
Written: .otto/specs/rate-limiting/design.md

$ otto spec task generate --spec rate-limiting
Generating tasks... (incorporating requirements.md + research.md + design.md)
  pass 1/3: primary generating tasks.md
  pass 2/3: secondary reviewing (clean session)
  pass 3/3: primary incorporating feedback (clean session)
Generated 6 tasks in 2 parallel groups
Written: .otto/specs/rate-limiting/tasks.md

$ otto spec execute --spec rate-limiting
Executing tasks...
  Phase 1:
    task-001   completed   1m 42s   Created rate limiter middleware
    task-002   completed   2m 10s   Added Redis token bucket store
  Phase 1 review:
    secondary: 1 issue (missing rate limit header in response)
    primary: incorporating feedback... done
  Phase 1 committed: f3a8b12

  Phase 2:
    task-003   running     0m 45s   ...
    task-004   pending     -        Waiting on task-003
  Progress: 2/6 completed | 1 running | 3 pending

# --- PR workflow ---
$ otto pr add https://dev.azure.com/org/project/_git/repo/pullrequest/12345
Detected backend: ADO
Tracking PR #12345 (feature/add-auth -> main)

$ otto pr list
  ID      PROVIDER   REPO          BRANCH              STATUS    PIPELINE
  12345   ado        org/my-repo   feature/add-auth    fixing    2/3 passing
  789     github     user/other    fix/timeout         watching  pending

$ otto pr status 12345
PR #12345: feature/add-auth -> main (ADO)
Status: fixing (attempt 2/5)
Pipeline: unit-tests FAILED, lint PASSED
Last fix: 2m ago - "Fix assertion in TestLoginHandler"
```

### CLI Framework

Built with [`github.com/spf13/cobra`](https://github.com/spf13/cobra) for command structure. Configuration is loaded directly from JSONC files (no viper — we read `.otto/otto.jsonc` and `~/.config/otto/otto.jsonc` and merge them).

---

## Server Component (`ottod`)

The daemon performs one continuous job:

1. **PR monitoring loop** — polls PR backends at `server.poll_interval`, fixes pipeline failures, and responds to reviewer comments

Spec execution (`otto spec execute`) runs **locally in the CLI process**, not in the daemon. The CLI spawns its own OpenCode server via `ServerManager.EnsureRunning()` if one isn't already available. This means:

- The daemon is only responsible for global, background tasks (PR monitoring).
- `otto spec execute` can run without the daemon running at all.
- The daemon and CLI may each have their own OpenCode server instance if both are active simultaneously — this is fine since each `ServerManager` independently checks health and starts a server only if needed.

### PR Monitoring Loop

The daemon monitors **all** globally tracked PRs, regardless of which repo they belong to.

```
every <poll_interval> (default: 2 minutes):
  for each tracked PR with status == "watching":
    1. Fetch pipeline status from the PR's backend
    2. If pipeline passed:
       - Update PR document status to "green"
       - Post success comment on PR
    3. If pipeline failed:
       - Update PR document status to "fixing"
       - Determine the PR's worktree directory (from repo config)
       - Fetch build logs for failed checks
       - Phase 1: Create OpenCode session (directory-scoped to PR worktree) to distill logs into failure summary
       - Phase 2: Create fresh OpenCode session (same worktree) with failure summary to fix code
       - Wait for LLM completion (applies multi-model review)
       - Commit and push fix from the worktree
       - Update PR document with fix attempt
       - If fix_attempts >= max_fix_attempts:
         - Status → "failed", post comment: "otto: giving up after N attempts"
       - Else:
         - Status → "watching" (wait for next pipeline run)
    4. If backend is ADO and merlinbot is enabled:
       - Check for new MerlinBot comments
       - Address them via OpenCode session
    5. Check for new reviewer comments (all backends):
       - Fetch comments via GetComments()
       - Diff against previously-seen comment IDs (from PR document frontmatter)
       - For each new, unresolved comment:
         a. Create OpenCode session (directory-scoped to PR worktree)
         b. Send pr-comment-respond prompt with comment context
         c. LLM decides: AGREE → fix + reply + resolve,
            BY_DESIGN → reply + resolve, WONT_FIX → reply + resolve
         d. If AGREE: commit and push the fix from the worktree
         e. Reply via ReplyToComment(), resolve via ResolveComment()
         f. Update PR document with comment history entry
       - Update seen-comment IDs in PR document frontmatter
```

### Server API (for CLI communication)

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/status` | GET | Server health and stats |
| `/prs` | GET | List tracked PRs |
| `/prs` | POST | Add a PR to track |
| `/prs/:id` | DELETE | Remove a tracked PR |
| `/prs/:id/fix` | POST | Manually trigger fix |
| `/events` | GET | SSE stream of otto events |

All PR CLI commands (`otto pr list`, `otto pr add`, `otto pr status`, `otto pr remove`, `otto pr fix`) communicate directly with the daemon via these endpoints. If the daemon is not running, these commands fail with a clear error:

```
$ otto pr list
Error: cannot reach otto daemon at localhost:4097. Start it with: otto server start
```

### Process Management

- **`otto server start`** launches the daemon as a background process that persists after the shell exits:
  - Uses `setsid` (Linux) or equivalent to detach from the controlling terminal
  - stdout/stderr redirected to `~/.local/share/otto/logs/ottod.log`
  - The CLI process writes a PID file and returns immediately — the user gets their shell back
  - The daemon survives shell exit, logout, and terminal close (no `nohup` needed — `setsid` creates a new session)
  - If the daemon is already running (PID file exists, process alive), `otto server start` prints the status and exits
- **`otto server start --foreground`** runs the daemon in the foreground (for systemd, debugging, or container use)
- PID file at `~/.local/share/otto/ottod.pid`
- Logs to `~/.local/share/otto/logs/`
- Graceful shutdown on SIGTERM/SIGINT
- `otto server install` generates and enables a systemd user unit file:
  ```ini
  [Unit]
  Description=Otto Daemon
  After=network.target

  [Service]
  ExecStart=/usr/local/bin/otto server start --foreground
  Restart=on-failure
  User=%u

  [Install]
  WantedBy=default.target
  ```

---

## Data Storage

All state is file-based. No databases.

### Directory Layout

```
~/.config/otto/
  otto.jsonc                     # User-level configuration
  prompts/                       # User prompt overrides (optional)
    requirements.md              #   Override built-in requirements prompt
    research.md                  #   Override built-in research prompt
    design.md                    #   Override built-in design prompt
    tasks.md                     #   Override built-in task generation prompt
    ...                          #   Any prompt template can be overridden

~/.local/share/otto/
  ottod.pid                      # Daemon PID file
  logs/                          # Daemon logs
    otto-2026-02-06.log
  prs/
    <provider>-<id>.md           # PR tracking documents (global)

<repo>/.otto/
  otto.jsonc                     # Repo-specific config overrides
  specs/
    <slug>/
      requirements.md
      research.md
      design.md
      tasks.md
      questions.md
      history/
        run-001.md
        run-002.md
```

### Markdown Processing

Otto reads and writes markdown with YAML frontmatter. Key libraries:

| Library | Purpose |
|---------|---------|
| `github.com/adrg/frontmatter` | Parse YAML frontmatter from markdown |
| `gopkg.in/yaml.v3` | YAML serialization for frontmatter |
| `github.com/tidwall/jsonc` | JSONC parsing for config files |

---

## Go Module Structure

```
otto/
  cmd/
    otto/
      main.go                           # CLI entrypoint (single binary)
  internal/
    cli/                                 # Cobra command definitions
      root.go
      spec.go                           # otto spec *
      spec_task.go                      # otto spec task *
      pr.go                             # otto pr * (add, list, status, remove, fix, log)
      pr_review.go                      # otto pr review (third-party PR review)
      repo.go                           # otto repo *
      worktree.go                       # otto worktree *
      server.go                         # otto server *
      config.go                         # otto config *
    config/                              # Configuration loading/merging
      config.go                         # Load .otto/otto.jsonc + ~/.config/otto/otto.jsonc
      types.go                          # Config struct definitions
    server/                              # ottod daemon
      server.go                         # HTTP server + lifecycle
      pr_loop.go                        # PR monitoring loop (pipeline + comments)
      comments.go                       # Reviewer comment monitoring and response
      api.go                            # REST API handlers
      daemon.go                         # Background process launch (setsid, PID file)
    opencode/                            # OpenCode integration layer
      client.go                         # ServerManager: start/stop/health-check opencode serve
      session.go                        # Session helpers (create, prompt, wait) with directory scoping
      review.go                         # Multi-model review pipeline
    provider/                            # PR backend interface + implementations
      provider.go                       # PRBackend interface (inline comments, resolve, no diff fetching)
      registry.go                       # Backend registry and URL matching
      ado/
        ado.go                          # ADO backend implementation
        types.go
        workflow.go                     # ADO-specific workflow (MerlinBot, etc.)
      github/
        github.go                       # GitHub backend implementation
        types.go
    spec/                                # Specification system
      spec.go                           # Core spec types and operations
      requirements.go                   # Requirements generation/refinement
      research.go                       # Research generation
      design.go                         # Design generation
      tasks.go                          # Task generation/management
      questions.go                      # Questions Q&A system
      execute.go                        # Task execution engine (the outer loop)
      runner.go                         # Individual task runner
    prompts/                             # LLM prompt templates (embedded via go:embed)
      loader.go                         # Template loading with user override
      requirements.md                   # Requirements refinement prompt
      research.md                       # Technical research prompt
      design.md                         # Design generation prompt
      tasks.md                          # Task generation prompt
      review.md                         # Multi-model critical review prompt
      task-briefing.md                   # Pre-execution task context distillation
      phase-review.md                   # Inter-phase review gate prompt
      external-assumptions.md           # External assumption validation & repair (per-phase)
      domain-hardening.md               # Domain hardening & polishing (per-phase)
      documentation-alignment.md        # Documentation alignment (end of execution)
      question-harvest.md               # Question extraction from execution logs
      question-resolve.md               # Auto-resolution of open questions
      pr-review.md                      # Third-party PR review (inline comments)
      pr-comment-respond.md             # Evaluate and respond to reviewer comments
    store/                               # File-based state management
      store.go                          # Generic markdown document read/write
      frontmatter.go                    # YAML frontmatter helpers
      flock.go                          # File locking for concurrent access
    repo/                                # Repo management
      repo.go
      worktree.go                       # Worktree/branch creation, listing, removal
      strategy.go                       # Git strategy implementations (worktree, branch, hands-off)
      mapping.go                        # PR-to-worktree mapping logic
  docs/
    design.md                            # This document
    requirements.md                      # Requirements document
  go.mod
  go.sum
```

---

## Key Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/sst/opencode-sdk-go` | OpenCode Go SDK (sessions, prompts, events) |
| `github.com/adrg/frontmatter` | YAML frontmatter parsing |
| `gopkg.in/yaml.v3` | YAML serialization |
| `github.com/tidwall/jsonc` | JSONC config file parsing |
| `github.com/tidwall/sjson` | JSON path manipulation |
| `github.com/charmbracelet/log` | Structured logging |
| `github.com/charmbracelet/lipgloss` | CLI output styling |
| `github.com/charmbracelet/huh` | Interactive prompts (PR review comment approval) |
| `github.com/google/go-github/v82` | GitHub REST API client |
| `github.com/shurcooL/githubv4` | GitHub GraphQL API client |
| `github.com/gofri/go-github-ratelimit/v2` | GitHub API rate-limit handling |
| `github.com/gofrs/flock` | File locking for concurrent access |
| `dario.cat/mergo` | Deep merge for config layering |
| `golang.org/x/oauth2` | OAuth2 token transport |
| `github.com/stretchr/testify` | Test assertions and mocks |

---

## Resolved Design Decisions

1. **OpenCode lifecycle** — Otto manages a single `opencode serve` process, started via `os/exec` from the primary repository root. It checks health at `GET /global/health` before each operation. The child process is tracked by PID and cleaned up (SIGTERM + wait) on otto exit. The server is started lazily on first LLM need, not eagerly at otto startup.

2. **Git operations** — Shell out to `git` via `os/exec`. This is simpler and more predictable than `go-git`, and matches what OpenCode itself does internally.

3. **Concurrency model** — Multiple PR fixes and spec tasks run in parallel, each in its own OpenCode session. Bounded by `spec.max_parallel_tasks` (default 4). File access to shared state (tasks.md, PR docs) is serialized via `flock`.

4. **Config format** — JSONC (not YAML). This matches OpenCode's own config format and supports comments for user documentation.

5. **Single binary** — `otto` is one binary. The daemon mode is `otto server start` (which forks itself) or `otto server start --foreground` for systemd. No separate `ottod` binary.

6. **PR backend detection** — URL pattern matching for automatic backend detection, with `--provider` flag for override and `pr.default_provider` for ID-only additions.

7. **ADO API approach** — Raw HTTP with typed Go structs (not `azure-devops-go-api`). The `az devops` CLI can be used for some queries but not all — some operations will require direct REST calls. Research needed to determine the exact split.  You may be able to determine the nature of some queries by inspecting az devops calls.  The az CLI also has certain authentication adantages on WSL + Windows environments for enrolled devices for Microsoft employees. 

8. **PR fix prompt engineering** — Two-phase approach: first LLM pass evaluates/distills logs (they're too large to feed directly), second LLM pass performs the actual repair in a clean session.

9. **OpenCode server lifecycle** — Otto owns and manages a single OpenCode server process. It starts `opencode serve` automatically, health-checks it via `/global/health`, and shuts it down on exit. Not externally managed. The `directory` query parameter on every SDK call allows this single server to operate across multiple worktrees simultaneously — no per-worktree server processes are needed.

10. **Concurrency** — Yes, concurrent PR fixes and spec task execution, each in its own OpenCode session and repo worktree. Bounded by configurable limits.

11. **Idempotency as a core principle** — Every otto command is safe to re-run. Spec artifact commands refine existing content (not blindly overwrite). `otto spec execute` resumes from the first incomplete phase. Status is persisted per-task in tasks.md, and each completed phase is committed, so re-running after a crash or abort picks up exactly where it left off.

12. **LLM-first orchestration** — Otto delegates all reasoning, evaluation, and content generation to LLMs. Go code handles plumbing (file I/O, concurrency, state, process lifecycle, git, API calls). Rigid code is used only where determinism and reliability are required (the execution loop, locking, status tracking, health checks). When in doubt, farm it to the LLM.

13. **Enforced pipeline flow** — Spec commands follow a strict serial progression: requirements → research → design → task generate → execute. Each command checks that its prerequisite artifacts exist. This is rigid Go code (file existence checks), not an LLM decision. Questions are captured as advisory context throughout the pipeline but never gate progression — they are included in LLM prompts so the model is aware of open uncertainties, and the user can address them at any time via `otto spec questions`. `otto spec run` is exempt from pipeline enforcement as an ad-hoc escape hatch.

14. **Prompts as Go templates** — All LLM prompts are stored as markdown files in `internal/prompts/`, embedded into the binary via `go:embed`, and processed with Go's standard `text/template` engine. Users can override any prompt by placing a file at `~/.config/otto/prompts/<name>.md`. Templates use `{{.variable}}` for substitution and `{{if .variable}}` for conditionals — the standard Go template syntax. No custom parser, no third-party template library. This keeps prompts readable, version-controlled, and tunable without recompilation.

15. **Single server, directory-scoped operations** — The OpenCode Go SDK provides a `directory` query parameter on virtually every API method (`Session.New`, `Session.Prompt`, `File.Read`, `Find.Files`, `Event.ListStreaming`, etc.). This means one `opencode serve` process can handle operations across many worktrees simultaneously by passing the target directory on each call. Otto does not need to spawn, pool, or route between multiple OpenCode server processes. Sessions are directory-bound: a session created with `Directory: "/path/to/worktree"` operates exclusively in that directory's file and git context. This was confirmed by examining the SDK's `SessionNewParams`, `SessionPromptParams`, `FindFilesParams`, `FileReadParams`, `PathGetParams`, etc., all of which include `Directory param.Field[string] \`query:"directory"\``.

16. **PR review uses local checkout, not diff injection** — `otto pr review <url>` maps the PR to a local repo, fetches and checks out the source branch in a worktree, and creates an OpenCode session scoped to that worktree. The LLM reviews the code by running `git diff origin/<target>...HEAD` and reading full files — giving it complete context (surrounding code, type definitions, imports) rather than a stripped-down diff. The command is fire-and-forget: the PR is never added to the global tracking store or the daemon's monitoring loop. A temporary worktree is cleaned up after the review.

17. **Automated comment resolution on tracked PRs** — When monitoring a tracked PR, otto checks for new reviewer comments on each poll cycle. Each comment is evaluated by an LLM which decides: AGREE (fix the code, reply, resolve), BY_DESIGN (reply with rationale, resolve), or WONT_FIX (reply with explanation, resolve). Comments are processed sequentially within a poll cycle, each in a fresh OpenCode session. The PR document tracks seen comment IDs in frontmatter to avoid re-processing.

18. **LLM JSON retry via session resume** — When otto expects structured JSON output from the LLM (PR review comments, comment response decisions) and the response is malformed (markdown-wrapped, preamble text, invalid JSON), otto does not discard the session. Instead, it resumes the same session with a follow-up prompt: "Your previous response was not valid JSON. Please return only the JSON array/object as specified, with no other text." This preserves the LLM's prior reasoning context and avoids re-running the entire analysis. Up to 2 retry attempts are made before falling back to a best-effort JSON extraction (strip markdown fences, trim preamble). If extraction also fails, the operation is logged and skipped — never crashes the outer loop.

19. **Questions are advisory, never blocking** — Questions discovered during any pipeline stage (requirements, research, design, task execution) are captured in questions.md but never halt progression. They are included as context in subsequent LLM prompts so the model is aware of open uncertainties. The user can address them at any time via `otto spec questions`, which attempts auto-resolution before presenting remaining questions interactively. This prevents the autonomous execution engine from stalling on questions that may not even be relevant to the current phase.

20. **Worktree management with pluggable git strategies** — Otto supports three git strategies per repo: `worktree` (git worktrees for isolation), `branch` (branches in primary directory), and `hands-off` (otto never creates/switches branches, only commits and diffs). Each repo's config defines `git_strategy`, `primary_dir`, `worktree_dir` (worktree strategy only), and `branch_template` (Go `text/template` for naming). `otto worktree add/list/remove` commands manage worktrees and branches according to the strategy. This ensures otto works for users who prefer worktrees, users with a single clone using branches, and users who want full manual git control.

21. **CLI-daemon boundary** — All PR commands (`pr list`, `pr add`, `pr status`, `pr remove`, `pr fix`, `pr log`) communicate directly with the daemon via its HTTP API. If the daemon is not running, these commands fail. `spec execute` runs locally in the CLI process, not in the daemon — it spawns its own OpenCode server if needed. The daemon is only responsible for background tasks: PR monitoring, pipeline fix triggering, and comment response.

22. **Session cleanup after log collection** — OpenCode sessions are deleted after their dialog logs have been collected and persisted. Every workflow that creates a session (task execution, phase review, PR fix, comment response, PR review, question resolution) deletes the session after extracting the information it needs. The exception is `otto spec run` (ad-hoc), where the session is kept for potential user continuation. This prevents session accumulation on the OpenCode server.

23. **OpenCode shutdown guard** — Otto only shuts down the OpenCode server process if otto started it. If the server was already running when otto connected (health check succeeded before `EnsureRunning` launched a process), otto leaves it alone on exit. The `ServerManager.cmd` field being nil indicates an externally managed server.

24. **Parallel task acceptance in shared worktrees** — Multiple tasks in the same phase may run concurrently in the same worktree. File conflicts, stale reads, or merge issues are tolerated — the system is eventually consistent. Conflicts are caught by the phase review gate or resolved by task retries. This is a pragmatic tradeoff: per-task worktree isolation would add enormous complexity for marginal benefit.

25. **Task retry until success** — Failed tasks are retried up to `spec.max_task_retries` (default 15). Each retry creates a fresh OpenCode session with the task description plus the error/output from the previous attempt, giving the LLM context to correct the failure. Tasks should never permanently block progress. Only after exhausting all retries is a task marked `failed`, and even then the outer loop continues to subsequent phases if possible.

26. **Spec execute runs in CLI process** — `otto spec execute` runs locally in the CLI process, not in the daemon. The CLI spawns its own OpenCode server via `ServerManager.EnsureRunning()`. The daemon and CLI may each have independent OpenCode server instances simultaneously. This keeps the daemon simple (only PR monitoring) and allows spec execution without a running daemon.

27. **PR-to-worktree mapping via repo config** — Given a PR's repo URL and branch name, otto maps to a local working directory using the repo's `git_strategy`: worktree strategy derives the worktree name from `branch_template` and locates/creates it under `worktree_dir`; branch strategy uses `primary_dir` with a branch checkout; hands-off strategy uses `primary_dir` and verifies the branch matches. The mapping is deterministic and reversible.

28. **Config deep merge** — Repo-level config (`.otto/otto.jsonc`) is deep-merged into user-level config (`~/.config/otto/otto.jsonc`). Nested objects are merged recursively — overriding a single key in `models` does not replace the entire `models` object. Arrays are replaced (not merged). This gives repos fine-grained override control without duplicating the full config.

29. **Server daemonization** — `otto server start` launches the daemon as a fully detached background process using `setsid` (Linux). The CLI writes a PID file and returns immediately. The daemon persists after shell exit, logout, and terminal close. stdout/stderr are redirected to log files. `otto server start --foreground` is available for systemd and debugging. No `nohup` wrapper needed.

30. **Dirty worktree handling** — Otto treats dirty worktrees/branches as its own responsibility. Uncommitted changes from previous sessions, manual edits, or interrupted executions are not errors — the LLM sees them as part of the working context and can fix, incorporate, or discard them. At phase completion, all changes (including pre-existing dirty state) are committed together. The retry loop handles any failures caused by dirty state.

31. **Task briefing for context distillation** — Before executing each task, otto runs a preparatory LLM call (the "briefing" step) that reads all spec artifacts (requirements.md, research.md, design.md, tasks.md, phase summaries) and produces a focused implementation brief specific to that task. This replaces the naive approach of dumping every spec document verbatim into every task prompt. Benefits: (a) the executor receives a higher signal-to-noise prompt, improving quality especially with less capable models; (b) the briefing is an inspectable artifact for debugging; (c) it externalizes the context-distillation reasoning that intelligent models do internally, making the pipeline more robust and model-independent. The briefed prompt includes reference pointers to the raw spec files (`requirements.md`, `design.md`, etc.) so the executor can explore further context if the brief is insufficient. Enabled by default (`spec.task_briefing: true`). Falls back gracefully to the static context dump if the briefing call fails (LLM error, empty response, timeout). Configurable via `spec.task_briefing: false` in `otto.jsonc` to disable.

## Open Questions

1. **PR fix prompt engineering** — How much build log context to feed the log-analysis LLM (full log? truncated? just error lines?). Likely needs truncation with error-focused extraction. This will require iteration.

2. **Spec artifact format stability** — The tasks.md format (YAML frontmatter vs inline metadata) needs validation through real usage. The current inline approach is simpler to parse but less structured.

3. **Notification / alerting** — User wants Teams notifications (direct message, not group) when PRs go green, otto gives up, or spec execution completes. Research needed: Teams incoming webhooks, Graph API personal chat, or Power Automate integration. Authentication and token requirements are unclear — this needs a research spike.

## Implementation Notes

Key learnings and patterns established during implementation:

- **File locking** — Concurrent access to shared state files (tasks.md, PR docs) uses `gofrs/flock` via `store.WithLock`. Advisory locks acquired with a 30-second timeout prevent data races between parallel tasks and the daemon.
- **Atomic file writes** — All store writes use temp-file-plus-rename to prevent corruption on crash or concurrent read. The temp file is written in the same directory, then atomically renamed over the target.
- **PR monitoring architecture** — PR monitoring uses a poll-based loop (`server/pr_loop.go`), not webhooks. Each cycle fetches PR status and comments via the provider API. This avoids inbound networking requirements and works behind firewalls.
- **Notifications** — Teams/chat notifications use Power Automate webhook integration with Adaptive Card payloads (`server/notify.go`). No direct Graph API or Teams SDK dependency.
- **Session cleanup** — OpenCode sessions are deleted via deferred deletes after log collection to prevent session accumulation on the server. Every workflow path that creates a session is responsible for cleaning it up.
- **Git command timeouts** — All `git` shell-outs use `exec.CommandContext` with context-based timeouts (10–30 seconds depending on operation) to prevent hangs on network issues or lock contention.
