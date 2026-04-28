# Performance

Focus on whether the change degrades latency, throughput, or resource
usage on a real production workload.

## Always check

- **Time complexity.** O(n²) operations on user-supplied collections.
  Nested loops where a map / set lookup would do.
- **Database access patterns.** N+1 queries inside a loop. Missing
  batch APIs. Queries inside hot paths that hit the network on every
  call.
- **Memory.** Unbounded slices / lists / maps. Large allocations on
  hot paths. Per-request allocation of objects that could be
  package-scope or pooled.
- **I/O.** Blocking calls on a request path. Sequential operations
  that could be parallel. Synchronous-over-async patterns that
  serialize concurrency.
- **Caching opportunities.** Repeated lookups for the same key inside
  a single request. Redundant API / RPC calls per request.

## Quick wins

The 20% of changes that give 80% of the gains. Prefer:

- Hoisting a constant out of a loop.
- Replacing a linear scan with a map lookup.
- Batching a sequence of single-item RPCs into one batch RPC.
- Moving a cache initialization from per-request to per-process.

## For each finding include

- **Impact.** High / Medium / Low with reasoning (per-request? per-N?
  worst case?).
- **Current behavior.** What the code does today.
- **Expected improvement.** What changes after the fix.

## Scope

Focus on hot paths and per-request code. One-time initialization,
test helpers, and offline batch jobs are usually not worth flagging.
If the correctness aspect is also spawned, defer input-validation
concerns to it and focus on throughput / latency / resource usage.

## Output

For every issue produce a finding in the standard format including
impact and expected improvement.
