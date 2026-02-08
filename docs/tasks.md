# Otto — Implementation Tasks

Ordered, phased implementation plan derived from `design.md` and `research.md`.

Tasks are organized into phases that build on each other. Within each phase, tasks are listed in dependency order. Tasks marked with `[parallel]` can be worked concurrently within their phase.

---

## Executor Instructions

This file is designed to be consumed by an LLM agent that implements otto task-by-task. Follow these rules precisely:

### Processing Model

1. **Work one task at a time.** Read the task, implement it fully, verify it, mark it `completed`, then move to the next.
2. **Process tasks in phase order.** Complete all tasks in Phase 0 before starting Phase 1, etc. Within a phase, follow the listed order unless a task is marked `[parallel]` — those may be done in any order within the phase.
3. **Before starting each task:** read `docs/design.md` and `docs/research.md` for context. Tasks reference these documents — they are your primary specifications. When a task says "matching design.md", consult the actual document.
4. **Before starting each task:** mark its status as `status: in-progress` in this file.
5. **After completing each task:** run all tests (`go test ./...`) and ensure they pass. If the task introduced tests, they must pass. If existing tests break, fix the breakage before marking complete.
6. **After verification:** mark the task `status: completed` and commit the changes with message format: `otto: <task-id> — <brief description>`.
7. **On failure:** if a task cannot be completed after reasonable effort, mark it `status: failed` with a note explaining what went wrong. Continue to the next task unless it depends on the failed one.
8. **Never skip ahead.** Do not implement Phase N+1 tasks while Phase N has incomplete tasks, unless the incomplete tasks are explicitly `[parallel]` and non-blocking.

### Reference Documents

These files are your specifications — consult them whenever the task description references them or when you need design context:

- **`docs/design.md`** (~1861 lines) — Complete design specification. Contains: configuration schema (line 262), OpenCode integration architecture (line 348), multi-model review pipeline (line 69), spec system (line 695), task execution engine (line 866), PR backend interface (line 977), CLI design (line 1405), server/daemon (line 1572), data storage (line 1652), Go module structure (line 1695), dependency list (line 1775).
- **`docs/research.md`** (~750 lines) — Deep research on implementation-critical topics: OpenCode SDK API surface, ADO REST API, GitHub PR API & go-github, Go daemonization, flock, JSONC deep merge, Teams notifications, Charmbracelet libraries, MerlinBot/build logs.
- **`internal/prompts/*.md`** (10 files) — Seed prompt templates already written. These are substantive, production-quality prompts, not stubs. Tasks that reference prompt templates should refine (not rewrite) these existing files.

### Acceptance Criteria

Every task has an implicit acceptance criterion: **`go build ./...` and `go test ./...` must pass after the task is complete.** Tasks with explicit test requirements must have those tests written and passing. Tasks that produce CLI commands must be reachable via `otto --help` traversal.

### Status Tracking

Each task has a `status` field. Valid values:
- `pending` — not yet started
- `in-progress` — currently being worked on
- `completed` — done and verified
- `failed` — attempted but could not be completed
- `skipped` — intentionally skipped (e.g., moved/merged into another task)

---

## Phase 0: Project Scaffolding & Foundation

Establish the Go module, directory structure, build tooling, and core types that everything else depends on.

### 0.1 — Initialize Go module and directory structure
- **status**: pending
- **done-when**: `go build ./cmd/otto` compiles and `./otto --help` prints usage

- `go mod init github.com/alanmeadows/otto`
- Create the full directory tree (from design.md line 1695):
  ```
  cmd/otto/main.go
  internal/cli/          (root.go, spec.go, spec_task.go, pr.go, pr_review.go, repo.go, worktree.go, server.go, config.go)
  internal/config/       (config.go, types.go)
  internal/server/       (server.go, pr_loop.go, comments.go, api.go, daemon.go)
  internal/opencode/     (client.go, session.go, review.go, json.go, permissions.go)
  internal/provider/     (provider.go, registry.go)
  internal/provider/ado/ (ado.go, types.go, workflow.go, auth.go)
  internal/provider/github/ (github.go, types.go)
  internal/spec/         (spec.go, requirements.go, research.go, design.go, tasks.go, questions.go, execute.go, runner.go, analysis.go)
  internal/prompts/      (loader.go + 10 .md template files — already exist as seed prompts)
  internal/store/        (store.go, frontmatter.go, flock.go)
  internal/repo/         (repo.go, worktree.go, strategy.go, mapping.go)
  ```
- Add `.gitignore` for Go projects
- Stub `main.go` with a basic cobra root command that prints version

### 0.2 — Add core dependencies
- **status**: pending
- **done-when**: `go mod tidy` exits cleanly with no errors

- `go get github.com/spf13/cobra`
- `go get github.com/sst/opencode-sdk-go`
- `go get github.com/tidwall/jsonc`
- `go get github.com/imdario/mergo`
- `go get github.com/gofrs/flock`
- `go get github.com/adrg/frontmatter`
- `go get gopkg.in/yaml.v3`
- `go get github.com/yuin/goldmark`
- `go get github.com/charmbracelet/log`
- `go get github.com/charmbracelet/lipgloss`
- `go get github.com/charmbracelet/huh`
- `go get github.com/charmbracelet/x/ansi`
- `go get github.com/google/go-github/v82`
- `go get github.com/shurcooL/githubv4`
- `go get github.com/gofri/go-github-ratelimit`
- Verify `go mod tidy` succeeds
- **Pin `github.com/charmbracelet/lipgloss` to v1.x. Do not upgrade to v2 (beta, breaking changes to color types and API).**

### 0.3 — Define configuration types and schema
- **status**: pending

- `internal/config/types.go`: Define the Go struct hierarchy matching the JSONC schema in design.md (line 262–340). The structs are: `Config` (top-level), `ModelsConfig` (primary/secondary/tertiary strings), `OpenCodeConfig` (url, auto_start, password, permissions), `PRConfig` (default_provider, max_fix_attempts, providers map), `ADOConfig` (organization, project, pat, auto_complete, merlinbot, create_work_item), `GitHubConfig` (token), `RepoConfig` (name, primary_dir, worktree_dir, git_strategy, branch_template, branch_patterns), `ServerConfig` (poll_interval, port, log_dir), `SpecConfig` (max_parallel_tasks, task_timeout, max_task_retries), `NotificationsConfig` (teams_webhook_url, events)
- `NotificationsConfig` struct: `TeamsWebhookURL string`, `Events []string` — define now even though implementation is Phase 9, to avoid retrofitting the struct hierarchy later
- Include `git_strategy` enum type (`worktree`, `branch`, `hands-off`)
- Include `branch_template` as string (parsed via `text/template` at runtime)
- Include JSON struct tags for unmarshaling

### 0.4 — Implement JSONC config loading and deep merge
- **status**: pending

- `internal/config/config.go`:
  - `Load()` function: reads `~/.config/otto/otto.jsonc` (user) and `.otto/otto.jsonc` (repo, if present)
  - **Detect repo root via `git rev-parse --show-toplevel`. If not in a git repo, skip repo-level config.**
  - Uses `tidwall/jsonc` to strip comments → `encoding/json` to unmarshal into `map[string]any`
  - Deep-merges repo over user config using `imdario/mergo` with `WithOverride`
  - Unmarshals merged map into the typed `Config` struct
  - Falls back to defaults for missing values
- Environment variable overrides: `OTTO_ADO_PAT`, `GITHUB_TOKEN`, `OPENCODE_SERVER_PASSWORD`, `OPENCODE_SERVER_USERNAME`
- Unit tests for: deep merge of nested objects, array replacement, env var overrides, missing files, malformed JSONC

### 0.5 — Set up logging with slog + charmbracelet/log
- **status**: pending

- `internal/cli/root.go` or a dedicated `internal/logging/` package:
  - Initialize `charmbracelet/log` as the `slog.Handler` backend
  - TTY detection: use `TextFormatter` (colored) for interactive terminals, `JSONFormatter` for file/piped output
  - Support `--verbose` / `-v` flag on root command to set `DebugLevel`
  - Use `slog.Info/Debug/Warn/Error` throughout the codebase (never direct `charmbracelet/log` calls outside init)

### 0.6 — Implement file-based store utilities
- **status**: pending

