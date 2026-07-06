---
name: create-task
description: Guide through creating well-structured Stringwork tasks with work context, relevant files, and constraints. Use when delegating complex work to workers.
---

# Task Creation Guide

## When to Use

- Delegating implementation work to a worker
- Breaking a large feature into subtasks for multiple workers
- Creating review, investigation, or research tasks
- Any time you want to ensure a task has enough context for a worker to succeed autonomously

## Task Anatomy

A well-structured task has:

### Required
- **title** — short, imperative phrase ("Add auth middleware", "Fix race condition in cache")
- **created_by** — your agent name (usually 'cursor')

### Recommended
- **assigned_to** — use `'any'` for auto-assignment to the least-loaded worker, or a specific agent name
- **description** — detailed instructions: what to do, what to check, expected output format
- **relevant_files** — array of file paths the worker should focus on
- **background** — architectural context: patterns used, related components, design decisions
- **constraints** — array of guardrails: "do not modify the public API", "keep backward compatibility"

### Optional
- **priority** — 1=critical, 2=high, 3=normal (default), 4=low
- **expected_duration_seconds** — enables SLA monitoring; the server alerts you if this is exceeded
- **depends_on** — array of task IDs this task depends on
- **model_tier** — named tier for worker model selection (`fast`, `standard`, `capable`). Resolved via `orchestration.model_tiers` in config at spawn time. **You decide per task.**
- **model** — explicit CLI model override (e.g. `haiku`, `opus`). Takes precedence over `model_tier`.
- **worker_type** — pin to a worker CLI type when using `assigned_to='any'`
- **capabilities** — required worker capabilities for auto-assignment (e.g. a cheap-model pool tagged `fast`)

## Model selection (driver decides)

Pick the model tier on every `create_task`. Stringwork injects `--model` when spawning the worker. **Each tier maps per provider** (`claude-code`, `codex`, `gemini`) — the same `model_tier='fast'` yields `haiku` for Claude, `o4-mini` for Codex, and `gemini-2.5-flash` for Gemini.

| Task signal | Suggested tier |
|-------------|----------------|
| Docs, style, simple reads | `fast` |
| Standard feature or fix | `standard` |
| Security, auth, large diffs, unclear root cause | `capable` |

| Provider | Worker type | Example fast | Example standard | Example capable |
|----------|-------------|--------------|------------------|-----------------|
| Claude Code | `claude-code` | `haiku` | `sonnet` | `opus` |
| Codex | `codex` | `o4-mini` | `gpt-5-codex` | `gpt-5-codex` |
| Gemini | `gemini` | `gemini-2.5-flash` | `gemini-2.5-pro` | `gemini-2.5-pro` |

Configure tier → model mappings in `~/.config/stringwork/config.yaml`:

```yaml
orchestration:
  model_tiers:
    fast:
      claude-code: haiku
      codex: o4-mini
      gemini: gemini-2.5-flash
    standard:
      claude-code: sonnet
      codex: gpt-5-codex
      gemini: gemini-2.5-pro
    capable:
      claude-code: opus
      codex: gpt-5-codex
      gemini: gemini-2.5-pro
  workers:
    - type: claude-code
      model: sonnet
    - type: codex
      model: gpt-5-codex
    - type: gemini
      model: gemini-2.5-pro
```

Pin a provider explicitly:

```
create_task title='Codex review' assigned_to='codex' model_tier='fast' created_by='cursor' ...
create_task title='Gemini docs pass' assigned_to='gemini' model_tier='fast' created_by='cursor' ...
```

Example (auto-assign — orchestrator picks worker, tier resolves per provider):

```
create_task
  title='Fix typo in README'
  assigned_to='any'
  created_by='cursor'
  model_tier='fast'
  description='Single-file docs fix.'
```

Call `worker_status` to see configured tier mappings for all worker types.

Call `list_model_options` at session start for the full tier → model map and selection guidance.

## Mechanical executor brief (5 elements)

Delegates start with **no shared conversation context**. A complete brief includes:

1. **Task / outcome** — what done looks like
2. **Files / targets** — exact paths to touch and what NOT to touch
3. **Upstream decisions** — choices already made (so the delegate does not re-litigate)
4. **Verification** — which lint/build/test must pass
5. **Commit** — whether to commit (default: **NO** — propose a message instead)

If (1)–(4) are missing or ambiguous, the delegate should make safe mechanical
progress only, then STOP and report what is missing.

For routing (when to delegate vs keep on the driver, what never to delegate):
team playbooks that adopt mechanical-executor routing (e.g. RegFin
`docs/ai-token-efficiency.md` — section *Mechanical executor routing*).

## How the worker should report back

Include in every task `description`:

> Return a structured report: **Changed** / **Verified** (quote the key pass/fail
> line from actual output) / **Decisions I had to make** / **Blocked** / **Commit**
> (proposed). Report outcomes, not a command-by-command narration.

Canonical contract (vendor-neutral): `Changed` / `Verified` / `Decisions` /
`Blocked` / `Commit` — see team playbooks that adopt this pattern (e.g. RegFin
`docs/ai-agent-best-practices.md` §3.4).

## Examples

### Implementation task

```
create_task
  title='Add rate limiting middleware'
  assigned_to='any'
  created_by='cursor'
  description='Implement a rate limiting middleware using a token bucket algorithm. Should support per-IP and per-user limits. Add unit tests.'
  relevant_files=['internal/middleware/', 'internal/config/config.go']
  background='Middleware chain is in internal/middleware/chain.go. Config is loaded from YAML.'
  constraints=['Do not modify existing middleware signatures', 'Use stdlib only, no external rate limit libraries']
  expected_duration_seconds=600
  priority=2
```

### Investigation task

```
create_task
  title='Investigate memory leak in worker manager'
  assigned_to='claude-code'
  created_by='cursor'
  description='Memory usage grows linearly with spawned workers. Profile the WorkerManager and identify where references are retained after worker exit.'
  relevant_files=['internal/app/worker_manager.go', 'internal/app/worker_manager_test.go']
  background='Workers are spawned via exec.Command. ProcessInfo is stored in processActivity map.'
  constraints=['Read-only investigation — do not modify code yet']
  expected_duration_seconds=300
```

### Parallel subtasks

For large features, break into independent subtasks and assign to `'any'`:

```
# Subtask 1
create_task title='Implement domain model for feature X' assigned_to='any' ...

# Subtask 2
create_task title='Add API handler for feature X' assigned_to='any' depends_on=[<subtask1_id>] ...

# Subtask 3
create_task title='Write integration tests for feature X' assigned_to='any' depends_on=[<subtask1_id>, <subtask2_id>] ...
```

## Tips

- Workers cannot see your conversation — put everything they need in the task description and background
- Use `expected_duration_seconds` to catch tasks that take longer than expected
- Check `worker_status` after creating a task to confirm a worker picked it up
- Use `constraints` to prevent workers from making unwanted changes
