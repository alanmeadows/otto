# Azure DevOps REST API Research: PR Lifecycle Management

> Research compiled February 7, 2026. API version: **7.1** (latest stable as of this date; 7.2 is in preview).

## Table of Contents

- [Authentication](#authentication)
- [1. Get PR Details](#1-get-pr-details)
- [2. Get Pipeline/Build Status for a PR](#2-get-pipelinebuild-status-for-a-pr)
- [3. Get Build Logs](#3-get-build-logs)
- [4. Post General Comments on a PR](#4-post-general-comments-on-a-pr)
- [5. Post Inline/Thread Comments on Specific Files and Lines](#5-post-inlinethread-comments-on-specific-files-and-lines)
- [6. Reply to Existing Comment Threads](#6-reply-to-existing-comment-threads)
- [7. Resolve/Close Comment Threads](#7-resolveclose-comment-threads)
- [8. Set Auto-Complete on a PR](#8-set-auto-complete-on-a-pr)
- [9. Create Work Items](#9-create-work-items)
- [10. MerlinBot](#10-merlinbot)
- [ADO Comment/Thread Model Deep Dive](#ado-commentthread-model-deep-dive)
- [az devops CLI](#az-devops-cli)
- [Rate Limiting](#rate-limiting)
- [Polling vs Webhooks](#polling-vs-webhooks)

---

## Authentication

### Methods (in order of preference for this tool)

| Method | Header Format | Best For |
|--------|--------------|----------|
| **Microsoft Entra token via `az cli`** | `Authorization: Bearer <token>` | Microsoft employees on enrolled devices (WSL + Windows) |
| **Personal Access Token (PAT)** | `Authorization: Basic base64(:<PAT>)` | Quick scripting, local dev |
| **Microsoft Entra OAuth** | `Authorization: Bearer <token>` | Production apps, service principals |

### PAT Authentication (simplest for Go)

```
Authorization: Basic base64(":" + PAT)
```

The username is empty (or any string); only the PAT matters. Base64-encode `:<PAT>`.

PAT scopes needed for this tool:
- **Code** → Read, Write (for PR read + comments)
- **Build** → Read (for pipeline status + logs)
- **Work Items** → Read, Write (for creating work items)

### Entra Token via az CLI (recommended for Microsoft employees)

```bash
az account get-access-token \
  --resource 499b84ac-1321-427f-aa17-267ca6975798 \
  --query "accessToken" \
  -o tsv
```

Used as: `Authorization: Bearer <token>`

The resource ID `499b84ac-1321-427f-aa17-267ca6975798` is the Azure DevOps application ID — this is a constant.

**Key advantage on WSL + Windows with enrolled devices**: `az login` can leverage the Windows Web Account Manager (WAM) broker via `--allow-broker`. On an Entra-joined/enrolled corporate device, this enables:
- **SSO via device credentials** — no interactive browser auth needed after initial setup
- **Conditional Access compliance** — the device is already trusted, so MFA policies are satisfied
- **Token refresh** — tokens auto-refresh without re-auth
- **No PAT management** — no secrets to rotate/store

From WSL, `az login` delegates to the Windows-side broker. Run:
```bash
az login --allow-broker
az account set -s <subscription-id>
TOKEN=$(az account get-access-token --resource 499b84ac-1321-427f-aa17-267ca6975798 --query accessToken -o tsv)
```

Entra tokens expire after **1 hour** — the Go tool should cache and refresh them. In Go, shell out to `az account get-access-token` or use the Azure Identity SDK.

---

## Base URL Pattern

All REST API calls use:
```
https://dev.azure.com/{organization}/{project}/_apis/...?api-version=7.1
```

For Build APIs specifically:
```
https://dev.azure.com/{organization}/{project}/_apis/build/...?api-version=7.1
```

---

## 1. Get PR Details

### Endpoint

```
GET https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}?api-version=7.1
```

`repositoryId` can be the repo name (string) or GUID.

### Key Response Fields

```json
{
  "pullRequestId": 1234,
  "title": "Fix widget rendering",
  "description": "Markdown description here",
  "status": "active",           // "active" | "completed" | "abandoned"
  "sourceRefName": "refs/heads/feature/widget-fix",
  "targetRefName": "refs/heads/main",
  "mergeStatus": "succeeded",   // "succeeded" | "conflicts" | "failure" | "rejectedByPolicy" | "queued" | "notSet"
  "mergeId": "guid",
  "createdBy": {
    "displayName": "User Name",
    "id": "guid",
    "uniqueName": "user@org.com"
  },
  "creationDate": "2026-02-01T10:00:00Z",
  "repository": {
    "id": "repo-guid",
    "name": "my-repo",
    "url": "...",
    "project": {
      "id": "project-guid",
      "name": "MyProject"
    }
  },
  "reviewers": [
    {
      "id": "guid",
      "displayName": "Reviewer",
      "vote": 10,              // 10=approved, 5=approved-with-suggestions, 0=no-vote, -5=wait-for-author, -10=rejected
      "isRequired": true
    }
  ],
  "lastMergeSourceCommit": { "commitId": "sha" },
  "lastMergeTargetCommit": { "commitId": "sha" },
  "lastMergeCommit": { "commitId": "sha" },
  "autoCompleteSetBy": { ... },  // null if not set
  "completionOptions": { ... },  // merge strategy, delete source branch, etc.
  "labels": [ { "id": "guid", "name": "label-name" } ],
  "isDraft": false
}
```

### Gotchas
- `sourceRefName` and `targetRefName` include the `refs/heads/` prefix — strip it for display.
- `mergeStatus` can be `"queued"` while ADO is computing mergeability — poll until resolved.
- To get iterations (pushes), use a separate endpoint: `GET .../pullrequests/{id}/iterations`.

---

## 2. Get Pipeline/Build Status for a PR

There are two approaches:

### Approach A: PR Statuses (Policy-Based)

```
GET https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}/statuses?api-version=7.1
```

Returns statuses posted by build policies, pipelines, and external services:

```json
{
  "value": [
    {
      "id": 1,
      "state": "succeeded",        // "succeeded" | "failed" | "pending" | "error" | "notSet" | "notApplicable"
      "description": "Build succeeded",
      "context": {
        "name": "my-pipeline",
        "genre": "continuous-integration"
      },
      "targetUrl": "https://dev.azure.com/org/project/_build/results?buildId=5678",
      "creationDate": "2026-02-01T11:00:00Z"
    }
  ]
}
```

### Approach B: Query Builds Directly

```
GET https://dev.azure.com/{organization}/{project}/_apis/build/builds?api-version=7.1&branchName=refs/pull/{pullRequestId}/merge&reasonFilter=pullRequest
```

Or more precisely, get builds for the PR's source branch:

```
GET https://dev.azure.com/{organization}/{project}/_apis/build/builds?api-version=7.1&branchName=refs/pull/{pullRequestId}/merge&$top=10
```

### Key Build Response Fields

```json
{
  "value": [
    {
      "id": 5678,
      "buildNumber": "20260201.1",
      "status": "completed",       // "completed" | "inProgress" | "cancelling" | "postponed" | "notStarted" | "none"
      "result": "succeeded",       // "succeeded" | "partiallySucceeded" | "failed" | "canceled" | "none"
      "sourceBranch": "refs/pull/1234/merge",
      "sourceVersion": "commit-sha",
      "definition": {
        "id": 42,
        "name": "CI Pipeline"
      },
      "queueTime": "...",
      "startTime": "...",
      "finishTime": "...",
      "url": "...",
      "_links": {
        "web": { "href": "https://dev.azure.com/org/project/_build/results?buildId=5678" },
        "timeline": { "href": "..." }
      }
    }
  ]
}
```

### Approach C: Build Timeline (for stage/job/task-level status)

```
GET https://dev.azure.com/{organization}/{project}/_apis/build/builds/{buildId}/timeline?api-version=7.1
```

Returns a flat array of records with `type` = "Stage", "Job", "Task" and `state`/`result` for each.

### Gotchas
- PR builds use branch name `refs/pull/{pullRequestId}/merge` — this is the ADO-synthesized merge branch.
- Multiple pipelines may run on a single PR — filter by `definition.name` if needed.
- The statuses endpoint (Approach A) may show multiple entries per pipeline (one per iteration/push) — the latest is the most recent.

---

## 3. Get Build Logs

### List All Logs for a Build

```
GET https://dev.azure.com/{organization}/{project}/_apis/build/builds/{buildId}/logs?api-version=7.1
```

Returns:
```json
{
  "value": [
    {
      "id": 1,
      "type": "Container",
      "url": "https://dev.azure.com/org/project/_apis/build/builds/5678/logs/1",
      "lineCount": 42,
      "createdOn": "..."
    }
  ]
}
```

### Get a Specific Log

```
GET https://dev.azure.com/{organization}/{project}/_apis/build/builds/{buildId}/logs/{logId}?api-version=7.1
```

Returns **plain text** (the raw log output). Set `Accept: text/plain`.

You can add `&startLine=N&endLine=M` query params to fetch partial logs.

### Strategy for Failed Builds

1. Get the build timeline: `GET .../builds/{buildId}/timeline`
2. Find records where `result == "failed"` and `type == "Task"`
3. Use the `log.id` from the failed task record
4. Fetch that specific log: `GET .../builds/{buildId}/logs/{logId}`

### Gotchas
- Log IDs are per-build, not globally unique.
- Logs can be very large — use `startLine`/`endLine` to limit.
- The timeline gives you the *structure* (stages → jobs → tasks), and each task has a `log.id` reference.
- For YAML pipelines, the timeline `record.name` shows the task display name (e.g., "Run Tests").

---

## 4. Post General Comments on a PR

General (non-file-specific) comments are posted by creating a **thread** with `threadContext` = null.

### Endpoint

```
POST https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}/threads?api-version=7.1
```

### Request Body

```json
{
  "comments": [
    {
      "parentCommentId": 0,
      "content": "This is a general comment on the PR.\n\nMarkdown is supported.",
      "commentType": 1
    }
  ],
  "status": 1
}
```

### Comment Types
- `1` = text (normal comment — **use this**)
- `2` = codeChange
- `3` = system

### Thread Status Values
- `0` = unknown
- `1` = active
- `2` = fixed (resolved)
- `3` = won't fix
- `4` = closed
- `5` = byDesign
- `6` = pending

### Response

Returns the created thread with `id`, `comments[]` array (each comment gets an `id`), and `status`.

### Gotchas
- You must create a **thread** to post any comment — there are no standalone comments.
- The first comment in the thread has `parentCommentId: 0`.
- Markdown is fully supported in `content`.
- `commentType: 1` is the only type you should use for user-generated comments.

---

## 5. Post Inline/Thread Comments on Specific Files and Lines

Same endpoint as #4, but with a `threadContext` object:

### Endpoint

```
POST https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}/threads?api-version=7.1
```

### Request Body

```json
{
  "comments": [
    {
      "parentCommentId": 0,
      "content": "This line has a potential null reference issue.",
      "commentType": 1
    }
  ],
  "threadContext": {
    "filePath": "/src/widget.go",
    "rightFileStart": {
      "line": 42,
      "offset": 1
    },
    "rightFileEnd": {
      "line": 42,
      "offset": 1
    }
  },
  "status": 1
}
```

### Thread Context Model

```json
{
  "filePath": "/path/to/file.go",    // Must start with /
  "leftFileStart":  { "line": N, "offset": M },  // Left side = target/base (before change)
  "leftFileEnd":    { "line": N, "offset": M },
  "rightFileStart": { "line": N, "offset": M },  // Right side = source (the PR's changes)
  "rightFileEnd":   { "line": N, "offset": M }
}
```

**Key rules:**
- Use `rightFileStart`/`rightFileEnd` for comments on **added/modified lines** (the new code).
- Use `leftFileStart`/`leftFileEnd` for comments on **deleted/unchanged lines** (the old code).
- `offset` is the **1-based character position** within the line. Use `1` to highlight the whole line.
- `filePath` must start with `/`. It's relative to the repo root.
- To comment on a single line, set `start` and `end` to the same line number.
- To comment on a range, set different line numbers for `start` and `end`.
- The file path and line numbers must match what's in the PR diff — if the file wasn't changed, the comment won't anchor correctly.

### Gotchas
- `offset` is mandatory but often just set to `1`.
- If you specify both `left` and `right`, ADO tries to anchor to both sides of the diff.
- A file-level comment (no line) uses `threadContext` with only `filePath` (no line positions).
- Comments on lines outside the diff will show up but won't be anchored to a specific diff hunk — they'll appear as floating comments on the file.

---

## 6. Reply to Existing Comment Threads

### Endpoint

```
POST https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}/threads/{threadId}/comments?api-version=7.1
```

### Request Body

```json
{
  "parentCommentId": 1,
  "content": "Good point, I've fixed this in the latest push.",
  "commentType": 1
}
```

### Key Details
- `parentCommentId` should reference the comment you're replying to (the first comment in the thread is `id: 1`).
- Setting `parentCommentId: 0` makes it a new root comment in the thread (not a reply to a specific comment) — this is unusual but valid.
- In practice, for most threaded discussions, reply to comment `1` (the thread-starting comment).

### Getting Existing Threads

```
GET https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}/threads?api-version=7.1
```

Returns all threads with their comments. Each thread has:
- `id`: thread ID
- `status`: 1=active, 2=fixed, etc.
- `threadContext`: file/line info (null for general comments)
- `comments[]`: array of comments in the thread
- `properties`: arbitrary key-value pairs (used by bots like MerlinBot)
- `isDeleted`: whether the thread is deleted

---

## 7. Resolve/Close Comment Threads

### Endpoint

```
PATCH https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}/threads/{threadId}?api-version=7.1
```

### Request Body

```json
{
  "status": 2
}
```

### Status Values (repeated for reference)
| Value | Meaning |
|-------|---------|
| 1 | Active |
| 2 | Fixed (Resolved) |
| 3 | Won't Fix |
| 4 | Closed |
| 5 | By Design |
| 6 | Pending |

### Key Details
- **You resolve/close threads, not individual comments.** The thread is the atomic unit of resolution.
- Individual comments within a thread cannot have their own status — status is thread-level only.
- Any participant with appropriate permissions can change thread status.
- Changing status to `2` (Fixed) is the standard "resolve" action.
- You can also reply and resolve in one operation by POSTing a comment AND then PATCHing the thread status, or by posting the reply and including thread status update in the same call.

### Gotchas
- The `PATCH` only updates the fields you send — other thread properties are preserved.
- You can re-activate a resolved thread by setting status back to `1`.

---

## 8. Set Auto-Complete on a PR

### Endpoint

```
PATCH https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}?api-version=7.1
```

### Request Body

```json
{
  "autoCompleteSetBy": {
    "id": "<identity-id-of-user-setting-autocomplete>"
  },
  "completionOptions": {
    "mergeStrategy": "squash",
    "deleteSourceBranch": true,
    "transitionWorkItems": true,
    "mergeCommitMessage": "Merged PR 1234: Fix widget rendering"
  }
}
```

### Merge Strategy Values
- `"noFastForward"` — merge commit (default)
- `"squash"` — squash merge
- `"rebase"` — rebase
- `"rebaseMerge"` — rebase + merge commit

### To Get Current User's Identity ID

```
GET https://dev.azure.com/{organization}/_apis/connectiondata?api-version=7.1
```

Look for `authenticatedUser.id` in the response.

Or use:
```
GET https://dev.azure.com/{organization}/_apis/profile/profiles/me?api-version=7.1
```

### To Cancel Auto-Complete

```json
{
  "autoCompleteSetBy": {
    "id": "00000000-0000-0000-0000-000000000000"
  }
}
```

Setting `autoCompleteSetBy.id` to the nil GUID clears it.

### Gotchas
- Auto-complete fires when all required policies pass and all required reviewers approve.
- You need the **identity GUID** of the user setting auto-complete, not a username string.
- The `completionOptions` control what happens when the auto-complete fires.
- If auto-complete is already set, PATCHing with new `completionOptions` will update them.

---

## 9. Create Work Items

### Endpoint

```
POST https://dev.azure.com/{organization}/{project}/_apis/wit/workitems/${type}?api-version=7.1
```

Where `{type}` is the work item type: `Task`, `Bug`, `User Story`, `Feature`, etc. Note the `$` prefix.

Content-Type: **`application/json-patch+json`** (this is critical — not regular JSON).

### Request Body

```json
[
  {
    "op": "add",
    "path": "/fields/System.Title",
    "value": "Fix null reference in widget renderer"
  },
  {
    "op": "add",
    "path": "/fields/System.Description",
    "value": "<div>Found during PR review of PR #1234</div>"
  },
  {
    "op": "add",
    "path": "/fields/System.AreaPath",
    "value": "MyProject\\MyTeam"
  },
  {
    "op": "add",
    "path": "/fields/System.IterationPath",
    "value": "MyProject\\Sprint 42"
  },
  {
    "op": "add",
    "path": "/fields/System.AssignedTo",
    "value": "user@org.com"
  },
  {
    "op": "add",
    "path": "/fields/System.Tags",
    "value": "from-pr-review; auto-created"
  }
]
```

### Link Work Item to PR

To link the work item to the PR after creation:

```
PATCH https://dev.azure.com/{organization}/{project}/_apis/wit/workitems/{workItemId}?api-version=7.1
Content-Type: application/json-patch+json

[
  {
    "op": "add",
    "path": "/relations/-",
    "value": {
      "rel": "ArtifactLink",
      "url": "vstfs:///Git/PullRequestId/{projectId}%2F{repositoryId}%2F{pullRequestId}",
      "attributes": {
        "name": "Pull Request"
      }
    }
  }
]
```

### Alternative: Link via PR Update

```
POST https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests/{pullRequestId}/workitems/{workItemId}?api-version=7.1
```

This links an existing work item to a PR directly.

### Via PR Comments

ADO supports `#workitemID` mentions in PR comments — but this is for **linking existing work items**, not creating them. There is no way to create work items via PR comment syntax. You must use the Work Item Tracking API.

### Gotchas
- Content-Type **must** be `application/json-patch+json`, not `application/json`.
- Work item types with spaces (e.g., `User Story`) are URL-encoded: `$User%20Story`.
- The `op` field in each patch operation is typically `"add"` for creation.
- Description field supports HTML content.
- Area/Iteration paths must exist in the project settings.

---

## 10. MerlinBot

### What is MerlinBot?

MerlinBot is a **Microsoft-internal automated PR bot** used within the One Engineering System (1ES). It is **not a public Azure DevOps feature** — it's a custom service hook/bot deployed in Microsoft's internal ADO organizations (particularly those under the DevDiv/1ES umbrella).

### What MerlinBot Does

- **Auto-assigns reviewers** based on code ownership (CODEOWNERS-like rules)
- **Posts PR comments** with review checklists, policy guidance, and automated checks
- **Enforces policies** such as minimum reviewers, required sign-offs, security reviews
- **Creates/manages threads** on PRs with automated status updates
- **Integrates with compliance workflows** (SDL checks, credential scanning results, etc.)
- **Tracks PR metrics** (time to review, time to merge, etc.)

### How MerlinBot Works (from an API perspective)

MerlinBot is a service that:
1. **Subscribes to ADO Service Hooks** for events like `git.pullrequest.created`, `git.pullrequest.updated`, `git.pullrequest.commented`
2. **Uses the same REST APIs** described in this document (threads, comments, statuses) to interact with PRs
3. **Identifies itself** via a service account — its comments appear as authored by the MerlinBot user
4. **Uses thread `properties`** to store metadata (e.g., `{ "MerlinBot.ThreadType": "ReviewChecklist" }`)

### Interacting with MerlinBot Comments

Since MerlinBot uses standard ADO threads/comments, you can:

- **Read its comments**: List PR threads and filter by author (MerlinBot's identity)
- **Reply to its threads**: POST a comment to the thread ID
- **Check its properties**: Thread `properties` dictionary contains MerlinBot-specific metadata
- **Trigger it**: Some MerlinBot behaviors are triggered by specific comment text (e.g., typing `/merlin retry` in a comment may re-trigger checks — the exact trigger commands are documented internally at Microsoft)

### Gotchas
- MerlinBot documentation lives on `eng.ms` (internal Microsoft docs, requires corpnet access)
- The bot's exact behavior, trigger commands, and configuration vary by team/organization
- If your tool needs to interop with MerlinBot, treat its comments as regular ADO threads and filter by the MerlinBot service account's display name or identity ID
- MerlinBot may set thread `properties` that encode retry state, check results, etc. — parse these as needed
- **MerlinBot is not the same as Azure DevOps bot accounts in general** — other teams may have their own bots using the same API patterns

---

## ADO Comment/Thread Model Deep Dive

### Architecture

```
Pull Request
  └── Thread 1 (general comment, no file context)
  │     ├── Comment 1 (root) — content, author, date
  │     ├── Comment 2 (reply to 1)
  │     └── Comment 3 (reply to 1)
  │     └── [status: Active]
  │
  └── Thread 2 (inline on file /src/main.go, line 42)
  │     ├── Comment 1 (root) — "This looks wrong"
  │     └── Comment 2 (reply) — "Fixed in latest push"
  │     └── [status: Fixed]
  │
  └── Thread 3 (system — auto-generated by policies/merges)
        ├── Comment 1 (system) — "Reviewer X approved"
        └── [status: Closed]
```

### Key Concepts

1. **Threads are the container**: Every comment lives inside a thread. There are no standalone comments.

2. **Thread = resolution unit**: Status (Active, Fixed, Won't Fix, Closed, Pending, By Design) is set at the thread level. You **cannot** resolve individual comments — only entire threads.

3. **Comments are ordered by ID**: Within a thread, comments are sequentially numbered starting from 1.

4. **Thread Context** determines positioning:
   - `null` → general PR comment (shows in Overview tab)
   - `{ filePath: "/..." }` → file-level comment
   - `{ filePath: "/...", rightFileStart/End: {...} }` → inline comment anchored to specific lines

5. **Comment types**:
   - Type `1` (text) — user comments, bot comments
   - Type `2` (codeChange) — system-generated for code suggestions
   - Type `3` (system) — auto-generated by ADO (votes, policy updates, merge events)

6. **Thread properties**: Arbitrary `string → { "$type": "...", "$value": "..." }` dictionary. Bots use this to store metadata. Human threads typically have no properties.

7. **Deleted threads/comments**: Threads and comments have `isDeleted` flags. Deleted items may still appear in API responses.

### Thread vs Comment — Summary

| Aspect | Thread | Comment |
|--------|--------|---------|
| Has status (Active/Resolved/etc.) | ✅ | ❌ |
| Has file/line context | ✅ | ❌ (inherits from thread) |
| Can be created independently | ✅ | ❌ (must belong to a thread) |
| Can be replied to | ✅ (by adding comments) | Comments reference parent via `parentCommentId` |
| Can be resolved | ✅ | ❌ (thread is resolved) |

---

## az devops CLI

### PR-Related Commands

| Command | Description |
|---------|-------------|
| `az repos pr create` | Create a PR with full options (auto-complete, draft, reviewers, labels, work items) |
| `az repos pr show --id N` | Get PR details |
| `az repos pr list` | List PRs with filters (status, creator, reviewer, branch) |
| `az repos pr update --id N` | Update PR (title, description, status, auto-complete, squash, draft) |
| `az repos pr set-vote --id N --vote approve` | Vote on PR (approve, approve-with-suggestions, reject, reset, wait-for-author) |
| `az repos pr checkout --id N` | Checkout PR source branch locally |
| `az repos pr reviewer add --id N` | Add reviewers |
| `az repos pr reviewer list --id N` | List reviewers |
| `az repos pr reviewer remove --id N` | Remove reviewers |
| `az repos pr policy list --id N` | List policies on a PR |
| `az repos pr policy queue --id N` | Re-queue a policy evaluation |
| `az repos pr work-item add --id N --work-items W` | Link work items to PR |
| `az repos pr work-item list --id N` | List linked work items |
| `az repos pr work-item remove --id N` | Unlink work items |

### Other Useful Commands

| Command | Description |
|---------|-------------|
| `az pipelines runs list` | List pipeline runs |
| `az pipelines runs show --id N` | Get pipeline run details |
| `az boards work-item create --type Bug --title "..."` | Create work item |
| `az boards work-item update --id N --fields "System.State=Active"` | Update work item |
| `az devops invoke` | Raw REST API call via CLI — useful for APIs without dedicated commands |

### What CLI Can Do That REST Can't (and vice versa)

**CLI advantages:**
- `az repos pr checkout` — checks out the PR branch locally (no REST equivalent for local git operations)
- `--detect` — auto-detects organization/project from git remote config (ergonomic for scripts in repos)
- `--use-git-aliases` — enables `git pr list` syntax
- Built-in authentication management (`az login`) — no manual token handling

**REST advantages over CLI:**
- **No CLI command for PR threads/comments** — you can't create, read, or manage comment threads via CLI (critical gap!)
- **No CLI command for build logs** — you can't fetch log content
- **No CLI command for PR statuses** — can't check pipeline statuses on a PR
- **No CLI command for setting thread status** (resolve/close) — must use REST
- **More granular control** — REST gives access to all fields, query parameters, response structure
- **Better for automation** — REST is more predictable for programmatic use
- The CLI's `az devops invoke` can work around these gaps but it's essentially raw REST with extra steps

**Bottom line: For this Go tool, use REST API directly.** The CLI is great for interactive use but lacks thread/comment management entirely.

### Authentication on WSL + Windows (Microsoft Employees)

```bash
# One-time setup — installs the azure-devops extension
az extension add --name azure-devops

# Login (leverages Windows broker on enrolled device)
az login --allow-broker

# Set defaults (once)
az devops configure --defaults organization=https://dev.azure.com/MyOrg project=MyProject

# Get token for REST API use
TOKEN=$(az account get-access-token --resource 499b84ac-1321-427f-aa17-267ca6975798 --query accessToken -o tsv)

# Use with curl
curl -H "Authorization: Bearer $TOKEN" "https://dev.azure.com/MyOrg/MyProject/_apis/git/..."
```

---

## Rate Limiting

### How It Works

ADO uses **Azure DevOps Throughput Units (TSTUs)** — an abstract unit blending CPU, memory, I/O, and database usage.

| Metric | Value |
|--------|-------|
| Normal user activity | ≤ 10 TSTUs per 5-minute window |
| Occasional spike | Up to 100 TSTUs |
| **Hard limit** | **200 TSTUs per sliding 5-minute window** |
| Pipeline limit | 200 TSTUs per pipeline per 5-minute window |

### What Happens When You Hit the Limit

1. **Delays**: Requests are slowed (milliseconds to 30 seconds per request)
2. **HTTP 429**: If consumption stays very high, requests are blocked with `TF400733`
3. **Email notification**: The user/service account gets notified

### Response Headers to Monitor

| Header | Description |
|--------|-------------|
| `Retry-After` | Seconds to wait before next request (RFC 6585) |
| `X-RateLimit-Resource` | Which service/resource is throttled |
| `X-RateLimit-Delay` | How long the current request was delayed (seconds) |
| `X-RateLimit-Limit` | Total TSTUs allowed |
| `X-RateLimit-Remaining` | TSTUs remaining before delays start |
| `X-RateLimit-Reset` | Unix timestamp when tracked usage returns to 0 |

### Best Practices for the Go Tool

1. **Honor `Retry-After`**: If present, sleep that many seconds before retrying.
2. **Monitor `X-RateLimit-Remaining`**: If approaching 0, proactively slow down.
3. **Implement exponential backoff**: On 429 or delay headers.
4. **Batch when possible**: Don't poll in tight loops.
5. **Cache aggressively**: PR details, build status — don't re-fetch unnecessarily.
6. **Use `If-None-Match` / ETags**: ADO supports conditional requests on some endpoints.

### TSTUs Are Opaque

You cannot pre-calculate TSTUs for a given operation. A simple GET might cost 1 TSTU; a complex work item query might cost 10. The only way to gauge is to monitor the headers.

### Increasing Limits

Assign **Basic + Test Plans** access level to the service account identity to get higher limits. Revert when no longer needed (you're charged for the access level).

---

## Polling vs Webhooks

### Webhooks (Service Hooks)

ADO supports **Service Hooks** — HTTP POST notifications to a URL when events occur.

**Relevant events:**
| Event | Event ID | When |
|-------|----------|------|
| Build completed | `build.complete` | A build finishes |
| PR created | `git.pullrequest.created` | A new PR is opened |
| PR updated | `git.pullrequest.updated` | PR status, votes, or source branch changes |
| PR commented on | `ms.vss-code.git-pullrequest-comment-event` | A comment is added to a PR |
| Pipeline run state changed | `ms.vss-pipelines.run-state-changed-event` | Pipeline run starts/completes/fails |
| Pipeline run stage state changed | `ms.vss-pipelines.stage-state-changed-event` | Individual stage completes |

**Webhook setup via REST:**
```
POST https://dev.azure.com/{organization}/_apis/hooks/subscriptions?api-version=7.1

{
  "publisherId": "tfs",
  "eventType": "build.complete",
  "consumerId": "webHooks",
  "consumerActionId": "httpRequest",
  "publisherInputs": {
    "buildStatus": "Failed",
    "projectId": "project-guid"
  },
  "consumerInputs": {
    "url": "https://your-service.com/webhook"
  }
}
```

### Polling

For a CLI/local tool (not a web service), polling is the pragmatic choice.

**Recommended polling strategy:**

1. **For pipeline status monitoring:**
   - Poll every **30 seconds** while builds are in progress
   - Use PR statuses endpoint (lighter weight than querying builds directly)
   - Stop polling once all statuses are `succeeded` or `failed`
   - Consider using the build timeline endpoint to get stage-level progress

2. **For comment monitoring:**
   - Poll every **60 seconds** (comments are less time-critical)
   - Use `If-None-Match` with ETags to avoid unnecessary data transfer
   - Track last-seen thread/comment IDs to detect new activity

3. **Backoff pattern:**
   ```
   Initial interval: 10s
   Max interval: 120s
   Multiplier: 1.5x on no change
   Reset to initial on change detected
   ```

### Recommendation for This Tool

Since this is a **Go CLI tool** (not a web service that can receive webhooks), **polling is the right approach**:

- Webhooks require a publicly accessible HTTP endpoint — impractical for a developer workstation tool
- Polling every 30s for build status is well within rate limits
- For a single PR, the polling load is negligible (< 1 TSTU per poll cycle)
- Consider a hybrid: use `az pipelines runs show` or the REST API in a polling loop, but offer a `--webhook-url` option for users who have a tunnel/server

---

## Quick Reference: All Endpoints

| Operation | Method | Endpoint |
|-----------|--------|----------|
| Get PR | GET | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}` |
| Update PR | PATCH | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}` |
| List PR statuses | GET | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}/statuses` |
| List builds | GET | `{org}/{project}/_apis/build/builds?branchName=refs/pull/{id}/merge` |
| Get build | GET | `{org}/{project}/_apis/build/builds/{buildId}` |
| Get build timeline | GET | `{org}/{project}/_apis/build/builds/{buildId}/timeline` |
| List build logs | GET | `{org}/{project}/_apis/build/builds/{buildId}/logs` |
| Get build log | GET | `{org}/{project}/_apis/build/builds/{buildId}/logs/{logId}` |
| List PR threads | GET | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}/threads` |
| Create thread | POST | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}/threads` |
| Update thread | PATCH | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}/threads/{threadId}` |
| Create comment (reply) | POST | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}/threads/{threadId}/comments` |
| Update comment | PATCH | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}/threads/{threadId}/comments/{commentId}` |
| Create work item | POST | `{org}/{project}/_apis/wit/workitems/$\{type\}` |
| Update work item | PATCH | `{org}/{project}/_apis/wit/workitems/{id}` |
| Link work item to PR | POST | `{org}/{project}/_apis/git/repositories/{repo}/pullrequests/{id}/workitems/{workItemId}` |
| Get connection data | GET | `{org}/_apis/connectiondata` |

All endpoints require `?api-version=7.1` query parameter.

Base URL: `https://dev.azure.com/` (prefix all endpoints)

---

## Go Implementation Notes

### HTTP Client Setup

```go
// Auth header for PAT
auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+pat))

// Auth header for Entra token
auth := "Bearer " + token
```

### Content Types to Remember

| Operation | Content-Type |
|-----------|-------------|
| Most POST/PATCH | `application/json` |
| Create/Update work items | `application/json-patch+json` |
| Get build logs | Accept: `text/plain` |

### Go SDK Option

Microsoft publishes a Go client: `github.com/microsoft/azure-devops-go-api/azuredevops/v7`. However:
- It may lag behind the latest API version
- It doesn't cover all APIs (notably, thread management is limited)
- For a focused CLI tool, raw REST may be simpler and more predictable
- Consider using it for type definitions even if you make raw HTTP calls

### Error Handling

ADO returns errors as:
```json
{
  "$id": "1",
  "innerException": null,
  "message": "TF401019: The pull request does not exist...",
  "typeName": "Microsoft.TeamFoundation.Git.Server.GitPullRequestNotFoundException",
  "typeKey": "GitPullRequestNotFoundException",
  "errorCode": 0,
  "eventId": 3000
}
```

Key: Parse `message` and `typeKey` for actionable error info.
