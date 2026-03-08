# PR Monitoring Architecture

Otto's PR autopilot continuously monitors tracked pull requests, automatically fixing pipeline failures, responding to review comments, resolving merge conflicts, and addressing MerlinBot feedback. This document details how the system works, with a focus on Azure DevOps (ADO).

## Overview

```
┌──────────────────────────────────────────────────────────────────────────┐
│ otto server start                                                        │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐     │
│  │ RunMonitorLoop (poll interval: 10m default)                      │     │
│  │                                                                  │     │
│  │  for each tracked PR:                                            │     │
│  │  ┌────────────────────────────────────────────────────────────┐   │     │
│  │  │ pollSinglePR                                               │   │     │
│  │  │                                                            │   │     │
│  │  │  Stage 0: Check terminal states (merged/abandoned/conflict)│   │     │
│  │  │  Stage 1: Check pipeline status → FixPR if failed          │   │     │
│  │  │  Stage 2: Process review comments ──┐                      │   │     │
│  │  │  Stage 3: Handle MerlinBot feedback ├─ shared worktree     │   │     │
│  │  │                                     └─ single push         │   │     │
│  │  │  Stage 4: Notify + save                                    │   │     │
│  │  └────────────────────────────────────────────────────────────┘   │     │
│  └──────────────────────────────────────────────────────────────────┘     │
│                                                                          │
│  ┌─────────────────┐  ┌──────────────────┐  ┌────────────────────────┐   │
│  │ ADO REST API    │  │ Copilot Server   │  │ Git (worktrees)        │   │
│  │ PR metadata     │  │ LLM diagnosis    │  │ Clean checkout         │   │
│  │ Build logs      │  │ Code fixes       │  │ Commit + push          │   │
│  │ Comment threads │  │ Comment eval     │  │ Rebase                 │   │
│  │ Pipeline queue  │  │ Conflict resolve │  │ Force-push             │   │
│  └─────────────────┘  └──────────────────┘  └────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────────┘
```

## PR Document Lifecycle

Each tracked PR is stored as a YAML+markdown file at `~/.local/share/otto/prs/{provider}__{id}.md`. The YAML frontmatter contains structured state; the markdown body logs fix history.

### States

```
watching ──► fixing ──► watching (fix applied, awaiting pipeline)
    │            │
    │            └──► failed (max attempts exhausted)
    │
    ├──► green (pipeline succeeded)
    │       │
    │       └──► watching (new push or comment fix triggers pipeline)
    │
    ├──► merged (terminal, reaped after 24h)
    └──► abandoned (terminal, reaped after 24h)
```

### Stage Tracking Fields

| Field | Type | Purpose |
|-------|------|---------|
| `status` | string | Overall state (watching/fixing/green/failed/merged/abandoned) |
| `pipeline_state` | string | Pipeline status (pending/running/succeeded/failed/unknown) |
| `feedback_done` | bool | All review comments resolved |
| `merlinbot_done` | bool | MerlinBot feedback addressed |
| `has_conflicts` | bool | Merge conflicts detected |
| `fix_attempts` | int | Number of code fix attempts completed |
| `max_fix_attempts` | int | Limit before marking as failed |
| `seen_comment_ids` | []string | Composite keys (threadID:commentID) to prevent re-processing |
| `waiting_on` | string | Human-readable summary computed from above fields |

## Poll Cycle: Stage by Stage

### Stage 0: Terminal State Check

```
GetPR(url) → check live status
  ├── completed → status="merged", save, done
  ├── abandoned → status="abandoned", save, done
  └── conflicts → HasConflicts=true → ResolveConflicts()
```

The PR's title is also synced from the live metadata on each poll.

### Stage 1: Pipeline Status

```
GetPipelineStatus(prInfo) → builds[], overall state
  │
  ├── succeeded
  │     └── if status != "green": notify PR_GREEN, status="green"
  │         fall through to stages 2-3 (comments/MerlinBot)
  │
  ├── failed
  │     └── if fix_attempts < max: FixPR()
  │         else: status="failed", notify PR_FAILED
  │
  └── inProgress/pending/unknown
        └── if status was "green": status="watching" (new push detected)
```

**ADO API:** `GET /_apis/build/builds?branchName=refs/pull/{id}/merge` returns all builds for the PR. Builds are deduplicated by pipeline definition, keeping only the most recent per definition.

### Stage 2: Review Comments (Batched)

```
GetComments(prInfo) → all comment threads
  │
  ├── Filter: skip MerlinBot authors, skip system comments
  ├── Track: unresolved count for feedback_done
  ├── Match: composite key (threadID:commentID) against seen set
  │
  └── For each new unresolved comment:
        evaluateComment(pr, comment, workDir) → committed bool
```

### Stage 3: MerlinBot (Batched)

```
handleMerlinBotDaemon(pr, comments, workDir)
  │
  ├── Find all MerlinBot-authored comments
  ├── Short-circuit: "no AI feedback" → resolve, done
  ├── Filter to unresolved only
  │
  └── LLM evaluates all threads at once:
        THREAD {id}: FIX / WONT_FIX / BY_DESIGN
        ├── FIX: LLM applies fix, reply, resolve
        ├── WONT_FIX: reply with reason, resolve
        └── BY_DESIGN: reply with reason, resolve
```

