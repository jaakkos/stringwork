package constitution

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DirSource is a constitution source backed by a directory on disk.
// Path is the directory to scan; Include is a list of glob patterns
// matched against base file names (e.g. "*.md"). When Include is empty
// the source defaults to "*.md", because that's the safe flavour: a
// constitution dir typically sits next to other repo files and we
// don't want to silently inject binaries or unrelated assets into
// every worker prompt.
//
// Include accepts arbitrary text formats when listed explicitly —
// e.g. `["*.md", "team-members.yaml"]` — and the matched files are
// inlined verbatim into spawn prompts. That is intentional: teams
// that want to ship structured metadata (yaml/json/toml) alongside
// their rules can do so. Two consequences worth knowing about:
//
//   - Anything you `Include` ends up in every spawn prompt that
//     resolves this source, so factor cost into the file selection.
//   - Never list a file that contains credentials, tokens, or
//     personally-identifying data. The constitution is shipped to
//     workers as plaintext and there is no scrubbing layer.
//
// If Path/constitution.md exists and contains a markdown ordered list
// of relative paths (e.g. `1. project.md`, `2. engineering.md`), that
// list defines the order. Otherwise the source falls back to
// alphabetical ordering of files matching Include. The constitution.md
// file itself is always emitted first when present, regardless of the
// ordered-list contents — it's the entry point the worker is told to
// read.
//
// SourceName overrides the displayed source label; when empty the
// source uses the directory basename. Scope is honoured here so a
// directory can be declared in config as "only attach for review
// tasks": a non-empty Scope.TaskKind/Scope.AgentRole filter restricts
// the source to matching scopes.
type DirSource struct {
	SourceName string
	Path       string
	Include    []string
	Scope      ScopeFilter
}

// ScopeFilter narrows when a source contributes. Empty slices mean
// "match every value of that dimension". A scope passes when each
// declared dimension contains the request's value. The check is
// case-insensitive and trims surrounding whitespace.
type ScopeFilter struct {
	TaskKind   []string
	AgentRoles []string
}

// matches returns true when the requested scope satisfies the filter.
// An empty filter (no TaskKind, no AgentRoles) always passes — the
// common case for unconditional sources.
func (s ScopeFilter) matches(scope Scope) bool {
	if !contains(s.TaskKind, scope.TaskKind) {
		return false
	}
	if !contains(s.AgentRoles, scope.AgentRole) {
		return false
	}
	return true
}

func contains(allowed []string, want string) bool {
	if len(allowed) == 0 {
		return true
	}
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), want) {
			return true
		}
	}
	return false
}

// Name returns the configured SourceName or the directory basename.
// Used for diagnostics ("which source did this file come from?") and
// MUST be stable across runs so `constitution show` is deterministic.
func (d *DirSource) Name() string {
	if d.SourceName != "" {
		return d.SourceName
	}
	if d.Path == "" {
		return "(unnamed)"
	}
	return filepath.Base(d.Path)
}

// List returns the resolved files for this directory, honouring the
// scope filter, ordering rules, and Include globs. Missing-dir is NOT
// an error: a user with no constitution dir simply contributes no
// files. Per-file read errors are also non-fatal so one unreadable
// file in a 20-file constitution doesn't blank out the rest — the bad
// file is just skipped (a future iteration could surface this via
// `constitution doctor`).
func (d *DirSource) List(scope Scope) ([]File, error) {
	if d == nil || d.Path == "" {
		return nil, nil
	}
	if !d.Scope.matches(scope) {
		return nil, nil
	}

	info, err := os.Stat(d.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", d.Path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", d.Path)
	}

	patterns := d.Include
	if len(patterns) == 0 {
		patterns = []string{"*.md"}
	}

	matches, err := scanDir(d.Path, patterns)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}

	ordered := orderFiles(d.Path, matches)
	sourceName := d.Name()

	var out []File
	for _, rel := range ordered {
		abs := filepath.Join(d.Path, rel)
		content, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		out = append(out, File{
			Source:      sourceName,
			Path:        abs,
			DisplayPath: collapseHome(abs),
			Content:     string(content),
		})
	}
	return out, nil
}