- `internal/store/store.go`: Generic read/write for markdown files with YAML frontmatter
  - `ReadDocument(path) -> (frontmatter map, body string, error)`
  - `WriteDocument(path, frontmatter, body) -> error`
  - Uses `github.com/adrg/frontmatter` for parsing
  - Uses `gopkg.in/yaml.v3` for serializing frontmatter
- `internal/store/flock.go`: File locking wrapper around `gofrs/flock`
  - `WithLock(path, timeout, fn) -> error` — acquires exclusive lock on `<path>.lock`, calls fn, releases
  - Uses `TryLockContext` with configurable timeout (default 5s)
- Unit tests for: round-trip read/write, concurrent locking, timeout behavior

### 0.7 — Embed prompt templates
- **status**: pending

- `internal/prompts/loader.go`:
  - `//go:embed *.md` to embed all prompt templates
  - `Load(name) -> (*template.Template, error)`: checks `~/.config/otto/prompts/<name>.md` first, falls back to embedded
  - `Execute(name, data map[string]string) -> (string, error)`: loads and executes template with data map
- **Seed prompt files already exist** in `internal/prompts/` — 10 substantive templates totalling ~1000 lines. Do NOT overwrite them. Implement the loader to embed and serve these existing files.
- Template variable names are listed in design.md line 93 (Template Variables table). Ensure the loader's `Execute` function accepts all variables from that table.
- Unit tests for: embedded loading, user override resolution, template variable substitution

### 0.8 — Implement OpenCode permission config writer [parallel]
- **status**: pending

- `internal/opencode/permissions.go`:
  - `EnsurePermissions(directory string) -> error`:
    - Write `opencode.json` to `directory` with permission config from otto's config
    - Default: `{"permission": {"edit": "allow", "bash": "allow", "webfetch": "allow", "doom_loop": "allow", "external_directory": "allow"}}`
    - **Explicitly set `doom_loop` and `external_directory` to `allow`** — defaulting to `ask` will block automated runs
    - Consider making the permission config configurable via otto's config to allow per-repo overrides
    - Idempotent: overwrite if file exists
  - Called before any LLM operation in a worktree directory
- Note: this is a simple file write utility with no OpenCode SDK dependency; placed in Phase 0 because it is a foundation utility like store (0.6)

---

## Phase 1: OpenCode Integration Layer

The foundation for all LLM interactions. Nothing that calls LLMs can work without this.

### 1.0 — Define OpenCode mock strategy [parallel]
- **status**: pending

- Create an `opencode.ClientInterface` wrapper or use `httptest.Server` to mock the OpenCode HTTP API
- Provide canned responses for: session creation, prompting, message retrieval, event streaming, session deletion
- All Phase 1 unit tests use mocks — no tests should require a running OpenCode server or LLM
- This establishes the testing pattern for all downstream LLM-dependent code

### 1.1 — Implement OpenCode health check (raw HTTP)
- **status**: pending

- `internal/opencode/client.go`:
  - `HealthCheck(ctx, baseURL) -> (healthy bool, version string, error)`: raw `GET /global/health` call (not via SDK — health is not in the SDK per research.md)
  - Handle basic auth if `OPENCODE_SERVER_PASSWORD` is set (manually add `Authorization` header)
  - Timeout: 5s connect, 10s total

### 1.2 — Implement ServerManager lifecycle
- **status**: pending

- `internal/opencode/client.go`:
  - `ServerManager` struct: `cmd *exec.Cmd`, `client *opencode.Client`, `baseURL string`, `ownsProcess bool`
  - `EnsureRunning(ctx) -> error`:
    1. Call `HealthCheck` — if healthy, set `ownsProcess = false`, create SDK client, return
    2. **Check `config.opencode.auto_start`. If false, return a clear error: "OpenCode server is not reachable at <url> and auto_start is disabled. Start it manually with: opencode serve".** Only proceed to step 3 if `auto_start` is true.
    3. Start `opencode serve` via `os/exec.Command` from repo root
    4. Set env: `OPENCODE_SERVER_PASSWORD` if configured
    5. Poll `HealthCheck` with backoff (100ms, 200ms, 400ms, ...) up to 30s
    6. Create SDK client once healthy
    7. **When creating SDK client, configure basic auth via `option.WithHeader("Authorization", basicAuthValue)` if `OPENCODE_SERVER_PASSWORD` is set. Use `OPENCODE_SERVER_USERNAME` (default `opencode`) for the username portion of the basic auth header.**
    8. Set `ownsProcess = true`
  - `Shutdown() -> error`: only `SIGTERM` + `Wait()` if `ownsProcess == true`
  - `Client() -> *opencode.Client`: accessor
- Unit tests (using mocks from 1.0): mock health endpoint, test start logic, test shutdown guard, test auth header construction

### 1.3 — Implement session helpers with directory scoping
- **status**: pending

- `internal/opencode/session.go`:
  - `CreateSession(ctx, client, title, directory) -> (sessionID, error)`: wraps `client.Session.New()` with `Directory: opencode.F(directory)`
  - `SendPrompt(ctx, client, sessionID, prompt, model, directory) -> (response, error)`: wraps `client.Session.Prompt()`
  - `GetMessages(ctx, client, sessionID, directory) -> ([]Message, error)`: wraps `client.Session.Messages()`
  - `DeleteSession(ctx, client, sessionID, directory) -> error`: wraps `client.Session.Delete()`
  - `AbortSession(ctx, client, sessionID, directory) -> error`: wraps `client.Session.Abort()`
  - **Event streaming: use `Event.ListStreaming()` returning `*ssestream.Stream[EventListResponse]`, not `Event.List()` — SDK method names differ from what might be expected**
  - Model ref type: `ModelRef { ProviderID, ModelID string }` parsed from config's `provider/model` format (e.g., `"anthropic/claude-sonnet-4-20250514"` → `ProviderID: "anthropic"`, `ModelID: "claude-sonnet-4-20250514"`)

### 1.4 — Implement LLM JSON response parsing with retry
- **status**: pending

- `internal/opencode/json.go`:
  - `ParseJSONResponse[T any](ctx, client, sessionID, directory, rawResponse) -> (T, error)`:
    1. Try `json.Unmarshal` directly
    2. If fails: strip markdown fences (` ```json ... ``` `), trim preamble, retry unmarshal
    3. If still fails: send follow-up prompt in **same session**: "Your previous response was not valid JSON. Please return only the JSON as specified."
    4. Up to 2 retry prompts. If all fail, return best-effort extraction error.
  - Unit tests for: clean JSON, markdown-wrapped, preamble text, multiple retries

### 1.5 — Implement multi-model review pipeline
- **status**: pending

- `internal/opencode/review.go`:
  - `ReviewPipeline` struct: `client`, `serverMgr`, `primary`, `secondary`, `tertiary *ModelRef`
  - `Review(ctx, directory, prompt, contextData) -> (finalArtifact string, error)`:
    1. Pass 1: primary generates in a fresh session → capture output → delete session
    2. Pass 2: secondary critiques in a fresh session (receives artifact + review.md prompt) → capture critique → delete session
    3. Pass 3: (if tertiary configured) tertiary critiques in fresh session → capture → delete
    4. Pass 4: primary incorporates all feedback in fresh session → final artifact → delete session
  - `maxCycles` config (default 1, max 2): **iteration heuristic — always run exactly `maxCycles` iterations. If iteration support beyond 1 is needed later, use a simple metric: if the Levenshtein distance between pass 1 and pass 4 output exceeds 20% of artifact length, iterate. Alternatively, delegate to LLM: "Did the review feedback result in material changes? Reply YES/NO."**
  - Each session is directory-scoped and cleaned up after use
  - **Unit tests (using mocks from 1.0): mock primary/secondary/tertiary sessions. Verify 4-pass flow. Verify session cleanup (all sessions deleted). Verify iteration control (`maxCycles`). Verify graceful degradation when tertiary is nil.**

---

## Phase 2: CLI Skeleton & Repo Management

Build out the cobra command tree and repo/worktree management — everything needed before spec or PR commands work.

### 2.1 — Build cobra command tree (stubs) [parallel]
- **status**: pending
- **done-when**: `otto --help` shows all command groups; `otto spec --help`, `otto pr --help`, `otto server --help` each show subcommands

