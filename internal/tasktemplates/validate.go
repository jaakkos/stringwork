package tasktemplates

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateInputs checks the user-supplied input map against the
// template's freeform InputDeclaration. It returns a single combined
// error describing every failure found so the caller surfaces them all
// at once instead of round-tripping per missing key.
//
// Rules:
//
//   - Every key in decl.Required MUST be present and non-empty on
//     inputs. Empty string / empty slice / nil counts as missing.
//   - When a key has a primitive type tag in decl.Declarations, the
//     value MUST match: "string" → string, "list" → []string OR
//     []any-of-strings (mcp-go decodes JSON arrays as []any{}). Any
//     other tag value is treated as untyped and skipped.
//   - Extra keys NOT in decl.Declarations are preserved on the inputs
//     map (Plan honours them as opaque pass-through values for
//     custom routing rules) but flagged via doctor — never errored
//     here. The validator is a contract check, not a schema check.
func ValidateInputs(inputs map[string]any, decl InputDeclaration) error {
	var problems []string

	// Sort required keys for deterministic error ordering — the same
	// missing-keys set should produce the same error string across
	// runs so test expectations and operator scripts can match on it.
	required := append([]string(nil), decl.Required...)
	sort.Strings(required)
	for _, key := range required {
		v, ok := inputs[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("missing required input %q", key))
			continue
		}
		if isEmpty(v) {
			problems = append(problems, fmt.Sprintf("required input %q is empty", key))
		}
	}

	// Sort declared keys too so type-mismatch errors are stable.
	declared := make([]string, 0, len(decl.Declarations))
	for k := range decl.Declarations {
		declared = append(declared, k)
	}
	sort.Strings(declared)
	for _, key := range declared {
		v, ok := inputs[key]
		if !ok {
			continue
		}
		want := strings.TrimSpace(strings.ToLower(decl.Declarations[key]))
		switch want {
		case "string":
			if _, ok := v.(string); !ok {
				problems = append(problems, fmt.Sprintf("input %q: expected string, got %T", key, v))
			}
		case "list", "[]string":
			if !isStringList(v) {
				problems = append(problems, fmt.Sprintf("input %q: expected list of strings, got %T", key, v))
			}
		default:
			// Unknown type tag — treat as untyped.
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("input validation failed: %s", strings.Join(problems, "; "))
}

// CoerceStringList normalises any of the common shapes mcp-go / yaml.v3
// hand a list of strings into into a fresh []string. The shapes seen in
// practice:
//
//   - []string                — already correct.
//   - []any{"a", "b"}         — JSON / mcp-go default.
//   - []interface{}{"a", "b"} — YAML default.
//
// Anything else returns nil. Strict-typed callers should validate via
// ValidateInputs first; this helper is for the planner / classifier
// hot path that needs a usable []string and doesn't care about the
// original encoding.
func CoerceStringList(v any) []string {
	switch s := v.(type) {
	case []string:
		return append([]string(nil), s...)
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

// isEmpty reports whether the value should be considered "missing" for
// required-key validation. nil, empty strings, and empty slices are
// empty. Numbers and booleans are NEVER empty (zero / false are valid
// values that the caller may legitimately have meant).
func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []string:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// isStringList reports whether v is one of the shapes CoerceStringList
// can handle, with every element being a string. An empty list still
// counts (the validator doesn't enforce non-empty here — that's
// ValidateInputs' isEmpty check on Required).
func isStringList(v any) bool {
	switch s := v.(type) {
	case []string:
		return true
	case []any:
		for _, item := range s {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}
