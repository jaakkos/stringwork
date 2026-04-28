---
name: code-review
description: Coordinate structured code reviews using Stringwork workers. Use when you need to review a PR, branch, or set of changes with prioritized findings.
---

# Code Review Coordination

## When to Use

- Reviewing a pull request before merge
- Getting a second opinion on implementation changes
- Security or performance audits on specific files
- Reviewing changes across multiple files that benefit from parallel review

## How it works

The aspect decomposition (which workers, focused on what) lives in
the Stringwork [task templates](../../../docs/TASK_TEMPLATES.md)
system, not in this skill. You call the planner; it returns the
ordered list of `PlannedAspect` records to spawn. Your only jobs are:
gather inputs, ask the planner, iterate the plan, and synthesize the
findings that come back.

## Step 1 — Plan the review

Call `task_plan` with the file list and a one-line summary:

```
task_plan
  template='code-review'
  inputs={
    files: ['internal/middleware/auth.go', 'internal/middleware/auth_test.go'],
    summary: 'Add JWT middleware to enforce auth on protected routes'
  }
```

The response is JSON with `template`, `tags` (the classifier tags
that fired), and `aspects` (the planned worker briefs). Each aspect
already has a composed `description` (background + checklist +
finding format) ready to pass through unchanged.

## Step 2 — Spawn one task per planned aspect

For each entry in `aspects`, call `create_task` with the planner's
output copied through, including the `template` and `aspect` metadata
so listings and the constitution alias rule see the provenance:

```
create_task
  title='<PlannedAspect.title>'
  description='<PlannedAspect.description>'
  relevant_files=<PlannedAspect.relevant_files>
  template='code-review'
  aspect='<PlannedAspect.aspect>'
  assigned_to='any'
  created_by='cursor'
  priority=2
```

Use `assigned_to='any'` so workers pull aspects off the queue in
parallel. `priority=2` (high) puts review work above normal feature
tasks.

> If `task_plan` returns zero aspects (typically a docs-only or
> config-only change where no classifier fired), it includes a
> `notes` field explaining why. Do not spawn anything — surface the
> note to the user.

## Lightweight alternative — `request_review`

For an ad hoc, single-reviewer review (no decomposition needed),
`request_review` is the legacy entry point:

```
request_review
  from='cursor'
  to='claude-code'
  description='Quick review of the error handling changes in handler.go'
  files=['internal/handler.go']
```

This stamps `Template='code-review'` on the resulting task (so the
constitution still attaches review-scoped sources) but skips the
multi-aspect plan. Reach for `task_plan` when the change is large or
spans concerns; reach for `request_review` when one pair of eyes is
enough.

## Step 3 — Synthesize findings

When workers report back via `send_message`, combine their findings
into a single report:

1. Collect all findings from all spawned aspects
2. De-duplicate overlapping concerns (the same issue surfaced by
   two aspects — keep one, note both source aspects)
3. Resolve conflicting opinions by preferring the more conservative
   assessment
4. Order by severity:
   - **MUST_FIX** — security, data loss, crashes (block merge)
   - **SHOULD_FIX** — correctness, maintainability, performance
   - **NIT** — style, docs, minor improvements
5. Present the consolidated review to the user with file paths,
   line numbers, and concrete fix suggestions

The finding format is fixed by the planner (`### Finding format` at
the bottom of every aspect description), so all workers report in the
same shape. Trust the format.
