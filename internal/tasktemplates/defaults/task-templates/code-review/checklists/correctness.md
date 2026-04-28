# Correctness and Logic

Focus on whether the change does the right thing on every input it can see.

## Always check

- **Null / nil / undefined safety.** Every dereference is guarded or the
  value is provably non-null at the call site.
- **Error propagation.** Errors are wrapped with operation context, not
  swallowed, not returned bare without provenance. No log-and-return at
  low levels.
- **Race conditions.** Shared state is either immutable, owned by one
  goroutine/coroutine, or guarded explicitly. New goroutines have a
  defined exit path.
- **Edge cases.** Empty input, zero values, maximum length, unicode,
  concurrent calls, boundary conditions.
- **Contract drift.** For every changed signature, type, field, or key:
  trace definition → callers → storage → tests. Each step must be
  consistent.
- **Backward compatibility.** Old callers / old data still work, or
  there is an explicit migration plan.

## Per change-type

- **Bug fix:** the root cause is addressed (not just the symptom). A
  regression test exists and would have caught the bug before the fix.
- **New feature:** all error paths are handled; negative-path tests
  exist; the feature is reachable from a real entry point.
- **Refactor:** behavior is preserved; every caller is updated; no
  hidden semantic change.
- **Data model:** reader / writer parity; old code can read new data
  AND new code can read old data; schema evolution is safe.

## Output

For every issue produce a finding in the standard format with a
specific file:line reference and a concrete fix (not a vague hint).
