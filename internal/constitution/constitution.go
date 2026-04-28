// Package constitution loads and renders a layered set of guidance files
// ("constitution") that is surfaced to workers on every claim and inlined
// into spawn prompts.
//
// Inspired by unclebob/swarm-forge: a thin runtime that lets users (and
// teams) declare ordered prompt files which the worker is expected to
// read before doing any work. The package is I/O-light and pure-data:
// callers (policy / worker manager / tool handlers) own discovery via
// the Source interface, and rendering is two pure functions.
package constitution

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// File is a single rendered constitution entry. Path is the absolute
// on-disk location (used in the inline body separator). DisplayPath is
// the user-facing form (typically ~-collapsed). Content is the file
// body, already loaded by the Source. Source identifies which Source
// emitted this file — used by `constitution show` to make precedence
// debuggable.
type File struct {
	Source      string
	Path        string
	DisplayPath string
	Content     string
}

// Scope narrows which sources contribute. Zero values mean "match all".
// TaskKind is a coarse classifier derived from the task title (or, in a
// future iteration, an explicit task.kind field). AgentRole is the
// canonical agent type ("claude-code", "codex", …) for role-targeted
// rules. Sources with no scope filter always apply.
type Scope struct {
	TaskKind  string
	AgentRole string
}

// Source produces an ordered list of files for a given scope. Each
// implementation owns its own discovery (filesystem scan, git clone,
// embedded fixture, …) and is responsible for honouring its own scope
// filter — Resolve only concatenates and dedupes.
type Source interface {
	Name() string
	List(scope Scope) ([]File, error)
}

// Resolve walks the given sources in declaration order, asks each for
// the files it contributes under the requested scope, and returns the
// flattened list. Within a single source, ordering is the source's
// responsibility. Across sources, declaration order is preserved
// (earlier sources first), which is the swarm-forge "earlier files win
// on conflict" semantic — the worker reads top-to-bottom and treats
// later items as subordinate.
//
// One bad source does NOT swallow the rest: when src.List returns an
// error, Resolve records it under the source's Name() and continues
// with the remaining sources. The combined error (errors.Join) is
// returned alongside whatever files DID resolve, so a permission
// glitch on one path can't blank out a worker's view of the others.
// Callers that want to fail closed can check err != nil before using
// the slice; callers that prefer best-effort (the spawn / claim path)
// can use the slice and log the error.
func Resolve(sources []Source, scope Scope) ([]File, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	var (
		out  []File
		errs []error
	)
	for _, src := range sources {
		if src == nil {
			continue
		}
		files, err := src.List(scope)
		if err != nil {
			errs = append(errs, fmt.Errorf("constitution source %q: %w", src.Name(), err))
			continue
		}
		out = append(out, files...)
	}
	return out, errors.Join(errs...)
}

// BuildPreamble renders the short pointer block that ships in MCP tool
// responses (claim_next, get_work_context). It lists the resolved files
// in order so the worker can read them before doing any work, but does
// NOT inline contents — that would balloon every response by tens of
// kilobytes. Empty files → empty string (caller should treat the empty
// preamble as a pure no-op, no header, no trailing newline).
func BuildPreamble(files []File) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("== Constitution ==\n")
	b.WriteString("Read these files in order before working. If two files conflict,\n")
	b.WriteString("the earlier file wins.\n\n")
	width := digits(len(files))
	for i, f := range files {
		fmt.Fprintf(&b, "%*d. %s\n", width, i+1, f.DisplayPath)
	}
	return b.String()
}

// BuildInline renders the same ordered list as BuildPreamble plus the
// full content of every file, separated by `--- <path> ---` markers.
// Used at worker spawn time so the LLM sees the rules in its initial
// prompt without needing a follow-up read tool call. Empty files →
// empty string.
func BuildInline(files []File) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(BuildPreamble(files))
	b.WriteString("\n")
	for _, f := range files {
		fmt.Fprintf(&b, "--- %s ---\n", f.DisplayPath)
		b.WriteString(strings.TrimRight(f.Content, "\n"))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// SortFilesByDisplayPath is exposed for sources that want a stable
// alphabetical fallback when no explicit order file exists. Returned
// slice is sorted in place AND returned for chaining; callers may
// alternatively rely on this side effect.
func SortFilesByDisplayPath(files []File) []File {
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].DisplayPath < files[j].DisplayPath
	})
	return files
}