### Batched Push

Stages 2 and 3 share a **single clean worktree**. Each comment fix and MerlinBot fix commits locally without pushing. After all stages complete, one `gitPush()` sends all commits at once — triggering only **one** pipeline run instead of N.

```
┌─────────────────────────────────────────────┐
│ Shared Clean Worktree (detached HEAD, /tmp) │
│                                             │
│  Comment fix 1 → git commit                │
│  Comment fix 2 → git commit                │
│  MerlinBot fix → git commit                │
│                                             │
│  ─── all done ───                           │
│                                             │
│  Single gitPush() → 1 pipeline run          │
│  mergeBack() → sync user's local worktree   │
└─────────────────────────────────────────────┘
```

## FixPR: Two-Phase Pipeline Repair

When a pipeline fails, FixPR runs a two-phase process with a 15-minute timeout:

### Phase 1: Diagnosis + Classification

```
Collect build logs from all failed builds
  │
  └── For each failed/partiallySucceeded/canceled build:
        GET /_apis/build/builds/{id}/timeline → failed tasks
        GET /_apis/build/builds/{id}/logs/{logId} → raw logs
        Extract error context (±5 lines around ##[error] markers)
```

The logs are sent to the LLM with a prompt requiring a structured classification:

```
CLASSIFICATION: INFRASTRUCTURE   or   CLASSIFICATION: CODE
```

**Infrastructure path:** Queue fresh builds (never retry individual jobs — in-place retries cause artifact conflicts). Does NOT count against fix attempts.

```
RetryBuild(buildID) → queueFreshBuild()
  ├── GET /_apis/build/builds/{id} → get definition ID + source version
  └── POST /_apis/build/builds → queue new build with same definition
```

**Fallback heuristics** (if LLM doesn't include the marker): matches patterns like "infrastructure issue" + "retry the build" + "no code changes needed".

### Phase 2: Code Fix

```
Create LLM session in clean worktree
  │
  ├── Send diagnosis + "fix the identified issues"
  ├── LLM edits files in the worktree
  │
  └── gitCommitAndPush(workDir, branch, "fix CI failures (attempt N)")
        └── mergeBack() → sync to user's local worktree
```

After the fix, `fix_attempts` is incremented. If it reaches `max_fix_attempts` (default 5), the PR is marked `failed`, a comment is posted on the PR, and a notification is sent.

## Merge Conflict Resolution

When ADO reports `mergeStatus="conflicts"`, ResolveConflicts runs with a 10-minute timeout:

```
1. Fetch latest from origin
2. Capture branch context (commits, diff stats)
3. Attempt git rebase onto target branch
   ├── Clean rebase → force-push → done
   └── Conflicts → identify conflicted files
4. LLM session to resolve:
   - Provided: branch commits (intent), diff stats, conflicted files
   - Task: edit files to resolve markers, git add, git rebase --continue
5. Verify rebase completed (no REBASE_HEAD remaining)
6. Force-push rebased branch
```

## Comment Evaluation

Each review comment is evaluated by an LLM with surrounding code context (±10 lines):

```
Decision     Action
───────────  ──────────────────────────────────────────
AGREE        LLM fixes code, commits, replies "Fixed in {hash}"
BY_DESIGN    Replies with explanation, resolves as by-design
WONT_FIX     Replies with explanation, resolves as won't-fix
```

The LLM has access to the full repository via the worktree, not just the diff, so it can understand the broader context when deciding whether to agree or push back.

## ADO REST API Summary

| Operation | Method | Endpoint |
|-----------|--------|----------|
| Get PR metadata | GET | `/_apis/git/repositories/{repo}/pullrequests/{id}` |
| Get builds for PR | GET | `/_apis/build/builds?branchName=refs/pull/{id}/merge` |
| Get build timeline | GET | `/_apis/build/builds/{id}/timeline` |
| Get build log | GET | `/_apis/build/builds/{id}/logs/{logId}` |
| Queue fresh build | POST | `/_apis/build/builds` |
| Get comment threads | GET | `/_apis/git/repositories/{repo}/pullrequests/{id}/threads` |
| Post comment thread | POST | `/_apis/git/repositories/{repo}/pullrequests/{id}/threads` |
| Reply to thread | POST | `.../{id}/threads/{threadId}/comments` |
| Resolve thread | PATCH | `.../{id}/threads/{threadId}` (status: 2=fixed, 3=wontFix, 5=byDesign) |

**Authentication:** Entra ID bearer tokens via `az account get-access-token`, cached and refreshed transparently. Falls back to PAT if `az cli` is unavailable.

**Rate limiting:** HTTP 429 triggers exponential backoff (1s, 2s, 4s...). HTTP 203 indicates token expiry — cache is invalidated, token refreshed, request retried once.

## Notifications

Otto sends Microsoft Teams notifications via Power Automate webhooks for key PR events:

| Event | Trigger | Location |
|-------|---------|----------|
| `pr_green` | Pipeline succeeds (first time) | pollSinglePR |
| `pr_failed` | Max fix attempts exhausted | FixPR (sole owner) |
| `comment_handled` | New comments processed | pollSinglePR |

Each event type is sent from exactly one code location to prevent duplicate notifications.