- `internal/cli/root.go`: Root command with `--verbose`, `--config` flags, config loading in `PersistentPreRun`
- `internal/cli/spec.go`: `otto spec` parent + subcommands: `add`, `list`, `requirements`, `research`, `design`, `execute`, `questions`, `run`
- `internal/cli/spec_task.go`: `otto spec task` parent + subcommands: `generate`, `list`, `add`, `run`
- `internal/cli/pr.go`: `otto pr` parent + subcommands: `add`, `list`, `status`, `remove`, `fix`, `log`
- `internal/cli/pr_review.go`: `otto pr review <url>`
- `internal/cli/repo.go`: `otto repo` parent + subcommands: `add`, `remove`, `list`
- `internal/cli/worktree.go`: `otto worktree` parent + subcommands: `add`, `list`, `remove`
- `internal/cli/server.go`: `otto server` parent + subcommands: `start`, `stop`, `status`, `install`
- `internal/cli/config.go`: `otto config` parent + subcommands: `set`, `show`
- All commands are stubs that print "not implemented" — wired later
- Wire all into `cmd/otto/main.go`

### 2.2 — Implement `otto config show` and `otto config set`
- **status**: pending

- `otto config show`: Load merged config, pretty-print as JSONC (re-serialize with comments stripped)
- `otto config set <key> <value>`: Write to repo-level `.otto/otto.jsonc` (create if needed); use dotted key path (e.g., `models.primary`)
  - **Implementation approach:** Read file → `jsonc.ToJSON` → unmarshal to `map[string]any` → navigate dotted path, creating intermediate maps as needed → set value → marshal back to indented JSON → write
  - **Limitation: comments in the JSONC file are lost on write.** `tidwall/jsonc` is a one-way preprocessor and cannot round-trip comments. Document this limitation prominently in `--help` text. Consider `tidwall/sjson` as an alternative for in-place modification (preserves structure but still loses comments).
- Both are simple file operations, no LLM needed

### 2.3 — Implement repo management (`otto repo add/remove/list`) [parallel]
- **status**: pending

- `internal/repo/repo.go`:
  - `Add(config, repoConfig)`: validates paths exist, appends to `repos` array in user config, writes config
  - `Remove(config, name)`: removes by name from `repos` array, writes config
  - `List(config) -> []RepoConfig`: returns repos list
  - `FindByRemoteURL(config, remoteURL) -> (*RepoConfig, error)`: matches a git remote URL to a configured repo
  - `FindByCWD(config, cwd) -> (*RepoConfig, error)`: matches CWD's git remote to a configured repo
- `otto repo add`: interactive prompt (using `huh`) for name, primary_dir, worktree_dir, git_strategy, branch_template, branch_patterns
- `otto repo remove <name>`: removes by name
- `otto repo list`: table output using `lipgloss/table`

### 2.4 — Implement git strategy and worktree operations
- **status**: pending

- `internal/repo/strategy.go`:
  - `GitStrategy` interface: `CreateBranch(name) -> (workDir, error)`, `CheckoutBranch(branch) -> (workDir, error)`, `CurrentBranch() -> string`, `RemoveBranch(name) -> error`
  - `WorktreeStrategy`, `BranchStrategy`, `HandsOffStrategy` implementations
  - Branch name derivation from `branch_template` (parse Go `text/template`, execute with `{.name}`)
- `internal/repo/worktree.go`:
  - `Add(repo, name) -> (workDir, error)`: delegates to strategy
  - `List(repo) -> ([]WorktreeInfo, error)`: `git worktree list` or branch listing depending on strategy
  - `Remove(repo, name, force) -> error`: removes worktree dir + optionally deletes branch
  - Dirty state detection: `git status --porcelain` on the work directory
- Wire into `otto worktree add/list/remove` CLI commands
- Integration tests with a temp git repo

### 2.5 — Implement PR-to-worktree mapping
- **status**: pending

- `internal/repo/mapping.go`:
  - `MapPRToWorkDir(config, repoURL, branchName) -> (workDir string, cleanup func(), error)`:
    1. Find matching repo via `FindByRemoteURL`
    2. Based on `git_strategy`:
       - `worktree`: parse branch against template, derive worktree name, create if not exists under `worktree_dir`
       - `branch`: `git fetch && git checkout` in `primary_dir`
       - `hands-off`: verify current branch matches, error if not
    3. Return the working directory and a cleanup function (for temp worktrees)
  - **Branch template reverse mapping:** constrain `branch_template` to have `{{.name}}` as a suffix or prefix only (e.g., `users/alanmeadows/{{.name}}`). Implement reverse extraction as a simple prefix/suffix strip. If template is more complex, generate a regex from the template (replace `{{.name}}` with a named capture group, compile, match). Validate template format at config load time.
  - `MapPRReviewToWorkDir(config, repoURL, branchName) -> (workDir, cleanup, error)`:
    - Same as above but falls back to CWD match and temporary worktree creation (`/tmp/otto-review-<id>`)
- Tests for each strategy

### 2.6 — Implement codebase analysis [parallel]
- **status**: pending

- `internal/spec/analysis.go`:
  - `AnalyzeCodebase(repoDir string) -> (CodebaseSummary, error)`:
    - Detect project archetype by scanning for indicator files (`go.mod`, `Dockerfile`, `Makefile`, `package.json`, controller patterns, CRD types, etc.)
    - Parse dependency manifest (`go.mod` → extract key deps with versions)
    - Detect logging library (scan imports for slog, zap, logr, zerolog)
    - Detect testing patterns (scan for `testify`, `ginkgo`, `envtest`, table-driven patterns)
    - Detect error handling style (scan for `fmt.Errorf`, `errors.New`, custom error types)
    - Detect project layout (list top-level dirs, package naming)
    - Detect config loading (environment, flags, viper/koanf, CRD)
  - Output: structured `CodebaseSummary` rendered to string for `{{.codebase_summary}}` template variable
  - This is pure Go analysis (file scanning, regex matching) — no LLM calls

---

## Phase 3: Specification Pipeline (Core Value)

The spec workflow. Depends on Phase 1 (OpenCode layer) and Phase 2 (CLI + repo management).

### 3.1 — Implement spec types and directory management
- **status**: pending

- `internal/spec/spec.go`:
  - `Spec` struct: `Slug`, `Dir`, `RequirementsPath`, `ResearchPath`, `DesignPath`, `TasksPath`, `QuestionsPath`, `HistoryDir`
  - `LoadSpec(slug, repoDir) -> (*Spec, error)`: resolves `.otto/specs/<slug>/` and checks what artifacts exist
  - `ListSpecs(repoDir) -> ([]Spec, error)`: lists all specs under `.otto/specs/`
  - `ResolveSpec(slug, repoDir) -> (*Spec, error)`: handles auto-resolution when only one spec exists, prefix matching
  - `CreateSpecDir(slug, repoDir) -> error`: creates the directory tree
- Artifact existence helpers: `HasRequirements()`, `HasResearch()`, `HasDesign()`, `HasTasks()`, `HasQuestions()`

### 3.2 — Implement pipeline enforcement
- **status**: pending

- `internal/spec/pipeline.go`:
  - `CheckPrerequisites(spec, command) -> error`:
    - `requirements`: requires requirements.md exists
    - `research`: requires requirements.md
    - `design`: requires requirements.md + research.md
    - `task generate`: requires requirements.md + research.md + design.md
    - `execute`: requires all four
    - `run`: no enforcement (exempt)
  - Returns human-readable error with suggested next step
- Wire into each spec CLI command's `RunE` function

### 3.3 — Validate and refine seed prompt templates
- **status**: pending

**⚠ BLOCKING DEPENDENCY for all tasks 3.4–3.14. No spec command can produce meaningful output without substantive prompt templates.**

**Seed prompts already exist** at `internal/prompts/` — 10 production-quality templates totalling ~1,000 lines. These are NOT stubs; they contain structured instructions, output format specs, and variable references. The work here is to **validate compatibility with the system being built**, not to write prompts from scratch.