// digits returns the decimal digit count of n (n >= 1). Used to pad
// ordered-list line numbers so a 23-file constitution lines up: " 1.",
// " 2.", …, "10.", "11.", … instead of "1.\n2.\n…".
func digits(n int) int {
	if n < 10 {
		return 1
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}

// KnownTaskKinds is the closed set of values TaskKindFromTitle ever
// emits today. `mcp-stringwork constitution doctor` consults this to
// flag scope filters that reference an unreachable kind so a typo'd
// `task_kind: revue` (or a placeholder kind from a future ticket) is
// surfaced before it silently disables an entire source. Keep this in
// lock-step with TaskKindFromTitle when adding new emitted kinds.
var KnownTaskKinds = []string{"review"}

// TaskKindFromTitle is the v1 task classifier: a coarse string match
// over the title. Returns "review" when the title contains the word
// "review" (case-insensitive substring), otherwise "". A future
// iteration could swap this for an explicit task.kind field on
// domain.Task; sources scoped to task_kind: review keep working
// either way because the kind name does not change.
//
// Limitations:
//
//   - Substring matching means non-code-review titles ("performance
//     review", "schema review", "review the docs") also map to
//     "review". This is the intended v1 trade-off — better to err on
//     the side of attaching review checklists to a non-code review
//     than to silently drop them on a real one — but call sites that
//     need higher precision should switch to an explicit
//     task.kind/task.labels field once one exists.
//   - The classifier emits exactly one kind today: "review" (see
//     KnownTaskKinds). Configs listing additional kinds (e.g.
//     "bugfix", "refactor", "design-review") are dead config — the
//     heuristic never produces them. Extend KnownTaskKinds AND this
//     function in the same change before relying on a new kind.
func TaskKindFromTitle(title string) string {
	if strings.Contains(strings.ToLower(title), "review") {
		return "review"
	}
	return ""
}

// templateKindAliases maps task-templates template ids to the
// constitution scope kind they should activate. Keep narrow — a new
// alias here changes the rules every team's review-scoped sources
// attach to. The single entry today is the Phase-1 default
// "code-review" template; teams adding new templates should land the
// alias in the same change.
var templateKindAliases = map[string]string{
	"code-review": "review",
}

// TaskKindForTask returns the constitution scope kind for a task,
// given the template provenance and title. Implements the Phase-2
// alias rule:
//
//  1. If task.Template is set AND has a registered alias
//     (templateKindAliases), return that alias. This is the
//     deterministic path — once a task carries a template id, the
//     scope is decided structurally, not by re-classifying the title.
//  2. If task.Template is empty (the "IS NULL" / pre-deploy case),
//     fall back to TaskKindFromTitle. This protects pending tasks
//     created before the Template column existed: they keep matching
//     the title-based heuristic exactly as they did before Phase 2.
//  3. If task.Template is set but unknown (e.g. a hypothetical
//     "bug-investigation" template lands without an alias), return
//     "" — the explicit template wins over the title heuristic. This
//     prevents a future "bug investigation: review the logs" task
//     from accidentally pulling in review checklists.
//
// Adding a new alias is a one-line addition to templateKindAliases;
// landing a new alias in the same change as the new template id keeps
// the "task with template X gets which constitution rules?" question
// answerable from one file.
func TaskKindForTask(template, title string) string {
	if template == "" {
		return TaskKindFromTitle(title)
	}
	if alias, ok := templateKindAliases[template]; ok {
		return alias
	}
	return ""
}

// IsTemplateKindAlias reports whether s is the id of a known
// task-templates template that has a constitution scope alias. Used
// by `mcp-stringwork constitution doctor` to flag scope filters that
// reference a template id directly (e.g. `task_kind: ["code-review"]`)
// instead of the alias they should target (`task_kind: ["review"]`).
// Such config is dead — a scope filter only matches the alias, never
// the template id, so a typo here silently disables the source.
func IsTemplateKindAlias(s string) bool {
	_, ok := templateKindAliases[s]
	return ok
}
