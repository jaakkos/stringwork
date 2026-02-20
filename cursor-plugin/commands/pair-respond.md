---
name: pair-respond
description: Process unread messages and pending tasks from Stringwork workers
---

# Pair Respond

Process all unread messages and pending tasks from your Stringwork workers.

## Steps

### 1. Initialize

1. Call `get_session_context` for your agent name
2. Call `set_presence` with status='working' and your current workspace
3. Call `read_messages` to see all unread messages
4. Call `list_tasks` to see pending and in-progress tasks

### 2. Process messages

For each unread message:

- **Worker findings** — review the summary, check if the work meets expectations, acknowledge or request follow-ups
- **Questions from workers** — research and answer, or provide the missing context
- **System alerts** — check worker health (spawn failures, progress warnings, quota issues)
- **Review results** — synthesize findings, present to the user

### 3. Process pending tasks

For each pending task that needs attention:

- **Completed tasks** — review the output, close if satisfactory
- **Blocked tasks** — unblock by providing missing context or reassigning
- **Failed tasks** — check error details, fix the issue, retry or reassign

### 4. Report

Summarize what you processed: messages handled, tasks addressed, any items needing user input.