For each template below:
1. Read the existing file and verify all `{{.variable}}` references match the Template Variables table (design.md line 93)
2. Ensure the output format the prompt requests matches what the consuming Go code expects
3. Add any missing `{{.variable}}` references identified below
4. Smoke test: manually construct a sample data map and `template.Execute()` — verify no parse errors
5. Write a unit test in `internal/prompts/loader_test.go` that parses every template with all variables populated

#### 3.3.1 — `requirements.md` template
- Existing file: `internal/prompts/requirements.md` (~128 lines) — covers analysis framework, FR/NFR structure, questions output
- Validate: `{{.prompt}}`, `{{.existing_requirements}}`, `{{.codebase_summary}}` variables present
- Verify output format matches what `spec add` / `spec requirements` expects to write to disk

#### 3.3.2 — `research.md` template
- Existing file: `internal/prompts/research.md` (~188 lines) — covers tool usage mandates, confidence tags [VERIFIED/CODEBASE/UNVERIFIED], codebase-first methodology
- Validate: `{{.requirements}}`, `{{.existing_research}}`, `{{.codebase_summary}}` variables present

#### 3.3.3 — `design.md` template
- Existing file: `internal/prompts/design.md` (~176 lines) — covers concrete naming, interface-first, refinement mode support
- Validate: `{{.requirements}}`, `{{.research}}`, `{{.existing_design}}`, `{{.tasks}}`, `{{.codebase_summary}}` variables present

#### 3.3.4 — `tasks.md` template
- Existing file: `internal/prompts/tasks.md` (~164 lines) — covers sizing rules, parallel grouping, self-contained descriptions, re-run rules
- Validate: `{{.requirements}}`, `{{.research}}`, `{{.design}}`, `{{.existing_tasks}}`, `{{.codebase_summary}}` variables present
- **Add `{{.phase_summaries}}` variable** if not already present — needed for re-generation after partial execution

#### 3.3.5 — `review.md` template
- Existing file: `internal/prompts/review.md` (~41 lines) — covers severity levels, structured issue output
- Validate: `{{.artifact}}`, `{{.artifact_type}}` variables present

#### 3.3.6 — `phase-review.md` template
- Existing file: `internal/prompts/phase-review.md` (~37 lines) — covers bug detection, fix application
- **Add `{{.phase_summaries}}` variable** if not already present — reviewer needs prior phase context
- Validate: `{{.uncommitted_changes}}` or equivalent diff variable present

#### 3.3.7 — `question-harvest.md` template
- Existing file: `internal/prompts/question-harvest.md` (~47 lines) — covers uncertainty extraction from logs
- **Add `{{.phase_summaries}}` variable** if not already present — harvester needs context of accomplishments
- Validate: `{{.execution_logs}}` or equivalent variable present

#### 3.3.8 — `question-resolve.md` template
- Existing file: `internal/prompts/question-resolve.md` (~52 lines) — covers auto-resolution with confidence assessment
- Validate: `{{.question}}`, `{{.requirements}}`, `{{.research}}`, `{{.design}}` variables present

#### 3.3.9 — `pr-review.md` template
- Existing file: `internal/prompts/pr-review.md` (~80 lines) — covers git diff workflow, severity levels, JSON output
- Validate: `{{.pr_title}}`, `{{.pr_description}}`, `{{.target_branch}}` variables present
- Verify JSON output schema matches what `pr review` (7.5) expects to parse

#### 3.3.10 — `pr-comment-respond.md` template
- Existing file: `internal/prompts/pr-comment-respond.md` (~66 lines) — covers AGREE/BY_DESIGN/WONT_FIX decisions, JSON output
- Validate: `{{.comment_body}}`, `{{.file_path}}`, `{{.line_number}}`, `{{.code_context}}` variables present
- Verify JSON output schema matches what comment handler (8.4) expects to parse

**For each template:** use `{{.variable}}` syntax matching the Template Variables table (design.md line 93). The existing prompts already use this syntax — verify no mismatches. Write a comprehensive unit test in `internal/prompts/loader_test.go` that `template.New().Parse()`s every embedded template with a fully-populated data map and asserts no errors.

### 3.4 — Implement `otto spec add <prompt>`
- **status**: pending

- `internal/spec/requirements.go` (creation path):
  1. Generate slug from prompt (sanitize, kebab-case, truncate)
  2. Create spec directory
  3. **Run `AnalyzeCodebase()` and inject result as `{{.codebase_summary}}`**
  4. Build prompt from `requirements.md` template with `{{.prompt}}` and `{{.codebase_summary}}` data
  5. Run multi-model review pipeline → get final requirements.md
  6. Write to `.otto/specs/<slug>/requirements.md`
  7. Print slug and path
- **Extract shared `generateRequirements(spec, prompt, existingContent, codebaseSummary)` function — reused by task 3.5**
- CLI wiring in `internal/cli/spec.go`

### 3.5 — Implement `otto spec requirements [--spec <slug>]`
- **status**: pending

- `internal/spec/requirements.go` (refinement path):
  1. Resolve and load spec
  2. Check prerequisites (requirements.md must exist)
  3. **Run `AnalyzeCodebase()` and inject result as `{{.codebase_summary}}`**
  4. Build prompt with existing requirements.md + all other existing artifacts as context + `{{.codebase_summary}}`
  5. Run multi-model review pipeline
  6. Overwrite requirements.md with refined version
  7. If issues found, append to questions.md
- **Reuse shared `generateRequirements()` function from task 3.4**

### 3.6 — Implement `otto spec research [--spec <slug>]`
- **status**: pending

- `internal/spec/research.go`:
  1. Resolve and load spec
  2. Check prerequisites (requirements.md)
  3. Build prompt: requirements.md, existing research.md (if any), design.md, tasks.md, codebase_summary
  4. Run multi-model review pipeline
  5. Write/overwrite research.md

### 3.7 — Implement `otto spec design [--spec <slug>]`
- **status**: pending

- `internal/spec/design.go`:
  1. Resolve and load spec
  2. Check prerequisites (requirements.md + research.md)
  3. Build prompt: requirements.md, research.md, existing design.md, tasks.md, questions.md, codebase_summary
  4. Run multi-model review pipeline
  5. Write/overwrite design.md

### 3.8 — Implement `otto spec task generate [--spec <slug>]`
- **status**: pending

- `internal/spec/tasks.go` (generation path):
  1. Resolve and load spec
  2. Check prerequisites (requirements + research + design)
  3. Build prompt: all spec docs, existing tasks.md (if re-running — preserve completed task statuses), codebase_summary
  4. Run multi-model review pipeline
  5. Write/overwrite tasks.md
  6. Parse and validate task structure (IDs, parallel groups, depends_on references)

### 3.9 — Implement task parsing and management
- **status**: pending

- `internal/spec/tasks.go` (parsing):
  - `ParseTasks(tasksPath) -> ([]Task, error)`: parse the markdown-with-inline-metadata format
  - `Task` struct: `ID`, `Status`, `ParallelGroup`, `DependsOn []string`, `Description`, `Files []string`, `RetryCount int`
  - `UpdateTaskStatus(tasksPath, taskID, status) -> error`: flock-protected status update
  - `GetRunnableTasks(tasks) -> []Task`: tasks with `status: pending` whose dependencies are all `completed`
  - `BuildPhases(tasks) -> [][]Task`: group tasks by `parallel_group`, ordered by group number. **Validate that all `depends_on` references for tasks in phase N are satisfied by tasks in phases < N. If a dependency is unsatisfied, error with a clear message identifying the circular or out-of-order dependency. Phase selection at runtime must check that all deps are `completed` before launching a phase — do not rely solely on group ordering.**
- `internal/cli/spec_task.go`: wire `task list`, `task add`, `task run`

### 3.10 — Implement `otto spec task add [--spec <slug>] <prompt>` [parallel]
- **status**: pending

- LLM-first approach: send current tasks.md + new task prompt → LLM returns updated tasks.md
- Write result back (flock-protected)
- No Go-side graph analysis

### 3.11 — Implement `otto spec task run [--spec <slug>] [--id <taskid>]` [parallel]
- **status**: pending

