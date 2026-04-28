# Security

Assume an attacker who knows the stack and has access to public APIs.
Focus on whether the change widens the attack surface or leaks data.

## Always check

- **PII exposure.** No personally identifying data in logs, error
  messages, telemetry, or user-visible responses. Reference IDs (not
  raw values) cross trust boundaries.
- **Sensitive data handling.** Secrets, tokens, credentials, raw
  identifiers (SSN, TIN, ITIN, bank account numbers) are stored
  encrypted-at-rest, transmitted over TLS, and scrubbed from logs.
- **Data minimization.** The change stores or transmits the smallest
  amount of sensitive data needed to do the job.
- **Input validation.** Every external input is validated and sanitized
  at the boundary. SQL queries use parameter binding. HTML / template
  output is escaped.
- **Authentication and authorization.** Every new endpoint checks the
  caller is authenticated AND authorized for the specific resource.
  Audit events are emitted for sensitive operations.
- **Secret / credential exposure.** No secrets in commits, no secrets
  in error messages, no secrets in test fixtures.
- **API responses.** No leakage of internal IDs, internal hostnames,
  internal stack traces, or other implementation details.

## For each finding include

- **Attack scenario.** Specific steps an attacker would take to
  exploit this — not a theoretical "could be used to".
- **Blast radius.** What data or systems are exposed if exploited.
- **Severity.** MUST_FIX for any exploitable issue.

## Output

For every issue produce a finding in the standard format with the
attack scenario and blast radius.
