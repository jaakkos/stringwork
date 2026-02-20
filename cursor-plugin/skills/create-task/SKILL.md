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