- `internal/spec/runner.go`:
  - **Mark task status as `running` in tasks.md (flock-protected) before doing any work** — this is required for accurate progress display and crash detection
  - Create fresh OpenCode session (directory-scoped)
  - Ensure permissions file exists in worktree
  - Build prompt: task description + relevant spec docs + prior phase summaries
  - Send prompt, wait for completion
  - Capture dialog log → save to `history/run-NNN.md`
  - Update task status to `completed` or `failed` in tasks.md (flock-protected)
  - Delete session
  - **Parameter inference:** when `--id` is omitted and only one pending task with satisfied dependencies exists, use it automatically. When multiple runnable tasks exist, print options and exit. (per design.md Parameter Inference table)

### 3.12 — Implement `otto spec questions [--spec <slug>]`
- **status**: pending

- `internal/spec/questions.go`:
  1. Parse questions.md, filter `status: unanswered`
  2. Auto-resolution pass: for each question, create LLM session with `question-resolve.md` template + full spec context
  3. Secondary validates each auto-answer (fresh session, review prompt)
  4. If validated → update status to `auto-answered` with reasoning
  5. Present remaining unanswered questions to user (using `huh` prompts for interactive input)
  6. Record user answers with `status: answered`

### 3.13 — Implement `otto spec run [--spec <slug>] <prompt>`
- **status**: pending

- Create OpenCode session with full spec context loaded
- Send user's prompt
- Print response
- Session NOT deleted (user may continue)
- No multi-model review

### 3.14 — Implement `otto spec list`
- **status**: pending

- List all specs under `.otto/specs/` with artifact status and task completion percentages
- Table output using `lipgloss/table`

---

## Phase 4: Task Execution Engine

The most critical component. Depends on Phase 3 (spec pipeline) and Phase 1 (OpenCode layer).

### 4.1 — Implement the outer execution loop
- **status**: pending

- `internal/spec/execute.go`:
  - `Execute(ctx, spec, config, serverMgr) -> error`

This is the most mechanically complex code in the project. Break into sub-tasks:

#### 4.1a — Sequential single-task execution
- Parse tasks.md → build dependency graph
- **Derive phase ordering from the dependency graph, validated against `parallel_group` assignments.** Identify next phase as the lowest `parallel_group` where all `depends_on` for every task in the group are `completed` and at least one task is `pending`.
- Skip completed phases (all tasks in phase have `status: completed`)
- For each pending/partial phase, run tasks sequentially via `runner.RunTask()` (Phase 3, task 3.11)
- **On task start: mark status `running`. On completion: mark `completed` or `failed`.** Tasks found with status `running` on resume are treated as crashed — reset to `pending` for retry.
- Crash recovery: re-running picks up from first incomplete phase, skips completed tasks

#### 4.1b — Parallel execution with semaphore
- Launch tasks concurrently (bounded by `spec.max_parallel_tasks` via semaphore)
- **Pre-flight overlap check:** before launching parallel tasks, check for overlapping `files` lists between tasks in the same phase. Log a warning if overlap is detected. Consider serializing overlapping tasks within a phase.
- Wait for all tasks in phase to complete
- The outer loop never dies on LLM errors — catches, logs, retries or marks failed

#### 4.1c — Retry logic with error context
- Per-task: retry on failure up to `max_task_retries` (default 15), fresh session each retry with previous error context injected into the prompt
- Escalating context: each retry includes the error from the previous attempt

#### 4.1d — Crash recovery and resume
- Resumability is a core design principle: re-running `spec execute` picks up from the first incomplete phase and skips completed tasks
- Task status persistence via flock-protected writes ensures safe crash recovery
- **Integration test (can defer to 10.1):** start execution, kill process mid-phase, re-run `spec execute`, verify it resumes from the correct phase and skips completed tasks

#### 4.1e — Phase commit logic
- After all tasks in a phase complete, git commit phase changes
- **Commit message format: `otto: phase N — <summary>`. Use `git add -A` for all changes. If all tasks in a phase failed, skip commit. If diff is empty after review gate, skip commit with a log message.**
- Phase review gate (see 4.2) runs before the commit

#### 4.1f — Health-check watchdog
- **Before each task launch, verify OpenCode server health via `HealthCheck()`. If unhealthy, pause launches, attempt restart via `ServerManager.EnsureRunning()`, then resume. Log all health transitions.**
- Protects against OpenCode server crashes under concurrent session load

### 4.2 — Implement phase review gate
- **status**: pending

- Within `execute.go`:
  - After all tasks in a phase complete:
    1. Secondary model reviews all uncommitted changes in a fresh session (`phase-review.md` template)
    2. (Optional) tertiary model reviews independently
    3. Primary model incorporates feedback in a fresh session, applies fixes
  - All sessions are directory-scoped and deleted after use

### 4.3 — Implement question harvesting
- **status**: pending

- Within `execute.go`, after each phase:
  - Collect dialog logs from all task runs in the phase (from `history/run-NNN.md`)
  - Create LLM session with `question-harvest.md` template + concatenated logs
  - LLM extracts uncertainties, assumptions, open questions
  - Append to questions.md with `status: unanswered`, `source: phase-N-harvest`
  - Delete session
  - Non-blocking: execution continues regardless of harvested questions

### 4.4 — Implement progress display
- **status**: pending

- `internal/spec/execute.go` or `internal/cli/spec.go`:
  - Live progress table using `lipgloss/table`:
    - Per-phase: task ID, status, duration, summary
    - Review gate results
    - Commit hashes
    - Overall: X/Y completed, Z running, W failed
  - Update display after each task completion and phase transition

### 4.5 — Implement summary chaining
- **status**: pending

- After each phase commit, generate a brief summary of what changed (from the commit message + task summaries)
- Persist phase summaries to `.otto/specs/<slug>/phase-summaries.md` (append per phase) so they survive crashes
- Feed accumulated summaries into the next phase's task prompts via `{{.phase_summaries}}` template variable
- **Wire `{{.phase_summaries}}` into the following prompt templates** (update tasks 3.3.4, 3.3.6, 3.3.7):
  - `tasks.md` — for re-generation context after partial execution
  - `phase-review.md` — reviewer needs to know prior phase outcomes
  - `question-harvest.md` — harvester benefits from knowing what has been accomplished
  - Individual task prompts in `runner.RunTask()` — injected at runtime, not baked into a template file
- Ensures downstream tasks know what upstream phases accomplished

---

## Phase 5: PR Backend — Interface & ADO Implementation

Build the PR backend abstraction and the primary ADO implementation.

### 5.1 — Define PRBackend interface and types
- **status**: pending

- `internal/provider/provider.go`:
  - `PRBackend` interface with all methods from design.md (line 977): `Name()`, `MatchesURL()`, `GetPR()`, `GetPipelineStatus()`, `GetBuildLogs()`, `GetComments()`, `PostComment()`, `PostInlineComment()`, `ReplyToComment()`, `ResolveComment()`, `RunWorkflow()`
  - Supporting types: `PRInfo`, `PipelineStatus`, `Comment`, `InlineComment`, `CommentResolution`, `WorkflowAction`
  - **`WorkflowSubmit` action:** reserved for future `otto pr create` feature. Document as "reserved for future use" in interface comments. Backends should return `ErrUnsupported` for unimplemented actions.
- `internal/provider/registry.go`:
  - `Registry` struct: stores registered backends
  - `Detect(url) -> (PRBackend, error)`: matches URL hostname patterns to backends
  - `Get(providerName) -> (PRBackend, error)`: lookup by name

### 5.2 — Implement ADO auth strategy
- **status**: pending

- `internal/provider/ado/auth.go`:
  - Entra ID token via `az account get-access-token --resource 499b84ac-1321-427f-aa17-267ca6975798`
    - Execute via `os/exec`, parse JSON output
    - Cache token in memory, refresh when expired (tokens last 1 hour)
  - PAT fallback: `Authorization: Basic base64(:<pat>)` using `OTTO_ADO_PAT` env var
  - Auto-detect: try Entra first (for enrolled devices on WSL), fall back to PAT
- **Auth must be implemented before any ADO API calls (5.3–5.6) — all operations depend on authenticated HTTP client**
- Unit tests: test auth header construction, test token caching, test PAT fallback

### 5.3 — Implement ADO backend — core operations
- **status**: pending

