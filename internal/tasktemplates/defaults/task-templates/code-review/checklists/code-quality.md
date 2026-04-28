# Code Quality and Standards

Focus on whether the change is one a teammate would be glad to maintain
six months from now.

## Always check

- **Naming.** Names are distinct enough to grep for and describe what
  the thing IS, not what it does to the caller. Avoid generic names
  (`utils`, `helpers`, `manager`, `data`) at module / package scope.
- **Structure.** One responsibility per file / module / function. New
  functions are 4-20 lines; new files are under ~500 lines. Split
  before extending when either limit is in sight.
- **Idiomatic patterns.** Code matches the project's existing patterns
  for the same problem (errors, logging, configuration, dependency
  injection, testing). Deviations are deliberate and justified.
- **Test quality.** Tests are deterministic, hermetic, and runnable
  from a single command. Every fix lands with a regression test that
  fails before the fix.
- **Test coverage.** For every new or changed production function,
  tests cover happy path, error paths, and at least one edge case.
- **Dead code.** Removed code is fully removed (no leftover imports,
  no orphan helpers, no commented-out blocks).
- **Build / wiring.** Generated files are regenerated, build files are
  updated, dependency manifests are in sync.

## Comment hygiene

Comments explain WHY with provenance (a ticket, an upstream bug, a
regulatory requirement) — not WHAT the code does. Narrating the code
in prose is a smell.

## Output

For every issue produce a finding in the standard format with a
specific file:line reference and a concrete suggestion.
