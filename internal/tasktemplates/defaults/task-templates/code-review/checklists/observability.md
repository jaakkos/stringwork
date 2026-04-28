# Observability

Focus on whether operators can understand what the system did when it
matters — usually after an incident.

## Always check

- **Telemetry on success AND failure paths.** A function that emits a
  metric on success but not on failure makes the failure invisible to
  alerting.
- **Correct instrumentation.** Wide events / structured logs carry the
  request ID, the entity ID, and the operation. Counters split
  attempts vs successes vs failures so error rates are computable.
- **Cardinality.** Counter and metric attributes are low-cardinality.
  Per-entity IDs (`*_id`, `pdrn`, `user_id`) belong in logs / wide
  events, not metric labels.
- **No PII in observability.** Wide events, metrics, and traces never
  carry raw PII. Reference IDs only.
- **State transition coverage.** Every state machine transition emits
  an event with the from-state and to-state.
- **Error classification.** `wevent.Error(ctx, "ERROR_TYPE", err)`
  beats `wevent.Add("error", err.Error())` — the type is queryable
  and the raw error string is allowed to contain PII.
- **Logging conventions.** Log level matches severity. ERROR is for
  things that page someone; WARN is for things a human should see in
  daily review; INFO is for sampled traffic; DEBUG is verbose.

## Output

For every issue produce a finding in the standard format. Focus on
missing instrumentation rather than re-litigating code logic — that's
the correctness aspect's job.