- `internal/provider/ado/ado.go`:
  - HTTP client with Entra ID token auth (from 5.2) and PAT fallback
  - Token caching with 1-hour expiry refresh
  - Rate limit handling: monitor `X-RateLimit-Remaining`, `Retry-After` headers, back off on 429
  - `GetPR()`: `GET /_apis/git/repositories/{repo}/pullRequests/{prId}?api-version=7.1`
  - `GetPipelineStatus()`: `GET /_apis/build/builds?branchName=refs/pull/{prId}/merge&api-version=7.1`
  - `GetComments()`: `GET .../pullRequests/{prId}/threads?api-version=7.1` — returns all threads with comments
- `internal/provider/ado/types.go`: ADO API response struct definitions
- **Unit tests using `httptest.Server` to mock ADO API responses. Test: auth header construction, rate limit response handling (429), malformed API response, pagination.**

### 5.4 — Implement ADO backend — comments
- **status**: pending

- `PostComment()`: `POST .../threads` with a new thread containing a single comment (general PR comment)
- `PostInlineComment()`: `POST .../threads` with `threadContext` containing `filePath` (must start with `/`), `rightFileStart`, `rightFileEnd`
- `ReplyToComment()`: `POST .../threads/{threadId}/comments` with `parentCommentId`
- `ResolveComment()`: `PATCH .../threads/{threadId}` with `{ "status": "fixed"|"wontFix"|"byDesign" }`

### 5.5 — Implement ADO backend — build logs
- **status**: pending

- `GetBuildLogs()`:
  1. Fetch build timeline: `GET /_apis/build/builds/{buildId}/timeline?api-version=7.1`
  2. Find failed `Task` records (`result == "failed"`)
  3. Extract error messages from `issues[]` array (often sufficient)
  4. If more context needed: fetch individual log: `GET /_apis/build/builds/{buildId}/logs/{logId}?api-version=7.1`
  5. Support `startLine`/`endLine` for partial retrieval
  6. Strip ANSI codes using `charmbracelet/x/ansi`
  7. Apply error-anchored truncation (grep for `##[error]`, extract context windows, keep tail)
  8. Return distilled error summary

### 5.6 — Implement ADO backend — workflows
- **status**: pending

- `internal/provider/ado/workflow.go`:
  - `RunWorkflow()` dispatch on `WorkflowAction`:
    - `WorkflowAutoComplete`: `PATCH .../pullRequests/{prId}` with `autoCompleteSetBy`
    - `WorkflowCreateWorkItem`: `POST /_apis/wit/workitems/$Task` with `application/json-patch+json` body
    - `WorkflowAddressBot`: MerlinBot comment detection (filter threads by author displayName/uniqueName matching configurable bot names list) + LLM response
    - **MerlinBot re-scan risk:** MerlinBot may reopen resolved threads if the underlying code issue persists. After resolving a MerlinBot thread, track whether it was re-opened in subsequent poll cycles. If a thread is re-opened more than 2 times, escalate to the user via notification.

---

## Phase 6: PR Backend — GitHub Implementation

### 6.1 — Implement GitHub backend — core operations
- **status**: pending

- `internal/provider/github/github.go`:
  - Use `github.com/google/go-github/v82/github` (NOT v60 per research.md)
  - Auth: `github.NewClient(nil).WithAuthToken(os.Getenv("GITHUB_TOKEN"))`
  - Rate limit handling: use `go-github-ratelimit` middleware
  - `GetPR()`: `client.PullRequests.Get()`
  - `GetPipelineStatus()`:
    - Query **both** check runs (`client.Checks.ListCheckRunsForRef()`) AND combined commit status (`client.Repositories.GetCombinedStatus()`) per research.md
    - Merge results into unified `PipelineStatus`
  - `GetComments()`: `client.PullRequests.ListComments()` (review comments) + `client.Issues.ListComments()` (issue comments)
- `internal/provider/github/types.go`
- **Unit tests using `httptest.Server` to mock GitHub API responses. Test: auth header, rate limit handling (429/403 Abuse Detection), malformed API response, pagination.**

### 6.2 — Implement GitHub backend — comments
- **status**: pending

