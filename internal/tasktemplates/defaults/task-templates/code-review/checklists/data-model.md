# Data Model and Schema

Focus on whether the wire format, storage schema, or external contract
can evolve safely.

## Always check

- **Wire compatibility.** Proto field numbers are not reused. Removed
  fields are reserved. Renamed fields keep their number. Required
  fields are not added to existing messages.
- **Schema evolution safety.** Old code can read data written by new
  code (forward compatibility). New code can read data written by old
  code (backward compatibility). One direction is enforceable; the
  other is a deployment ordering constraint.
- **Reader / writer parity.** When the writer changes, every reader is
  updated in the same change OR the change is documented as gated
  behind a feature flag with a rollout plan.
- **Migration safety.** Database migrations are reversible (or have an
  explicit "no rollback" justification). Index changes do not block
  writes. Column drops happen after the reader is removed.
- **Generated code.** Codegen artifacts are regenerated and committed.
  No drift between schema and generated bindings.
- **Sensitive field handling.** New fields carrying PII use the
  project's sensitive-string / encryption pattern.

## For migrations

- Is the migration reversible? Is there a rollback migration?
- Can old code read data written by new code?
- Can new code read data written by old code?
- Is there a deployment ordering constraint? (deploy reader before writer?)
- For envelope-versioned storage: does the reader handle unknown
  schema versions gracefully?

## Output

Every breaking-change finding is **MUST_FIX**. Wire-incompatible
changes that ship to production are operational incidents.
