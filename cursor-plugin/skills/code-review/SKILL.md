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

## Single-Worker Review

Create a review task for one worker:

```
create_task
  title='Review PR #123: Add auth middleware'
  assigned_to='any'
  created_by='cursor'
  description='Review the auth middleware changes. Focus on security, error handling, and test coverage.'
  relevant_files=['internal/middleware/auth.go', 'internal/middleware/auth_test.go']
  background='Auth service architecture and relevant patterns.'
  constraints=['Classify findings as MUST-FIX, SHOULD-FIX, or NIT']
```

## Multi-Worker Review (parallel)

Split review concerns across workers for faster, deeper analysis:

```
# Worker 1: Security focus
create_task
  title='Security review: auth middleware'
  assigned_to='any'
  description='Focus on security: injection, auth bypass, PII exposure, token validation.'
  relevant_files=[...]

# Worker 2: Code quality focus
create_task
  title='Code quality review: auth middleware'
  assigned_to='any'
  description='Focus on code quality, error handling, test coverage, naming, language idioms.'
  relevant_files=[...]
```

## Using request_review

For lightweight reviews, use `request_review` instead of `create_task`:

```
request_review
  from='cursor'
  to='claude-code'
  description='Quick review of the error handling changes in handler.go'
  files=['internal/handler.go']
```

## Synthesizing Findings

When workers report back via `send_message`, synthesize their findings:

1. Collect all findings from all reviewers
2. De-duplicate overlapping concerns
3. Prioritize by severity:
   - **Critical** — security, data loss, crashes (block merge)
   - **Important** — performance, correctness, maintainability (should fix)
   - **Nice-to-have** — style, docs, minor improvements (optional)
4. Present a consolidated review to the user

## Review Template for Workers

When writing the task description, ask workers to format findings as:

```
### [SEVERITY] Title (file:line)
- Description of the issue
- Why it matters
- Suggested fix
```