- `PostComment()`: `client.Issues.CreateComment()` (PRs are issues in GitHub's model)
- `PostInlineComment()`: prefer `client.PullRequests.CreateReview()` with batch comments (**use `CreateReview` with batch comments to avoid secondary rate limits on content-creation endpoints. Individual `CreateComment` calls in rapid succession will trigger 403 Abuse Detection.**)
  - Single inline comment: create a review with one `DraftReviewComment` and `Event: "COMMENT"`
  - Must use `Line`/`Side` (NOT deprecated `Position`), `CommitID` must be PR head SHA
- `ReplyToComment()`: `client.PullRequests.CreateCommentInReplyTo()` — **always reply to the root comment ID of the thread, never a nested reply's ID. Getting this wrong causes 422 errors.**
- `ResolveComment()`: requires GraphQL via `shurcooL/githubv4` — `resolveReviewThread` mutation (REST API cannot resolve threads per research.md)

### 6.3 — Implement GitHub backend — build logs
- **status**: pending

- `GetBuildLogs()`:
  1. List workflow runs: `client.Actions.ListRepositoryWorkflowRuns()` filtered by head SHA
  2. List jobs for failed run: `GET /repos/{owner}/{repo}/actions/runs/{run_id}/jobs`
  3. Filter to failed jobs (`conclusion: "failure"`)
  4. Download per-job log: `GET /repos/{owner}/{repo}/actions/jobs/{job_id}/logs` (302 → plain text, 1-min expiry URL)
  5. Strip ANSI codes, apply error-anchored truncation
  6. Return distilled error summary
  - Note: `GetWorkflowRunLogs()` returns a ZIP — prefer per-job logs for targeted retrieval

### 6.4 — Implement GitHub `RunWorkflow` stub
- **status**: pending

- `RunWorkflow()`: return `ErrUnsupported` for all workflow actions (auto-complete, work items, bot handling are ADO-only concepts)
- Document which actions are ADO-only in the method comments
- Future: may implement GitHub-equivalent actions (e.g., auto-merge via `client.PullRequests.EnableAutoMerge` if needed)

---

## Phase 7: PR Commands & Lifecycle

Wire PR backends into CLI commands and implement the PR document model.

### 7.1 — Implement PR document model
- **status**: pending

- `internal/store/pr.go` or extend `internal/provider/`:
  - PR document struct matching design.md (line 1652): frontmatter (id, provider, repo, branch, target, status, url, created, last_checked, fix_attempts, max_fix_attempts, seen_comment_ids) + markdown body (current state, fix history, comment history)
  - `LoadPR(id) -> (*PRDocument, error)`: reads from `~/.local/share/otto/prs/<provider>-<id>.md`
  - `SavePR(pr) -> error`: writes PR document
  - `ListPRs() -> ([]PRDocument, error)`: lists all PR documents
  - `DeletePR(id) -> error`: removes PR document

### 7.2 — Implement `otto pr add <url or id>`
- **status**: pending

1. Detect backend (URL pattern matching or `--provider` flag or `pr.default_provider`)
2. **URL parsing:** Parse ADO URLs matching `dev.azure.com/{org}/{project}/_git/{repo}/pullrequest/{id}` and `{org}.visualstudio.com/{project}/_git/{repo}/pullrequest/{id}`. Parse GitHub URLs matching `github.com/{owner}/{repo}/pull/{number}`. Use `net/url` + regex. When URL doesn't match any pattern and input is numeric-only, use `pr.default_provider`.
3. Fetch PR metadata via `GetPR()`
4. Create PR document with `status: watching`
5. POST to daemon API (`/prs`). **If daemon is unreachable, fail with a clear error: "cannot reach otto daemon at localhost:<port>. Start it with: otto server start".** Do NOT fall back to local file writes — all PR lifecycle commands require a running daemon per design.md.
6. If ADO + `auto_complete` enabled: call `RunWorkflow(WorkflowAutoComplete)`

### 7.3 — Implement `otto pr list`, `pr status`, `pr remove`, `pr log`
- **status**: pending

- `pr list`: Load all PR documents from `~/.local/share/otto/prs/`, display in `lipgloss/table`
- `pr status <id>`: Load specific PR, display detailed status including pipeline info, fix history, comment history
- `pr remove <id>`: Delete PR document, remove from daemon tracking
- `pr log <id>`: Display fix history from PR document
- **Parameter inference:** when `<id>` is omitted and only one PR is tracked, use it automatically. When multiple exist, print options and exit. Applies to `pr status`, `pr fix`, `pr log`, `pr remove`.

### 7.4 — Implement PR fix — two-phase LLM approach
- **status**: pending

- `internal/server/pr_loop.go`:
  - **Implement `FixPR()` as a standalone function in `internal/server/pr_loop.go`. Wire into both `otto pr fix` CLI command and the daemon monitoring loop (8.3).**
  - `FixPR(ctx, pr, backend, serverMgr, config) -> error`:
    1. Map PR to local worktree (via `MapPRToWorkDir`)
    2. Ensure OpenCode permissions in worktree
    3. Fetch build logs via `backend.GetBuildLogs()`
    4. **Phase 1**: Create OpenCode session in worktree, send failure summary prompt → LLM distills errors into structured diagnosis
    5. **Phase 2**: Create fresh OpenCode session in worktree, send fix prompt with Phase 1 diagnosis → LLM fixes code
    6. Commit and push: `git add -A && git commit -m "otto: fix ..." && git push`
    7. Update PR document: increment `fix_attempts`, add fix history entry
    8. If `fix_attempts >= max_fix_attempts`: set `status: failed`, post comment
    9. Else: set `status: watching`
    10. Delete both sessions

### 7.5 — Implement `otto pr review <url>` [parallel]
- **status**: pending

- `internal/cli/pr_review.go`:
  1. Detect backend from URL
  2. Fetch PR metadata
  3. Map to local repo (`MapPRReviewToWorkDir`) — find repo, fetch + checkout source branch
  4. Ensure OpenCode permissions
  5. Create OpenCode session (directory-scoped to worktree)
  6. Send `pr-review.md` prompt with PR title, description, target branch
  7. LLM runs `git diff origin/<target>...HEAD`, reads files, generates structured review
  8. Parse JSON response (with retry logic): array of `{file, line, severity, body}`
  9. Present comments in `lipgloss/table`, then interactive approval via `huh.NewMultiSelect` (all selected by default)
  10. Post approved comments via `backend.PostInlineComment()` (batch as review for GitHub)
  11. Optionally post summary comment via `backend.PostComment()`
  12. Cleanup: remove temp worktree if created

---

## Phase 8: Daemon & PR Monitoring

The background daemon for continuous PR tracking.

### 8.1 — Implement daemon process management
- **status**: pending
- **done-when**: `otto server start` creates PID file and background process; `otto server stop` terminates it; `otto server status` reports correctly; `--foreground` runs in current process

- `internal/server/daemon.go`:
  - `StartDaemon(config) -> error`:
    1. Check PID file — if process alive, print status and exit
    2. Fork: `exec.Command(os.Args[0], "server", "start", "--foreground")` with `SysProcAttr{Setsid: true}`
    3. Redirect stdout/stderr to log file at `config.server.log_dir`
    4. Detach stdin
    5. `cmd.Start()`, write PID file atomically (temp → rename), **`cmd.Process.Release()` (do NOT call `cmd.Wait()` in the parent — it blocks)**
    6. Print "daemon started (PID: N)"
  - **`--foreground` flag:** when set, skip fork/setsid, run the HTTP server directly in the current process. No PID file needed. Required for systemd `Type=simple` services and debugging.
  - **Signal handling:** install signal handlers via `signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)`. Pass context to HTTP server and monitoring loop for coordinated graceful shutdown.
  - **Daemonization gotchas (from research.md):**
    - Don't call `cmd.Wait()` in parent — use `cmd.Process.Release()` instead
    - `Pdeathsig` is unreliable in Go (Go issue #27505) — don't use it
    - Detect systemd via `$INVOCATION_ID` or `$NOTIFY_SOCKET` to avoid double-daemonization
    - No double-fork needed — `Setsid: true` is sufficient (don't over-engineer)
    - Consider `lumberjack` or SIGHUP-based log rotation for long-running daemon
  - `StopDaemon() -> error`: read PID file, `syscall.Kill(pid, syscall.SIGTERM)`, wait with timeout, remove PID file
  - `DaemonStatus() -> (running bool, pid int, uptime, error)`: read PID, check alive via `syscall.Kill(pid, 0)`
  - PID file at `~/.local/share/otto/ottod.pid`
  - Stale PID detection: `syscall.Kill(pid, 0)` → `ESRCH` means stale
- **Integration tests:** start daemon, verify PID file exists and process is alive. Stop daemon, verify PID file removed and process is dead. Write stale PID file, verify `DaemonStatus` detects it. Start daemon while another is running, verify it prints status and exits.

### 8.2 — Implement daemon HTTP server
- **status**: pending

- `internal/server/server.go`:
  - HTTP server on `config.server.port` (default 4097)
  - `internal/server/api.go`: handlers for:
    - `GET /status`: server health, uptime, tracked PR count
    - `GET /prs`: list tracked PRs (read from `~/.local/share/otto/prs/`)
    - `POST /prs`: add a PR (write document + start tracking)
    - `DELETE /prs/:id`: remove PR
    - `POST /prs/:id/fix`: manually trigger fix
    - `GET /events`: SSE stream of otto events (optional, can defer)
  - Graceful shutdown on context cancellation

### 8.3 — Implement PR monitoring loop (includes comment monitoring)
- **status**: pending

- `internal/server/pr_loop.go`:
  - `RunMonitorLoop(ctx, config, serverMgr) -> error`:
    - Ticker at `config.server.poll_interval`
    - Each tick: iterate all PR documents with `status: watching`
    - For each PR:
      1. Determine backend from provider field
      2. Fetch pipeline status → update PR doc
      3. If passed → set `status: green`, post success comment
      4. If failed → call `FixPR()` (Phase 7, task 7.4)
      5. If ADO + merlinbot → check/address MerlinBot comments via `RunWorkflow(WorkflowAddressBot)`
      6. **Comment evaluation (previously 8.4):** after pipeline check, run comment monitoring inline:
         a. `backend.GetComments()` → get all comments
         b. Diff against `seen_comment_ids` in PR frontmatter
         c. For each new, unresolved comment: evaluate and respond (logic extracted to `evaluateComment()` helper)
         d. Update `seen_comment_ids` in PR frontmatter
    - Catch all errors per-PR (never crash the loop)
    - Log each poll cycle result

### 8.4 — Implement comment evaluation and response logic
- **status**: pending

- `internal/server/comments.go`:
  - `evaluateComment(ctx, pr, comment, backend, serverMgr, config) -> error`:
    - Called by the monitoring loop (8.3) for each new, unresolved comment
    1. Map PR to worktree, create OpenCode session
    2. Send `pr-comment-respond.md` prompt with comment body, thread context, file/line, code context
    3. Parse LLM response: `AGREE`, `BY_DESIGN`, or `WONT_FIX`
    4. If AGREE: LLM applies fix in worktree → commit and push → `backend.ReplyToComment()` with "Fixed in <commit>" → `backend.ResolveComment(ResolutionResolved)`
    5. If BY_DESIGN: `backend.ReplyToComment()` with rationale → `backend.ResolveComment(ResolutionByDesign)`
    6. If WONT_FIX: `backend.ReplyToComment()` with explanation → `backend.ResolveComment(ResolutionWontFix)`
    7. Add comment history entry to PR document body
  - This is the function called by the monitoring loop, not a separate loop

### 8.5 — Implement `otto server install` (systemd)
- **status**: pending
- **done-when**: `otto server install` writes unit file to `~/.config/systemd/user/otto.service` and runs `systemctl --user daemon-reload`

- Generate systemd user unit file:
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
- Write to `~/.config/systemd/user/otto.service`
- Run `systemctl --user daemon-reload && systemctl --user enable otto`
- Idempotent: overwrites existing unit file

### 8.6 — Wire CLI PR commands to daemon API
- **status**: pending

- `internal/cli/pr.go`:
  - `pr list`, `pr add`, `pr status`, `pr remove`, `pr fix`, `pr log` all call daemon HTTP API at `localhost:<port>`
  - If daemon unreachable, print error: "Cannot reach otto daemon. Start it with: otto server start"
  - Structured error messages with next-step guidance

---

## Phase 9: Notifications

Optional but requested feature. Depends on daemon (Phase 8).

### 9.1 — Implement Teams notification via Power Automate
- **status**: pending

- `internal/server/notify.go`:
  - `Notify(ctx, config, event NotificationEvent) -> error`:
    - POST to configured Power Automate webhook URL
    - Payload: Adaptive Card JSON with event details
  - `NotificationEvent` types: `PRGreen`, `PRFailed`, `SpecComplete`, `CommentHandled`
  - Config uses `NotificationsConfig` struct defined in Phase 0 (task 0.3):
    ```jsonc
    "notifications": {
      "teams_webhook_url": "",        // Power Automate webhook URL
      "events": ["pr_green", "pr_failed", "spec_complete"]
    }
    ```
  - Treat webhook URL as a secret (no logging)
- Wire into: PR monitoring loop (green/failed), spec execution (completion), comment handling

### 9.2 — Design Adaptive Card templates
- **status**: pending

- Rich notification cards with:
  - PR title, branch, status
  - Link to PR on ADO/GitHub
  - Fix attempt count
  - Error summary (for failures)

---

## Phase 10: Polish, Testing & Documentation

### 10.1 — End-to-end integration tests
- **status**: pending

- Set up test fixtures: temp git repos, mock OpenCode server (reuse mocks from 1.0), mock PR backend HTTP servers
- Test critical paths:
  - Full spec pipeline: add → requirements → research → design → task generate → execute
  - PR lifecycle: add → detect failure → fix → green
  - PR review: review third-party PR → generate comments → post
  - Comment monitoring: detect comment → evaluate → respond
  - Config loading: user + repo merge, env var overrides
  - Worktree management: add/list/remove with each git strategy
  - **Crash recovery:** start execution, kill process mid-phase, re-run `spec execute`, verify it resumes from the correct phase and skips completed tasks
  - **Concurrent tasks.md access:** launch 8 goroutines each calling `UpdateTaskStatus()` on different task IDs simultaneously. Verify no data corruption, no deadlocks, and all updates are reflected.
  - **PR review quality (manual):** run `pr review` against a known-buggy PR (create a test repo with intentional bugs). Verify the generated comments identify at least the intentional bugs. Use this as a regression fixture.

### 10.2 — Error handling audit
- **status**: pending

- Ensure all LLM call sites handle: network errors, timeout, malformed response, rate limiting
- Ensure all file I/O handles: permission errors, disk full, concurrent modification
- Ensure all git operations handle: detached HEAD, dirty state, merge conflicts, network failures
- Ensure daemon handles: OpenCode server crash, PR backend downtime, invalid PR URLs

### 10.3 — CLI help text and examples
- **status**: pending

- Write detailed `Long` descriptions and `Example` strings for all cobra commands
- Ensure `--help` output is clear and self-documenting
- Add shell completion support via cobra's built-in completion commands

### 10.4 — Build and release pipeline
- **status**: pending

- `Makefile` or `Taskfile` with: `build`, `test`, `lint`, `install` targets
- `goreleaser` config for cross-platform binary releases
- CI pipeline (GitHub Actions): lint, test, build on PRs; release on tags

### 10.5 — Update design.md with implementation learnings
- **status**: pending

- Record any design decisions made during implementation
- Update dependency versions to match actual `go.mod`
- Fix go-github version reference from v60 to v82
- Add any new resolved decisions

### 10.6 — Write user-facing README
- **status**: pending
- **done-when**: `README.md` exists at repo root with installation, quick start, configuration reference, and command reference sections

- Installation instructions
- Quick start guide
- Configuration reference
- Command reference (auto-generated from cobra if possible)
- Example workflows (spec pipeline, PR monitoring)

---

## Dependency Graph Summary

```
Phase 0 (Foundation)
  ├─> Phase 1 (OpenCode Layer)
  └─> Phase 2 (CLI + Repo)
        ├─> Phase 3 (Spec Pipeline)   ← requires Phase 1
        │     └─> Phase 4 (Execution Engine)
        ├─> Phase 5 (ADO Backend)
        │     └─> Phase 7 (PR Commands)   ← Phase 7 depends on Phase 5 ONLY
        │           └─> Phase 8 (Daemon + Monitoring)
        │                 └─> Phase 9 (Notifications)
        └─> Phase 6 (GitHub Backend)   ← parallelizable with Phase 7
  Phase 10 (Polish) — runs alongside/after all phases
```

**Critical path:** 0 → 1 → 3 → 4 (spec pipeline is the core value proposition)
**Secondary path:** 0 → 2 → 5 → 7 → 8 (PR lifecycle — ADO is primary)
**Parallelizable:** Phase 6 (GitHub) can proceed independently and attach via the registry pattern in 5.1; Phase 7 does not block on Phase 6
**Can start early:** Phase 2 tasks that don't depend on OpenCode (repo management, CLI skeleton)
**Deferrable:** Phase 9 (notifications), Phase 10 (polish)

---

## Review Feedback Incorporated

This task list incorporates the following feedback from a comprehensive review:

**Missing Tasks Added:**
- 0.8: OpenCode permission config writer (moved from Phase 1 to Phase 0 — no SDK dependency)
- 1.0: OpenCode mock strategy (prerequisite for all Phase 1 tests)
- 6.4: GitHub `RunWorkflow` stub
- SDK client auth header configuration (added to 1.2)
- Codebase summary wired into `spec add` (3.4) and `spec requirements` (3.5)
- `NotificationsConfig` defined early in 0.3
- Repo root detection via `git rev-parse --show-toplevel` (added to 0.4)
- `--foreground` mode and signal handling (added to 8.1)
- PR parameter inference for single-PR auto-selection (added to 7.3)

**Ordering Fixes:**
- ADO auth (was 5.6) moved to 5.2 — must precede all ADO API calls
- Phase 7 depends only on Phase 5 (ADO); Phase 6 (GitHub) is parallelizable
- Task 2.6 marked `[parallel]`
- Task 3.3 marked as blocking dependency for 3.4–3.14

**Under-specified Tasks Clarified:**
- 1.5: "differs substantially" iteration heuristic defined
- 4.1: broken into sub-tasks (4.1a–4.1f) with commit mechanics specified
- 3.3: broken into 10 sub-tasks (3.3.1–3.3.10)
- 7.2: URL parsing patterns for ADO and GitHub specified
- 2.2: `config set` dotted key implementation approach and comment loss limitation documented

**Research Findings Incorporated:**
- 8.1: Five daemonization gotchas from research.md §4
- 6.2: Root comment ID reply constraint and secondary rate limit justification
- 0.8: Granular permission config (`doom_loop`, `external_directory` set to `allow`)
- 0.2: lipgloss v2 avoidance pinned to v1.x
- 5.6: MerlinBot re-scan risk and escalation strategy
- 1.3: `Event.ListStreaming()` method name documented
- 1.2: SDK client auth header construction

**Testing Gaps Addressed:**
- 1.0: OpenCode mock strategy (new task)
- 1.5: Unit test specifications added
- 4.1d: Crash recovery integration test
- 5.3, 6.1: Unit tests with `httptest.Server` specified
- 8.1: Daemon lifecycle integration tests specified
- 10.1: Concurrent access stress test, crash recovery test, PR review quality test added

**Redundancies Resolved:**
- 8.3/8.4: Clarified relationship — 8.3 is the loop, 8.4 is the extracted evaluation function
- 7.4: `FixPR()` placement clarified as standalone function reused by CLI and daemon
- 3.4/3.5: Shared `generateRequirements()` extraction noted

**Design Contract Alignments (second review):**
- 7.2: PR commands now fail with clear error when daemon is unreachable — no local fallback (matches design.md daemon contract)
- 1.2: `EnsureRunning()` now checks `config.opencode.auto_start` before spawning; returns clear error when disabled
- 3.9/4.1a: Phase selection now validates `depends_on` against dependency graph, not just `parallel_group` ordering
- 3.11/4.1a: Task status `running` is set at task start, transitions to `completed`/`failed`; `running` tasks on resume treated as crashed
- 3.11: `spec task run` parameter inference added (auto-resolve when only one runnable task exists)
- 4.5/3.3.4/3.3.6/3.3.7: `{{.phase_summaries}}` wired into tasks, phase-review, and question-harvest templates; summaries persisted to file
