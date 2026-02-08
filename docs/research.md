# Otto — Deep Research Findings

This document captures detailed research on implementation-critical topics identified in `design.md`. Each section contains API surface details, gotchas, library recommendations, and actionable implementation guidance.

**Research date:** June 2025

---

## Table of Contents

1. [OpenCode Go SDK](#1-opencode-go-sdk)
2. [Azure DevOps REST API](#2-azure-devops-rest-api)
3. [GitHub PR API & go-github](#3-github-pr-api--go-github)
4. [Go Process Daemonization](#4-go-process-daemonization)
5. [File Locking (flock)](#5-file-locking-flock)
6. [JSONC Deep Merge](#6-jsonc-deep-merge)
7. [Teams Notification APIs](#7-teams-notification-apis)
8. [Charmbracelet Libraries](#8-charmbracelet-libraries)
9. [MerlinBot & Build Log Analysis](#9-merlinbot--build-log-analysis)

---

## 1. OpenCode Go SDK

**Package:** `github.com/sst/opencode-sdk-go` (v0.19.2+)
**Also published as:** `github.com/anomalyco/opencode-sdk-go` (mirrors/redirects to `sst`)

### 1.1 Client API Surface

The `Client` struct exposes these service fields:

```go
type Client struct {
    Options []option.RequestOption
    Event   *EventService
    Path    *PathService
    App     *AppService
    Agent   *AgentService
    Find    *FindService
    File    *FileService
    Config  *ConfigService
    Command *CommandService
    Project *ProjectService
    Session *SessionService
    Tui     *TuiService
}
```

### 1.2 Session Service — Complete Method Table

| Method | Signature | HTTP | Notes |
|---|---|---|---|
| **New** | `(ctx, SessionNewParams) -> (*Session, error)` | `POST /session` | Creates a new session |
| **Get** | `(ctx, id, SessionGetParams) -> (*Session, error)` | `GET /session/{id}` | |
| **List** | `(ctx, SessionListParams) -> (*[]Session, error)` | `GET /session` | |
| **Delete** | `(ctx, id, SessionDeleteParams) -> (*bool, error)` | `DELETE /session/{id}` | Returns `*bool` (true on success). Added v0.5.0. |
| **Prompt** | `(ctx, id, SessionPromptParams) -> (*SessionPromptResponse, error)` | `POST /session/{id}/message` | Added v0.8.0 |
| **Messages** | `(ctx, id, SessionMessagesParams) -> (*[]SessionMessagesResponse, error)` | `GET /session/{id}/message` | |
| **Abort** | `(ctx, id, SessionAbortParams) -> (*bool, error)` | `POST /session/{id}/abort` | |
| **Update** | `(ctx, id, SessionUpdateParams) -> (*Session, error)` | `PATCH /session/{id}` | |
| **Children** | `(ctx, id, SessionChildrenParams) -> (...)` | `GET /session/{id}/children` | |
| **Command** | `(ctx, id, SessionCommandParams) -> (...)` | `POST /session/{id}/command` | |
| **Init** | `(ctx, id, SessionInitParams) -> (...)` | `POST /session/{id}/init` | |
| **Revert** | `(ctx, id, SessionRevertParams) -> (...)` | `POST /session/{id}/revert` | |
| **Shell** | `(ctx, id, SessionShellParams) -> (...)` | `POST /session/{id}/shell` | |
| **Summarize** | `(ctx, id, SessionSummarizeParams) -> (...)` | `POST /session/{id}/summarize` | |
| **Share/Unshare** | | | |
| **Permissions.Respond** | Sub-service | | |

### 1.3 File Service

| Method | Signature | HTTP |
|---|---|---|
| **List** | `(ctx, FileListParams) -> (*[]FileNode, error)` | `GET /file` |
| **Read** | `(ctx, FileReadParams) -> (*FileReadResponse, error)` | `GET /file/content` |
| **Status** | `(ctx, FileStatusParams) -> (*[]File, error)` | `GET /file/status` |

### 1.4 Find Service

| Method | Signature | HTTP |
|---|---|---|
| **Files** | `(ctx, FindFilesParams) -> (*[]string, error)` | `GET /find/file` |
| **Symbols** | `(ctx, FindSymbolsParams) -> (*[]Symbol, error)` | `GET /find/symbol` |
| **Text** | `(ctx, FindTextParams) -> (*[]FindTextResponse, error)` | `GET /find` |

### 1.5 Event Service

| Method | Signature | HTTP |
|---|---|---|
| **ListStreaming** | `(ctx, EventListParams) -> *ssestream.Stream[EventListResponse]` | `GET /event` |

> **Discrepancy:** The `api.md` file lists this as `List`, but the actual Go method name is **`ListStreaming`** returning `*ssestream.Stream[EventListResponse]`.

### 1.6 Project Service

| Method | Signature | HTTP |
|---|---|---|
| **Current** | `(ctx, ProjectCurrentParams) -> (*Project, error)` | `GET /project/current` |
| **List** | `(ctx, ProjectListParams) -> (*[]Project, error)` | `GET /project` |

### 1.7 Directory Parameter

**Confirmed across virtually all param structs.** The field type and tag:

```go
Directory param.Field[string] `query:"directory"`
```

Present on: `SessionListParams`, `SessionNewParams`, `SessionDeleteParams`, `SessionAbortParams`, `SessionGetParams`, `SessionMessagesParams`, `SessionPromptParams`, `FileListParams`, `FileReadParams`, `FileStatusParams`, `FindFilesParams`, `EventListParams`, `ProjectCurrentParams`, `ProjectListParams`, `PathGetParams`, `ConfigGetParams`, `AppProvidersParams`, `AgentListParams`, `CommandListParams`, and many more.

**Field type:** `param.Field[string]` from `github.com/sst/opencode-sdk-go/internal/param`.

### 1.8 Health Endpoint

Server exposes `GET /global/health` returning `{ healthy: true, version: string }`.

**Critical:** This endpoint is **not exposed in the Go SDK**. The SDK does not have a `Global` or `Health` service. To hit it, use `client.Get()` or a raw HTTP request.

Also available: `GET /global/event` for a global SSE event stream.

### 1.9 Authentication

- Set `OPENCODE_SERVER_PASSWORD` to protect the server with HTTP basic auth
- Default username: `opencode`
- Override with `OPENCODE_SERVER_USERNAME`
- Applies to both `opencode serve` and `opencode web`

**SDK gap:** The SDK reads `OPENCODE_BASE_URL` from the environment but does **not** automatically handle basic auth. Use `option.WithHeader("Authorization", ...)` or similar request options.

### 1.10 Server Mode

```bash
opencode serve [--port <number>] [--hostname <string>] [--cors <origin>]
```

| Flag | Default | Description |
|---|---|---|
| `--port` | 4096 | Port to listen on |
| `--hostname` | 127.0.0.1 | Hostname to listen on |
| `--mdns` | false | Enable mDNS discovery |
| `--mdns-domain` | opencode.local | Custom mDNS domain |
| `--cors` | [] | Additional browser origins |

Config file (`opencode.json`):
```json
{
  "server": {
    "port": 4096,
    "hostname": "0.0.0.0",
    "mdns": true,
    "cors": ["http://localhost:5173"]
  }
}
```

OpenAPI spec viewable at `http://<hostname>:<port>/doc`.

### 1.11 The `opencode.F()` Helper

```go
func F[T any](value T) param.Field[T]
```

Additional convenience helpers: `String()`, `Int()`, `Float()`, `Bool()`, `Null[T]()`, `Raw[T]()`, `FileParam()`.

Usage: `opencode.F("some value")` wraps any value into a `param.Field[T]`.

### 1.12 Permission / Yolo Mode

Correct config format:

```json
{
  "permission": {
    "edit": "allow",
    "bash": "allow",
    "webfetch": "allow"
  }
}
```

Valid values: `"allow"`, `"ask"`, `"deny"`.

Shorthand for allow-all: `"permission": "allow"` or `"permission": { "*": "allow" }`.

**Available permission keys:** `read`, `edit`, `glob`, `grep`, `list`, `bash`, `task`, `skill`, `lsp`, `todoread`, `todowrite`, `webfetch`, `websearch`, `codesearch`, `external_directory`, `doom_loop`.

**Defaults:** Most permissions default to `"allow"`. Exceptions: `doom_loop` and `external_directory` default to `"ask"`. `.env` files in `read` default to `"deny"`.

Granular glob-pattern rules are supported:
```json
{
  "permission": {
    "bash": {
      "*": "ask",
      "git *": "allow",
      "rm *": "deny"
    }
  }
}
```

### 1.13 Key Discrepancies from Design Assumptions

| Assumption | Reality |
|---|---|
| `Event.List` method name | Actual name is `ListStreaming`, returns SSE stream |
| `/global/health` in SDK | **Not in the SDK** — server-only, use raw HTTP |
| SDK handles basic auth | **No** — must set manually via request options |
| Permission `"allow"` only | Values are `"allow"`, `"ask"`, `"deny"` |

---

## 2. Azure DevOps REST API

**API Version:** `api-version=7.1`

### 2.1 Operations Quick Reference

| Operation | Method | Endpoint |
|---|---|---|
| Get PR details | GET | `/{org}/{project}/_apis/git/repositories/{repo}/pullRequests/{prId}` |
| List PR threads | GET | `...pullRequests/{prId}/threads` |
| Create thread | POST | `...pullRequests/{prId}/threads` |
| Reply to thread | POST | `...pullRequests/{prId}/threads/{threadId}/comments` |
| Update thread status | PATCH | `...pullRequests/{prId}/threads/{threadId}` |
| Get pipeline status | GET | `/{org}/{project}/_apis/build/builds?branchName=refs/pull/{prId}/merge` |
| Get build timeline | GET | `/{org}/{project}/_apis/build/builds/{buildId}/timeline` |
| Get build log | GET | `/{org}/{project}/_apis/build/builds/{buildId}/logs/{logId}` |
| Create work item | POST | `/{org}/{project}/_apis/wit/workitems/$Task` |
| Set auto-complete | PATCH | `...pullRequests/{prId}` with `autoCompleteSetBy` |

### 2.2 Authentication

Use Entra ID (Azure AD) tokens on enrolled Microsoft devices:

```bash
az account get-access-token --resource 499b84ac-1321-427f-aa17-267ca6975798
```

- Resource ID `499b84ac-1321-427f-aa17-267ca6975798` is the Azure DevOps resource
- Tokens expire in **1 hour**
- Works in WSL with Windows Entra enrollment
- Alternative: PAT (Personal Access Token) via `Authorization: Basic base64(:pat)`

### 2.3 Comment Threading Model

ADO uses a **thread-based** model:

- Comments live inside threads (`GET .../threads` → each thread has a `comments[]` array)
- Resolution is **thread-level only** — set via `PATCH .../threads/{threadId}` with `{ "status": "fixed" }`
- Thread statuses: `active`, `fixed`, `wontFix`, `closed`, `byDesign`, `pending`
- Inline comments use `threadContext` with `rightFileStart`/`rightFileEnd` (line numbers and character offsets)
- `filePath` in `threadContext` **must start with `/`**

### 2.4 az CLI Gaps

The `az devops` CLI does **not** support:
- PR threads/comments
- Build logs
- PR statuses

All of these require **raw REST API calls**.

### 2.5 Rate Limits

- **200 TSTUs** (Token-Based Usage Units) per 5-minute sliding window
- Monitor via response headers: `X-RateLimit-Remaining`, `Retry-After`
- When limit is hit, API returns `429 Too Many Requests` with `Retry-After` header

### 2.6 Polling vs. Webhooks

**Polling is preferred** for a CLI tool — ADO webhooks require a public HTTP endpoint, which is impractical for a local daemon. Poll at reasonable intervals (e.g., 30-60 seconds).

### 2.7 Work Items

- Content-Type **must be** `application/json-patch+json`
- Body is a JSON Patch array:
  ```json
  [
    { "op": "add", "path": "/fields/System.Title", "value": "Task title" },
    { "op": "add", "path": "/fields/System.Description", "value": "Description" }
  ]
  ```

---

## 3. GitHub PR API & go-github

### 3.1 Library Version

**go-github v60 is significantly outdated.** The current highest tagged version is **v82**. Target `github.com/google/go-github/v68` or later (v68+ added `subject_type` for file-level comments). For maximum coverage, use **v82**.

Import path: `github.com/google/go-github/v82/github`

### 3.2 Operations Quick Reference

| Operation | Service | Method |
|---|---|---|
| Get PR | `PullRequests` | `Get(ctx, owner, repo, number)` |
| List PR files | `PullRequests` | `ListFiles(ctx, owner, repo, number, opts)` |
| Get raw diff | `PullRequests` | `GetRaw(ctx, owner, repo, number, opts)` with `Accept: application/vnd.github.v3.diff` |
| List check runs | `Checks` | `ListCheckRunsForRef(ctx, owner, repo, ref, opts)` |
| Combined commit status | `Repositories` | `GetCombinedStatus(ctx, owner, repo, ref, opts)` |
| List workflow runs | `Actions` | `ListRepositoryWorkflowRuns(ctx, owner, repo, opts)` |
| Download run logs | `Actions` | `GetWorkflowRunLogs(ctx, owner, repo, runID, maxRedirects)` |
| Download job logs | N/A (use HTTP) | `GET /repos/{owner}/{repo}/actions/jobs/{job_id}/logs` |
| Post general comment | `Issues` | `CreateComment(ctx, owner, repo, number, comment)` |
| Post inline comment | `PullRequests` | `CreateComment(ctx, owner, repo, number, comment)` |
| Reply to thread | `PullRequests` | `CreateCommentInReplyTo(ctx, owner, repo, number, body, inReplyTo)` |
| Create review (batch) | `PullRequests` | `CreateReview(ctx, owner, repo, number, review)` |
| Submit pending review | `PullRequests` | `SubmitReview(ctx, owner, repo, number, reviewID, review)` |
| Dismiss review | `PullRequests` | `DismissReview(ctx, owner, repo, number, reviewID, dismissal)` |
| Resolve thread | **GraphQL only** | `resolveReviewThread(input: {threadId: "..."})` |

### 3.3 Comment Threading Model

GitHub has **three distinct comment types** on PRs:

#### Issue Comments (`IssueComment`)
- General conversation in the PR timeline
- NOT tied to any file or line
- Managed via `Issues.CreateComment` / `Issues.ListComments`
- Endpoint: `/repos/{owner}/{repo}/issues/{number}/comments`

#### Review Comments (`PullRequestComment`)
- Tied to a specific file, line, and commit in the diff
- Always belong to a review (even standalone ones create a single-comment review)
- Have `in_reply_to_id` field for threading
- Managed via `PullRequests.CreateComment` / `PullRequests.ListComments`

#### Reviews (`PullRequestReview`)
- A container grouping review comments with an overall verdict
- States: `PENDING`, `COMMENTED`, `APPROVED`, `CHANGES_REQUESTED`, `DISMISSED`

```
PR
├── Issue Comments (flat list, no threading)
├── Review 1 (APPROVED)
│   ├── Review Comment A (file.go:10)
│   │   ├── Reply to A
│   │   └── Reply to A
│   └── Review Comment B (main.go:25)
├── Review 2 (CHANGES_REQUESTED)
│   └── Review Comment C (file.go:15)
└── Standalone Review Comment D (creates implicit single-comment review)
```

### 3.4 Inline Comment Parameters

Key fields on `PullRequestComment`:
- `Body` (required): Comment text
- `CommitID` (required): **Must be the PR head SHA**, not merge commit
- `Path` (required): Relative file path
- `Line` (required unless `SubjectType: "file"`): Line number in final version
- `Side`: `"LEFT"` (deletions) or `"RIGHT"` (additions/unchanged)
- `StartLine` + `StartSide`: For multi-line comments (range from `StartLine` to `Line`)
- `SubjectType`: `"line"` (default) or `"file"` (file-level, no line needed)

> **Critical:** Always use `Line`/`Side`; the `Position` field (diff offset) is deprecated.

### 3.5 Batch Comments with Reviews (Preferred Approach)

Create a review with inline comments atomically:
```go
client.PullRequests.CreateReview(ctx, owner, repo, prNumber, &github.PullRequestReviewRequest{
    CommitID: github.String(headSHA),
    Event:    github.String("COMMENT"),
    Body:     github.String("Overall review body"),
    Comments: []*github.DraftReviewComment{
        {Path: github.String("file.go"), Line: github.Int(10), Body: github.String("Fix this")},
    },
})
```

Two-phase PENDING workflow:
1. Create with no `Event` (stays PENDING, invisible to others)
2. Add more comments if needed
3. Submit with `SubmitReview()` + `Event: "COMMENT"`

### 3.6 Thread Resolution — GraphQL Only

**There is NO REST API endpoint to resolve/unresolve individual review threads.** This requires GraphQL:

```graphql
mutation {
  resolveReviewThread(input: {threadId: "PRRT_..."}) {
    thread { isResolved }
  }
}
```

Use `shurcooL/githubv4` for this. go-github does not wrap GraphQL.

### 3.7 Actions Log Downloads

- `GetWorkflowRunLogs` returns a `*url.URL` — a redirect to a **ZIP file** containing all job logs
- Download URLs **expire after 1 minute**
- Each job/step has its own text file inside the ZIP
- Logs can be very large (hundreds of MB for complex workflows)
- Prefer per-job logs: `GET /repos/{owner}/{repo}/actions/jobs/{job_id}/logs` returns a single job's log (plain text)

### 3.8 CI Status — Query Both Systems

GitHub has two CI status systems:

| | Commit Statuses (legacy) | Check Runs (modern) |
|---|---|---|
| Created by | Any OAuth app, PAT, or bot | GitHub Apps only (for creating) |
| States | `error`, `failure`, `pending`, `success` | `queued`, `in_progress`, `completed` + conclusion |
| GitHub Actions | Does NOT use statuses | Creates check runs automatically |

**Always query both** to get the full CI picture.

### 3.9 Rate Limiting

| | Unauthenticated | Authenticated |
|---|---|---|
| Core API | 60 req/hour | 5,000 req/hour |
| Search API | 10 req/min | 30 req/min |
| GraphQL | N/A | 5,000 points/hour |

- Every `*github.Response` has a `Rate` field with `Limit`, `Remaining`, `Reset`
- Rate limit errors: `*github.RateLimitError` (primary), `*github.AbuseRateLimitError` (secondary)
- Recommended retry library: `github.com/gofri/go-github-ratelimit`
- **Secondary rate limits** apply to content creation — batch comments into reviews instead of individual posts

### 3.10 Authentication

```go
client := github.NewClient(nil).WithAuthToken(os.Getenv("GITHUB_TOKEN"))
```

| Token Type | Scope Needed | Use Case |
|---|---|---|
| Fine-grained PAT | `pull_requests:rw`, `checks:r`, `statuses:r`, `actions:r` | Recommended |
| Classic PAT | `repo` | Overly broad but simple |
| `GITHUB_TOKEN` (Actions) | Set in workflow YAML | CI bots |
| GitHub App | `pull_requests:write`, `checks:read`, `actions:read` | Production bots |

For GitHub App auth, use `ghinstallation` package:
```go
itr, _ := ghinstallation.NewKeyFromFile(http.DefaultTransport, appID, installID, "key.pem")
client := github.NewClient(&http.Client{Transport: itr})
```

### 3.11 Key Gotchas Summary

1. **Version:** Use go-github **v82** (or at minimum v68+), not v60
2. **Line vs Position:** Always use `Line`/`Side`; `Position` is deprecated
3. **CommitID:** Must be the PR head SHA, not merge commit — wrong SHA gives 422
4. **Issue vs PR comments:** General comments use `Issues.CreateComment`, not `PullRequests`
5. **Thread resolution:** REST API cannot resolve threads; requires GraphQL `resolveReviewThread`
6. **Replies:** Only reply to the **root** comment ID; nested replies not supported
7. **Logs are ZIPs:** Actions log downloads return a ZIP, 1-min expiry URL
8. **Check runs vs statuses:** Both must be queried for full CI status picture
9. **Batch comments:** Use `CreateReview` with inline comments to avoid notification spam + secondary rate limits
10. **Secondary rate limits:** Add delays or use `go-github-ratelimit` for content-creation endpoints

---

## 4. Go Process Daemonization

### 4.1 Core Mechanism

Use `os/exec.Cmd` with `syscall.SysProcAttr{Setsid: true}` to launch a child in a new session, detached from the controlling terminal.

**Key `SysProcAttr` fields (Linux):**

| Field | Type | Purpose |
|---|---|---|
| `Setsid` | `bool` | Creates new session via `setsid(2)`. Child becomes session leader with no controlling terminal. |
| `Setpgid` | `bool` | Sets process group ID |
| `Pdeathsig` | `Signal` | Signal sent to child when parent dies. **Unreliable in Go** (fires on OS thread death, not process death — Go issue #27505). |
| `Noctty` | `bool` | Detach fd 0 from controlling terminal |

**macOS support:** `Setsid`, `Setpgid`, `Setctty`, `Noctty`, `Foreground` all available. `Pdeathsig` and `Cloneflags` are Linux-only.

### 4.2 Recommended Implementation Pattern

```go
cmd := exec.Command(os.Args[0], "server", "run")
cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

// Redirect stdout/stderr to log file
logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
cmd.Stdout = logFile
cmd.Stderr = logFile

// Detach stdin
cmd.Stdin = nil

cmd.Start()

// Write PID file
os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)

// Release — parent does NOT call cmd.Wait()
cmd.Process.Release()
```

### 4.3 Gotchas & Best Practices

1. **No double-fork needed.** `Setsid: true` is sufficient. The classic Unix double-fork pattern is unnecessary when using `SysProcAttr.Setsid`.

2. **Don't call `cmd.Wait()` in the parent.** Use `cmd.Process.Release()` to release OS resources without blocking.

3. **`Pdeathsig` is unreliable in Go.** Due to Go's threading model, it fires when the OS thread that called `fork` exits, not when the Go process exits. Don't rely on it. Instead detect parent death by polling `os.Getppid()` (becomes 1 when reparented to init) or use a heartbeat.

4. **PID file management:**
   - Write atomically (temp file → rename)
   - Detect stale PIDs: `syscall.Kill(pid, 0)` returns `nil` if alive, `ESRCH` if dead
   - Place in `$XDG_RUNTIME_DIR` or `~/.otto/`

5. **systemd interaction:** If running under systemd `Type=simple`, do NOT daemonize. Detect via `$INVOCATION_ID` or `$NOTIFY_SOCKET`.

6. **Signal handling in the daemon:** Install handlers for `SIGTERM` and `SIGINT`:
   ```go
   signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
   ```

7. **Cross-platform:** Works on Linux and macOS. **Not applicable to Windows** — Windows has no `setsid`. Use Windows services or `CREATE_NEW_PROCESS_GROUP` for Windows.

8. **Log rotation:** Consider `lumberjack` or reopening log files on `SIGHUP`.

### 4.4 Library Recommendation

**No third-party library needed.** Stdlib `os/exec` + `syscall.SysProcAttr` is the right approach. Avoid stale libraries like `go-daemon` or `godaemon`. The pattern is ~30 lines.

---

## 5. File Locking (flock)

### 5.1 Library Comparison

| Library | Thread-safe | Cross-platform | Context support | Dependents |
|---|---|---|---|---|
| **`github.com/gofrs/flock`** (v0.13.0) | Yes (internal RWMutex) | Linux, macOS, Windows | Yes (`TryLockContext`) | ~27,000 |
| `syscall.Flock()` | No | Linux + macOS only | No | stdlib |
| `syscall.FcntlFlock()` | No | Linux + macOS | No | stdlib |

### 5.2 Recommendation: `github.com/gofrs/flock`

BSD-3-Clause license, 704 stars, extremely widely used.

**API:**

| Method | Behavior |
|---|---|
| `flock.New(path)` | Creates a handle (does NOT acquire lock) |
| `Lock()` | Blocking exclusive lock |
| `TryLock()` | Non-blocking exclusive, returns `(bool, error)` |
| `TryLockContext(ctx, retryDelay)` | Retries with context cancellation |
| `RLock()` | Blocking shared/read lock |
| `TryRLock()` | Non-blocking shared/read lock |
| `Unlock()` | Release lock |
| `Locked()` / `RLocked()` | Query current state |

### 5.3 Gotchas

1. **Advisory locks only.** All participants must cooperate by acquiring the lock before access. No enforcement.

2. **Use a separate `.lock` file.** Don't lock the data file itself. Use `tasks.md.lock` alongside `tasks.md` to avoid issues with editors that delete-and-recreate files.

3. **flock is per-fd, not per-goroutine.** `gofrs/flock` handles this correctly with an internal `sync.RWMutex`, making it goroutine-safe within a single process.

4. **Crash safety.** Kernel automatically releases `flock` locks on fd close or process exit (including `kill -9`). No stale lock cleanup needed.

5. **NFS warning.** `flock(2)` does not work reliably on NFS (older kernels < 2.6.12). Use `fcntl(F_SETLK)` for NFS. Local filesystems are fine.

6. **macOS shared-to-exclusive upgrade.** On some BSD/macOS systems, upgrading from shared → exclusive on the same fd may behave unexpectedly. `Unlock()` then `Lock()` instead.

### 5.4 Recommended Pattern for Otto

```go
fileLock := flock.New("/path/to/tasks.md.lock")
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
if err != nil || !locked {
    return fmt.Errorf("could not acquire lock: %w", err)
}
defer fileLock.Unlock()

// ... safely read/write tasks.md ...
```

---

## 6. JSONC Deep Merge

### 6.1 JSONC Parsing — `github.com/tidwall/jsonc`

Minimalist library (88 lines, MIT). Two functions:

| Function | Purpose |
|---|---|
| `jsonc.ToJSON(src []byte) []byte` | Strips `//`, `/* */` comments and trailing commas. Returns valid JSON of same byte length. |
| `jsonc.ToJSONInPlace(src []byte) []byte` | Same but reuses input buffer (zero allocation). |

**That's all it does.** No parsing, no AST. Just a preprocessor. Feature-complete, no bugs to fix (last updated 5 years ago). Does NOT handle JSON5 features.

### 6.2 Deep Merge Options

#### Option A: `github.com/imdario/mergo` (v0.3.16) — Recommended

Mature struct/map merge library. Used by Docker, Kubernetes, Grafana. MIT.

```go
mergo.Merge(&dst, src)                          // fill missing fields only
mergo.Merge(&dst, src, mergo.WithOverride)      // source wins
```

Relevant options:

| Option | Behavior |
|---|---|
| `WithOverride` | Source values override destination |
| `WithAppendSlice` | Append slices instead of replace |
| `WithSliceDeepCopy` | Deep-copy slices element by element |
| `WithOverwriteWithEmptyValue` | Zero-values override non-zero |
| `WithTransformers` | Custom type transformers |

**Gotcha:** Structs inside maps are NOT deep-merged (values in maps are not addressable via reflection). When using `map[string]any`, nested maps ARE recursively merged (they're maps, not structs).

**Config merge pattern:**
```go
// 1. Parse JSONC
defaultBytes := jsonc.ToJSON(defaultJSONC)
userBytes := jsonc.ToJSON(userJSONC)

// 2. Unmarshal
var defaults, user map[string]any
json.Unmarshal(defaultBytes, &defaults)
json.Unmarshal(userBytes, &user)

// 3. Deep merge: user overrides defaults
mergo.Merge(&defaults, user, mergo.WithOverride)
```

#### Option B: `github.com/knadh/koanf` (v2)

Full config management framework (lighter Viper alternative). Built-in `Merge()`, `MergeAt()`, file watching, env var overrides. No built-in JSONC parser, but trivially wired: file → `jsonc.ToJSON()` → koanf's JSON parser via `rawbytes` provider.

**Use koanf if** otto needs full config management (multiple files, env vars, watch-for-changes, typed access). For one-shot deep merge, mergo is simpler.

#### Option C: Manual recursive merge (~20 lines)

```go
func deepMerge(dst, src map[string]any) {
    for key, srcVal := range src {
        dstVal, exists := dst[key]
        if !exists {
            dst[key] = srcVal
            continue
        }
        srcMap, srcOK := srcVal.(map[string]any)
        dstMap, dstOK := dstVal.(map[string]any)
        if srcOK && dstOK {
            deepMerge(dstMap, srcMap)
        } else {
            dst[key] = srcVal
        }
    }
}
```

Zero dependencies, full control over array semantics, but you own edge cases.

### 6.3 Recommendation

**`tidwall/jsonc` + `imdario/mergo`** for simplest approach. Migrate to **koanf** later if env-var overrides or file watching are needed.

---

## 7. Teams Notification APIs

### 7.1 Options Comparison

| Approach | DM Support | Daemon Auth | Setup Complexity | Deprecation Risk |
|---|---|---|---|---|
| Incoming Webhooks | **No** (channel-only) | URL-based | Low | **High** (retiring Apr 2026) |
| Graph API (chat) | Technically yes | **No** (app perms = migration only) | Medium | Low |
| Graph API (Activity Feed) | Activity feed only | Yes (`TeamsActivity.Send`) | Medium-High | Low |
| **Power Automate** | **Yes** | URL-based | **Low** | Low |
| **Bot Framework** | **Yes** | Yes (client credentials) | **High** | Low |
| O365 Connectors | No | N/A | Low | **Dead** (retiring Apr 2026) |

### 7.2 Recommendation: Power Automate Workflow (Simplest Path)

1. Create a Power Automate flow: trigger = "When a Teams webhook request is received", action = "Post message in a chat" targeting a specific user
2. Copy the webhook URL
3. From the Go daemon, `POST` JSON to that URL
4. User receives a DM from the Flow bot

**Pros:**
- 15-minute setup, no code on the Teams side
- Works as a DM to a specific user
- No Azure AD app registration needed
- No premium license (standard M365 E3/E5 included)
- Supports Adaptive Cards for rich formatting

**Cons:**
- Flow is tied to a user account (add co-owners for resilience)
- Webhook URL is unauthenticated (treat as a secret in config)
- Messages come from "Workflows" / Flow bot, not a custom-branded bot
- Less programmatic control than Bot Framework

**Payload format:**
```json
{
  "type": "message",
  "attachments": [{
    "contentType": "application/vnd.microsoft.card.adaptive",
    "content": { /* Adaptive Card JSON */ }
  }]
}
```

### 7.3 Alternative: Bot Framework (Most Robust)

For branded, multi-user, production-grade notifications. Requires:
- Azure AD app + Azure Bot Service registration
- Teams app manifest → org app catalog
- Install app per user (can automate via Graph `TeamsAppInstallation.ReadWriteSelfForUser.All`)
- Store `conversationReference` from install event
- Send proactive messages via Bot Framework REST API

**Go support:** No official SDK, but the REST API is straightforward:
1. Get token from `https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token`
2. Create conversation: `POST {serviceUrl}/v3/conversations`
3. Send activity: `POST {serviceUrl}/v3/conversations/{conversationId}/activities`

### 7.4 Why Other Options Don't Work

- **Incoming Webhooks:** Channel-only (no DM), being deprecated April 2026
- **Graph API Chat Messages:** Application permissions (`Teamwork.Migrate.All`) are migration-only. Cannot send DMs from a daemon without user-interactive auth.
- **O365 Connectors:** Retired. Creation blocked since August 2024.

---

## 8. Charmbracelet Libraries

### 8.1 `charmbracelet/huh` — Interactive Forms & Prompts

**Version:** v0.8.0 | **License:** MIT | **Importers:** 980

#### Multi-Select API (for PR review comment approval)

```go
var selected []string

huh.NewMultiSelect[string]().
    Title("Comments to post").
    Options(
        huh.NewOption("Fix null check in auth.go:42", "comment-1").Selected(true),
        huh.NewOption("Add error handling in handler.go:15", "comment-2").Selected(true),
        huh.NewOption("Consider using sync.Pool", "comment-3"),
    ).
    Limit(10).
    Value(&selected).
    Run()
```

- `.Selected(true)` on individual options for pre-selection
- `.Filterable(bool)` for type-to-filter
- `.Validate(func([]T) error)` for custom validation
- `MultiSelectKeyMap`: `Toggle` (space), `Submit` (enter), `SelectAll`, `SelectNone`

#### Standalone vs. Form Usage
Each field has `.Run()` for standalone use. Fields can also compose into multi-group forms via `huh.NewForm(huh.NewGroup(...))`.

#### Bubble Tea Integration
`huh.Form` implements `tea.Model` — embeddable in Bubble Tea apps if needed.

#### Accessibility
`form.WithAccessible(true)` or `RunAccessible()` for screen reader support.

#### Alternatives
- **Survey** (`AlecAivazis/survey`): **Archived/unmaintained.** huh is its spiritual successor.
- **promptui** (`manifoldco/promptui`): Simpler, less flexible, no generics, not Bubble Tea compatible.

### 8.2 `charmbracelet/log` — Structured Logging

**Version:** v0.4.2 | **License:** MIT | **Importers:** 2,861

#### Log Levels
`DebugLevel` (-4), `InfoLevel` (0), `WarnLevel` (4), `ErrorLevel` (8), `FatalLevel` (12).

#### Structured Key-Value Logging
```go
log.Info("order placed", "id", orderID, "total", total)
logger := log.With("component", "auth")
logger.Info("started")
```

#### Formatters
`TextFormatter` (colored, default), `JSONFormatter`, `LogfmtFormatter`.

#### slog.Handler Implementation (Critical Finding)

Since v0.3.0, `charmbracelet/log` **implements `slog.Handler`**:

```go
handler := log.Default()
slog.SetDefault(slog.New(handler))

// Now slog calls render through charmbracelet/log:
slog.Info("server started", "port", 4097)
```

**Recommended approach for otto:**
- Use `log/slog` as the logging API throughout the codebase (standard, no vendor lock-in)
- Use `charmbracelet/log` as the `slog.Handler` backend for pretty terminal output
- For file/daemon logging, swap to `slog.NewJSONHandler()` or `JSONFormatter`

#### Standard Library Adapter
`.StandardLog()` returns `*log.Logger` for libraries expecting the standard logger.

### 8.3 `charmbracelet/lipgloss` — CLI Styling

**Version:** v1.1.0 | **License:** MIT | **Importers:** 7,622

#### Table Rendering — Sub-Package, Not Separate Repo

**There is no `charmbracelet/table` package.** Tables are in `lipgloss/table`:

```go
import "github.com/charmbracelet/lipgloss/table"

t := table.New().
    Border(lipgloss.NormalBorder()).
    BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("99"))).
    StyleFunc(func(row, col int) lipgloss.Style {
        if row == table.HeaderRow {
            return headerStyle
        }
        return defaultStyle
    }).
    Headers("FILE", "LINE", "SEVERITY", "COMMENT").
    Rows(rows...)

fmt.Println(t)
```

Border styles: `NormalBorder()`, `RoundedBorder()`, `DoubleBorder()`, `BlockBorder()`, `ThickBorder()`, `HiddenBorder()`, `ASCIIBorder()`, `MarkdownBorder()`.

Other sub-packages: `lipgloss/list` (styled lists), `lipgloss/tree` (tree rendering).

#### lipgloss v2 Warning

v2.0.0-beta1 exists with **breaking changes**:
- Color type changes from `TermColor` to `color.Color` (stdlib `image/color`)
- `AdaptiveColor` replaced by `LightDark()` function
- `Copy()` deprecated (assignment does true copy)
- New output functions with auto color downsampling

**Recommendation:** Stay on **lipgloss v1.1.0**. v2 is beta with significant breaking changes.

#### Cross-Package Compatibility
All three packages are designed together:
- huh uses lipgloss for theming
- log uses lipgloss for styling
- lipgloss/table uses lipgloss styles natively
- All share the same lipgloss v1 dependency

### 8.4 Version Summary for Otto

| Library | Version | Use Case | Import |
|---|---|---|---|
| huh | v0.8.0 | PR review comment multi-select approval | `github.com/charmbracelet/huh` |
| log | v0.4.2 | Structured logging (slog backend) | `github.com/charmbracelet/log` |
| lipgloss | v1.1.0 | CLI styling + table rendering | `github.com/charmbracelet/lipgloss` |
| lipgloss/table | (part of lipgloss) | Progress tables in spec execute | `github.com/charmbracelet/lipgloss/table` |

---

## 9. MerlinBot & Build Log Analysis

### 9.1 MerlinBot in Azure DevOps

MerlinBot is a **Microsoft-internal automated code review bot** within the **1ES (One Engineering System)** infrastructure. Not public/open-source — docs live on `eng.ms` (internal only, 403 externally).

#### What MerlinBot checks:
- Credential/secret detection
- Security policy violations
- Compliance checks
- Build/pipeline policy enforcement

#### Detecting MerlinBot Comments

MerlinBot comments come through the standard PR threads API. Detection heuristics:

1. **Author identity** (most reliable): `comments[0].author.displayName` or `.uniqueName` containing "MerlinBot" / "merlinbot@microsoft.com"
2. **Thread properties:** `thread.properties` may contain `"CodeReviewThreadType": "MerlinBot"` or similar
3. **Non-human identity type:** Bot accounts may have `author.descriptor` prefix `svc.` (vs `aad.` for AAD users)

**Recommendation:** Use a configurable list of bot display names/unique names in otto config. The existing `"merlinbot": true` flag should trigger filtering by author identity.

#### Responding to MerlinBot

- **Reply:** `POST .../threads/{threadId}/comments` with `parentCommentId`
- **Resolve:** `PATCH .../threads/{threadId}` with `{ "status": "fixed" }`
- Thread statuses: `active`, `fixed`, `wontFix`, `closed`, `byDesign`, `pending`

**Caveat:** Some MerlinBot checks are policy-blocking. Resolving via API may not suffice — MerlinBot re-scans and may reopen threads if the underlying issue persists.

### 9.2 Build Log Analysis Pipeline

#### ADO Build Timeline (Most Useful Endpoint)

```
GET /{org}/{project}/_apis/build/builds/{buildId}/timeline?api-version=7.0
```

Response contains hierarchical records (Stage → Job → Task) with:
- `result`: `failed|succeeded|canceled|skipped`
- `log.id`: Reference to downloadable log
- `issues[]`: Array with error messages, categories, and line numbers
- `errorCount`, `warningCount`

**Strategy:** Use timeline to find failed `Task` records, then fetch only those specific logs. The `issues` array often contains error messages directly, potentially avoiding raw log downloads.

#### ADO Log Range Retrieval

```
GET /{org}/{project}/_apis/build/builds/{buildId}/logs/{logId}?api-version=7.0&startLine=X&endLine=Y
```

Supports `startLine`/`endLine` query parameters for partial retrieval.

#### GitHub Actions Job-Level Logs

Prefer per-job logs over the full ZIP:
```
GET /repos/{owner}/{repo}/actions/jobs/{job_id}/logs
```

Returns 302 redirect to plain text log (1-min expiry URL). Find failed jobs first:
```
GET /repos/{owner}/{repo}/actions/runs/{run_id}/jobs
```

### 9.3 Log Truncation Strategies for LLM Context

#### Phase 1: Error Extraction (Before LLM)

1. **Use structured metadata first:** ADO timeline `issues[]` and GitHub `jobs[].steps[].conclusion` provide error info without downloading raw logs.

2. **Target failed tasks only:** Download logs only for failed steps/tasks.

3. **Tail-biased truncation:** Errors appear at the end. When logs are too large, take last 500-1000 lines plus first 20-50 lines (environment info).

4. **Error-line anchoring:** Scan for patterns and extract context windows:
   - Patterns: `error:`, `FAILED`, `fatal:`, `panic:`, `FAIL`, `exit code`, `##[error]` (ADO), `Process completed with exit code`
   - Extract ~20 lines before and ~10 lines after each match
   - Deduplicate overlapping windows

5. **Section-based truncation:** ADO uses `##[group]`/`##[endgroup]`, GitHub uses `::group::`/`::endgroup::`. Keep only sections containing errors.

#### Phase 2: LLM Token Budget

- **Target:** 8K-16K tokens (~24K-48K characters) for log content, leaving room for diff + file contents + instructions
- **Priority:** Error messages > stack traces > surrounding context > build configuration
- If multiple jobs failed, prioritize the first failure (subsequent are often cascading)

#### Size Estimates

| Scenario | Raw Log Size | After Extraction |
|---|---|---|
| Simple Go test failure | 5-20 KB | 1-3 KB |
| Complex build failure | 50-500 KB | 3-10 KB |
| Multi-job pipeline | 1-10 MB total | 5-20 KB (failed jobs only) |
| Massive pipeline (monorepo) | 50+ MB total | 10-30 KB (targeted) |

### 9.4 ANSI Stripping

CI logs contain heavy ANSI escape codes. Recommended library:

**`github.com/charmbracelet/x/ansi`** — Parser-based stripping (handles all escape sequence types), from the Charm ecosystem otto already uses.

```go
import "github.com/charmbracelet/x/ansi"

clean := ansi.Strip(rawLogLine)
truncated := ansi.Truncate(rawLogLine, 200, "...")
```

Also provides ANSI-aware `Truncate`, `Wordwrap`, `Hardwrap` — useful for log formatting before LLM input.

Alternative: `github.com/acarl005/stripansi` — simpler regex-based, one function, 99% accuracy.

### 9.5 End-to-End Pipeline

```
1. Get build status from PR (check run / build policy)
       ↓
2. Fetch timeline (ADO) or jobs list (GitHub)
       ↓
3. Identify failed tasks/steps from structured metadata
       ↓
4. Extract error messages from metadata (issues/conclusion)
   — Often sufficient without downloading raw logs
       ↓
5. If more context needed: download raw log for failed task only
       ↓
6. Strip ANSI codes (charmbracelet/x/ansi)
       ↓
7. Apply error-anchored truncation (grep for patterns,
   extract windows, keep tail)
       ↓
8. Phase 1 LLM: "What files/lines likely caused this failure?"
       ↓
9. Phase 2 LLM: Generate fix with targeted file context
```

---

## Appendix: Dependency Summary

| Dependency | Version | Purpose | License |
|---|---|---|---|
| `github.com/sst/opencode-sdk-go` | v0.19.2+ | OpenCode API client | MIT |
| `github.com/google/go-github/v82` | v82 | GitHub API client | BSD-3 |
| `github.com/shurcooL/githubv4` | latest | GitHub GraphQL (thread resolution) | MIT |
| `github.com/gofri/go-github-ratelimit` | latest | GitHub rate limit retry | MIT |
| `github.com/gofrs/flock` | v0.13.0 | File locking | BSD-3 |
| `github.com/tidwall/jsonc` | latest | JSONC → JSON preprocessing | MIT |
| `github.com/imdario/mergo` | v0.3.16 | Deep merge for config | BSD-3 |
| `github.com/charmbracelet/huh` | v0.8.0 | Interactive terminal forms | MIT |
| `github.com/charmbracelet/log` | v0.4.2 | Structured logging / slog backend | MIT |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | CLI styling + tables | MIT |
| `github.com/charmbracelet/x/ansi` | latest | ANSI stripping for CI logs | MIT |
| `github.com/spf13/cobra` | latest | CLI framework | Apache-2.0 |