// scanDir walks d.Path (one level deep) and returns the relative file
// names matching any of the include globs. Subdirectories are
// intentionally not recursed: a constitution is a flat list of
// guidance documents, and recursion would surprise users whose rules
// dirs contain unrelated assets.
//
// Malformed glob patterns are surfaced as errors rather than silently
// dropped (filepath.Match returns ErrBadPattern for them). Otherwise
// a typo like `*[md` would yield zero matches with no diagnostic and
// the user would never know their include list was broken.
func scanDir(root string, patterns []string) ([]string, error) {
	for _, p := range patterns {
		if _, err := filepath.Match(p, ""); err != nil {
			return nil, fmt.Errorf("invalid include pattern %q: %w", p, err)
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", root, err)
	}
	seen := make(map[string]struct{})
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if _, dup := seen[name]; dup {
			continue
		}
		matched := false
		for _, p := range patterns {
			ok, err := filepath.Match(p, name)
			if err != nil {
				// Should be unreachable thanks to the upfront
				// validation above, but defend against future
				// pattern lists that bypass it.
				return nil, fmt.Errorf("match pattern %q against %q: %w", p, name, err)
			}
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// orderFiles places constitution.md (if present) first, then either
// honours an ordered list contained inside it, or falls back to
// alphabetical order for everything else.
func orderFiles(root string, names []string) []string {
	const entryPoint = "constitution.md"
	hasEntry := false
	pool := make(map[string]bool, len(names))
	for _, n := range names {
		pool[n] = true
		if n == entryPoint {
			hasEntry = true
		}
	}

	var ordered []string
	if hasEntry {
		ordered = append(ordered, entryPoint)
		listed := parseOrderedList(filepath.Join(root, entryPoint))
		used := map[string]bool{entryPoint: true}
		for _, p := range listed {
			if used[p] {
				continue
			}
			if !pool[p] {
				continue
			}
			ordered = append(ordered, p)
			used[p] = true
		}
		var rest []string
		for _, n := range names {
			if used[n] {
				continue
			}
			rest = append(rest, n)
		}
		sort.Strings(rest)
		ordered = append(ordered, rest...)
		return ordered
	}

	sort.Strings(names)
	return names
}

// orderedListLine matches markdown ordered-list entries that look like
//
//  1. path/to/file.md
//  2. `path/to/file.md`
//  3. *path*  (treated as path)
//
// It captures the path token after the "N." prefix, stripping
// surrounding backticks so users can use markdown's preferred inline
// code formatting in constitution.md.
var orderedListLine = regexp.MustCompile("^\\s*\\d+\\.\\s+`?([^`\\s]+)`?\\s*$")

// parseOrderedList reads the entry-point file and extracts referenced
// paths in declaration order. The caller already knows whether
// constitution.md exists (orderFiles only invokes this when the file
// is in the include set), so a read error here is unexpected — log it
// to the configured destination and return nil so the caller falls
// back to alphabetical order. Logging is the only signal an operator
// gets that their authored ordering was ignored; without it,
// diagnosing "why did rule X jump rule Y?" requires reading the
// source code.
func parseOrderedList(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("constitution: failed to read ordered-list manifest %s: %v (falling back to alphabetical)", path, err)
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		m := orderedListLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ref := strings.TrimSpace(m[1])
		if ref == "" {
			continue
		}
		ref = filepath.Clean(ref)
		out = append(out, ref)
	}
	return out
}

// collapseHome rewrites paths under $HOME with a leading "~/", which is
// how users see paths in their shells and config files. Falls back to
// the absolute path on any os.UserHomeDir error.
func collapseHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
