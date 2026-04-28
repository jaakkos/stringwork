// Constitution CLI subcommands. These do NOT talk to the daemon: they
// read/write the on-disk constitution directory and resolve sources via
// the loaded policy. Available even when no daemon is running, so users
// can scaffold rules before they spawn their first worker.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jaakkos/stringwork/internal/constitution"
	"github.com/jaakkos/stringwork/internal/policy"
)

// runConstitutionCommand dispatches `mcp-stringwork constitution <subcommand>`.
// Exits the process on terminal errors so the caller (main) can return early.
func runConstitutionCommand(args []string) {
	if len(args) == 0 {
		printConstitutionUsage(os.Stderr)
		os.Exit(1)
	}
	switch args[0] {
	case "init":
		runConstitutionInit(args[1:])
	case "show":
		runConstitutionShow(args[1:])
	case "sync":
		runConstitutionSync(args[1:])
	case "doctor":
		runConstitutionDoctor(args[1:])
	case "-h", "--help", "help":
		printConstitutionUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown constitution subcommand: %s\n\n", args[0])
		printConstitutionUsage(os.Stderr)
		os.Exit(1)
	}
}

func printConstitutionUsage(w io.Writer) {
	fmt.Fprint(w, `usage: mcp-stringwork constitution <subcommand> [flags]

Subcommands:
  init   Scaffold the per-user constitution directory with a default
         constitution.md entry point and a starter engineering.md rule
         file. Idempotent by default — files that already exist are left
         alone unless --force is passed.

           --force        Overwrite existing files in the directory.
           --dir PATH     Override the target directory (default:
                          ~/.config/stringwork/constitution).

  show   Resolve all configured constitution sources and print what a
         worker would see for the given scope. Defaults to the preamble
         pointer block (the same text injected into MCP responses).

           --inline       Print the full file contents instead of just
                          the pointer block (mirrors what is inlined into
                          worker spawn prompts).
           --task-kind K  Apply scope filtering as if the request came
                          from a task whose kind is K (e.g. "review").
           --agent ROLE   Apply scope filtering as if the request came
                          from an agent of the given role.

  sync   Pull every configured 'git'-typed source into its cache_dir.
         No-op for 'dir' sources (those follow the user's own git pull
         on the team devtools repo). Warns when no git sources are
         configured.

           [name]         Optional source name to sync. When omitted,
                          every git source is synced in declaration
                          order.

  doctor Validate every configured source. For 'dir' sources: the
         path must exist, be a directory, and contain at least one
         file matching its include globs. For 'git' sources: the
         cache must be populated (suggest 'sync' otherwise) and the
         remote must respond to ls-remote. Exits non-zero when any
         source has an ERROR; warnings still allow exit 0 so doctor
         is safe to wire into pre-spawn checks.

The constitution directory is the built-in user-level source. Team
sources can be added via the 'constitution:' block in config.yaml, or
shared via a 'constitution.profile' file shipped alongside a team's
devtools repository.

`)
}

// runConstitutionInit handles `constitution init`. Creates the target
// directory if missing and writes a default scaffold (constitution.md +
// engineering.md) when those files don't already exist.
func runConstitutionInit(args []string) {
	dir := flagValue(args, "--dir")
	force := hasArg(args, "--force")
	if dir == "" {
		dir = policy.GlobalConstitutionDir()
	}
	created, skipped, err := scaffoldConstitution(dir, force)
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Printf("Constitution directory: %s\n", dir)
	for _, path := range created {
		fmt.Printf("  created  %s\n", filepath.Base(path))
	}
	for _, path := range skipped {
		fmt.Printf("  skipped  %s (exists; rerun with --force to overwrite)\n", filepath.Base(path))
	}
	if len(created) == 0 && len(skipped) == 0 {
		fmt.Println("  (no files written)")
	}
}

// runConstitutionShow handles `constitution show`. Resolves the policy's
// configured sources and prints the preamble (default) or the inlined
// full content (--inline). Supports task-kind / agent scope filters so
// users can preview what a review-only source would do without having
// to spawn an actual review task.
func runConstitutionShow(args []string) {
	scope := constitution.Scope{
		TaskKind:  strings.TrimSpace(flagValue(args, "--task-kind")),
		AgentRole: strings.TrimSpace(flagValue(args, "--agent")),
	}
	inline := hasArg(args, "--inline")

	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)

	if code := renderConstitutionShow(pol.ConstitutionSources(), scope, inline, pol.ConstitutionDir(), os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

// renderConstitutionShow is the testable core of `constitution show`.
// It drives Resolve, surfaces partial failures as stderr warnings, and
// renders surviving files to stdout. Returns 0 on success, 1 only when
// no files resolved AND a real error occurred.
//
// Partial-resolve handling mirrors the hot-path callers (claim_next,
// get_work_context, worker spawn-prompt): when one source errors but
// others resolve cleanly, surviving files are still rendered and the
// failure is reported to stderr as a warning. Hard-exiting on any err
// would hide the rest of the constitution from a user who is running
// `show` precisely to debug the broken source.
func renderConstitutionShow(srcs []constitution.Source, scope constitution.Scope, inline bool, builtinDir string, stdout, stderr io.Writer) int {
	files, err := constitution.Resolve(srcs, scope)
	// Emit the partial-failure warning only when at least one source
	// survived. With zero survivors the same content reappears as a
	// hard error below — printing both would duplicate the message.
	if err != nil && len(files) > 0 {
		fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	if len(files) == 0 {
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "No constitution files resolved.")
		fmt.Fprintf(stdout, "Built-in directory: %s\n", builtinDir)
		fmt.Fprintln(stdout, "Tip: run `mcp-stringwork constitution init` to create a starter set.")
		return 0
	}

	if inline {
		fmt.Fprint(stdout, constitution.BuildInline(files))
		return 0
	}

	fmt.Fprint(stdout, constitution.BuildPreamble(files))
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Sources:")
	for _, name := range uniqueSourceNames(files) {
		fmt.Fprintf(stdout, "  - %s\n", name)
	}
	return 0
}

// runConstitutionSync handles `constitution sync [name]`. Walks every
// configured source, picks out the git ones (dir sources are live and
// don't need syncing), and runs Sync() on each. Optional name filter
// targets a single source so a slow remote doesn't block all the
// others.
func runConstitutionSync(args []string) {
	target := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			target = a
			break
		}
	}

	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)
	srcs := pol.ConstitutionSources()

	gitSources := selectGitSources(srcs, target)
	if len(gitSources) == 0 {
		if target != "" {
			cliDie(fmt.Sprintf("no git source named %q found", target))
		}
		fmt.Println("No git sources configured. ('dir' sources are live; nothing to sync.)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	failures := 0
	for _, g := range gitSources {
		fmt.Printf("Syncing %s … ", g.Name())
		if err := g.Sync(ctx); err != nil {
			failures++
			fmt.Printf("FAILED\n  %v\n", err)
			continue
		}
		fmt.Println("OK")
	}
	if failures > 0 {
		os.Exit(1)
	}
}

// runConstitutionDoctor handles `constitution doctor`. Each source is
// validated and a verdict line is printed. Exit code is 0 unless at
// least one source produced an ERROR — warnings (e.g. "git source
// hasn't been synced yet") are non-fatal so doctor can be wired into
// pre-flight checks without causing spurious failures.
func runConstitutionDoctor(args []string) {
	_ = args // no flags yet; reserved for future --task-kind / --agent

	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)
	srcs := pol.ConstitutionSources()

	if len(srcs) == 0 {
		fmt.Println("No constitution sources configured.")
		fmt.Printf("Built-in directory: %s\n", pol.ConstitutionDir())
		return
	}

	hasError := false
	for _, src := range srcs {
		switch s := src.(type) {
		case *constitution.DirSource:
			if warning := doctorScopeWarning(s.Name(), s.Scope); warning != "" {
				fmt.Println(warning)
			}
			if err := doctorDirSource(s); err != nil {
				// The auto-injected "global" source points at
				// ~/.config/stringwork/constitution. Treat a
				// missing directory there as a WARN with a hint
				// to run `constitution init` — otherwise doctor
				// exits non-zero on a fresh install before the
				// user has had a chance to scaffold anything.
				if s.Name() == "global" && errors.Is(err, fs.ErrNotExist) {
					fmt.Printf("[WARN]  %-20s %v (run `mcp-stringwork constitution init` to scaffold)\n", s.Name(), err)
				} else {
					hasError = true
					fmt.Printf("[ERROR] %-20s %v\n", s.Name(), err)
				}
			} else {
				fmt.Printf("[OK]    %-20s %s\n", s.Name(), s.Path)
			}
		case *constitution.GitSource:
			if warning := doctorScopeWarning(s.Name(), s.Scope); warning != "" {
				fmt.Println(warning)
			}
			// Each git source gets its own deadline. A shared
			// 1-minute context across N sources meant a single
			// slow remote could starve every later source's
			// ls-remote, producing spurious ERROR verdicts. 30s
			// is enough headroom for a healthy ls-remote on a
			// large repo behind a slow corp HTTPS proxy (TLS
			// interception + first-contact pack negotiation
			// can push <1s of network into the 10-20s range)
			// while still bounding a stuck remote.
			//
			// IIFE so `defer cancel()` fires on every exit
			// path of the body — including a panic inside
			// doctorGitSource — without leaking N timers
			// across loop iterations the way a function-scoped
			// defer would.
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				level, msg := doctorGitSource(ctx, s)
				fmt.Printf("[%s] %-20s %s\n", level, s.Name(), msg)
				if level == "ERROR" {
					hasError = true
				}
			}()
		default:
			fmt.Printf("[OK]    %-20s (custom source)\n", src.Name())
		}
	}

	if hasError {
		os.Exit(1)
	}
}

// doctorScopeWarning returns a non-empty WARN line when the source's
// scope filter references a TaskKind value the in-process classifier
// (constitution.TaskKindFromTitle) does not emit. Such a filter is
// dead config — the source never resolves, but neither the resolver
// nor `constitution show` flag it because both treat "no match" as a
// silent skip. Doctor is the only place an operator finds out.
//
// Returns "" when the filter is empty (the common, always-applicable
// case), when every declared kind is reachable, or when the source
// declares no kind filter at all. Comparison is case-insensitive
// with whitespace trimmed, mirroring constitution.ScopeFilter.matches.
//
// Phase 2 addendum: when an unreachable kind happens to be the id of
// a known task-templates template that has an alias (e.g.
// "code-review" → "review"), the warning is upgraded with a hint
// pointing at the alias. This is the most common authoring mistake
// once teams start writing template-aware profiles.
func doctorScopeWarning(name string, scope constitution.ScopeFilter) string {
	if len(scope.TaskKind) == 0 {
		return ""
	}
	known := make(map[string]struct{}, len(constitution.KnownTaskKinds))
	for _, k := range constitution.KnownTaskKinds {
		known[strings.ToLower(strings.TrimSpace(k))] = struct{}{}
	}
	var (
		unknown    []string
		aliasHints []string
	)
	for _, k := range scope.TaskKind {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		if _, ok := known[key]; ok {
			continue
		}
		unknown = append(unknown, k)
		if constitution.IsTemplateKindAlias(key) {
			aliasHints = append(aliasHints,
				fmt.Sprintf("%q is a template id; use task_kind %q instead",
					k, constitution.TaskKindForTask(key, "")),
			)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	emitted := strings.Join(constitution.KnownTaskKinds, ", ")
	msg := fmt.Sprintf(
		"[WARN]  %-20s scope.task_kind references unreachable kind(s) %v — classifier emits only [%s]; the source will never resolve",
		name, unknown, emitted,
	)
	if len(aliasHints) > 0 {
		msg += " — " + strings.Join(aliasHints, "; ")
	}
	return msg
}

// selectGitSources flattens the resolved source list down to the
// GitSource entries, optionally filtered by name. Other source types
// are silently ignored — sync is git-only and the doctor / show
// subcommands cover the rest.
func selectGitSources(srcs []constitution.Source, name string) []*constitution.GitSource {
	var out []*constitution.GitSource
	for _, s := range srcs {
		g, ok := s.(*constitution.GitSource)
		if !ok {
			continue
		}
		if name != "" && g.Name() != name {
			continue
		}
		out = append(out, g)
	}
	return out
}

// doctorDirSource verifies a DirSource resolves to a real directory
// containing at least one file that matches its include globs. It
// delegates to the source's own List method so that bad globs and
// other List-time errors surface here exactly as a worker would see
// them — rather than silently passing the doctor while breaking at
// runtime. Missing-dir keeps its own classification (returned as
// fs.ErrNotExist) so the caller can downgrade it to a WARN with the
// `constitution init` hint for the auto-injected global source.
//
// The scope filter is intentionally bypassed: doctor is asking "is
// this source configured correctly?", which is a static config
// question. A scoped source (e.g. review-only) must still resolve to
// at least one file when stripped of its scope, otherwise the team's
// scoped path/glob is silently broken until someone files a review
// task. We clone the source with a zeroed ScopeFilter so List() walks
// every other code path a real worker request would take.
func doctorDirSource(d *constitution.DirSource) error {
	if d == nil || d.Path == "" {
		return fmt.Errorf("path is empty")
	}
	info, err := os.Stat(d.Path)
	if err != nil {
		return fmt.Errorf("path %s: %w", d.Path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path %s is not a directory", d.Path)
	}
	probe := *d
	probe.Scope = constitution.ScopeFilter{}
	files, err := probe.List(constitution.Scope{})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		patterns := d.Include
		if len(patterns) == 0 {
			patterns = []string{"*.md"}
		}
		return fmt.Errorf("no files match include patterns %v in %s", patterns, d.Path)
	}
	return nil
}

// doctorGitSource probes the cache plus the remote, then exercises
// the source's List method so configuration faults — bad globs,
// path-traversal escapes, missing subpaths — surface at doctor time
// rather than silently failing at worker spawn. Empty cache is still
// a non-fatal WARN because the user may not have run `sync` yet.
//
// Scope is bypassed for the same reason as doctorDirSource: the
// question is "does this source produce files when asked?" not "does
// it produce files for the empty default scope?". A scoped review
// source must still pass doctor when no review task is in flight.
func doctorGitSource(ctx context.Context, g *constitution.GitSource) (level, msg string) {
	if g.Repo == "" {
		return "ERROR", "repo is required"
	}
	if g.CacheDir == "" {
		return "ERROR", "cache_dir is required"
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "ERROR", "git binary not found in PATH"
	}

	cacheEmpty := false
	if _, err := os.Stat(filepath.Join(g.CacheDir, ".git")); err != nil {
		cacheEmpty = true
	}

	args := []string{"ls-remote", "--exit-code", g.Repo}
	if g.Ref != "" {
		args = append(args, g.Ref)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "ERROR", fmt.Sprintf("ls-remote %s failed: %v\n%s", g.Repo, err, string(out))
	}

	header := fmt.Sprintf("%s @ %s", g.Repo, g.Ref)

	if cacheEmpty {
		return "WARN", header + " (cache empty — run `mcp-stringwork constitution sync` to populate)"
	}
	probe := *g
	probe.Scope = constitution.ScopeFilter{}
	files, listErr := probe.List(constitution.Scope{})
	if listErr != nil {
		return "ERROR", fmt.Sprintf("%s — list failed: %v", header, listErr)
	}
	if len(files) == 0 {
		return "WARN", header + " (cache populated but resolves to zero files — check `paths`/include globs)"
	}
	return "OK", fmt.Sprintf("%s (%d file(s))", header, len(files))
}

// scaffoldConstitution writes the default constitution scaffold into
// dir. Returns the absolute paths of files actually created and files
// skipped because they already existed (and force was not set). The
// directory is created if missing.
func scaffoldConstitution(dir string, force bool) (created, skipped []string, err error) {
	if dir == "" {
		return nil, nil, fmt.Errorf("constitution directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create dir %s: %w", dir, err)
	}
	scaffold := []struct {
		name    string
		content string
	}{
		{name: "constitution.md", content: defaultConstitutionEntry},
		{name: "engineering.md", content: defaultEngineeringRules},
	}
	for _, f := range scaffold {
		path := filepath.Join(dir, f.name)
		if !force {
			if _, statErr := os.Stat(path); statErr == nil {
				skipped = append(skipped, path)
				continue
			}
		}
		if writeErr := os.WriteFile(path, []byte(f.content), 0o644); writeErr != nil {
			return created, skipped, fmt.Errorf("write %s: %w", path, writeErr)
		}
		created = append(created, path)
	}
	return created, skipped, nil
}

// uniqueSourceNames returns the distinct source labels from files in
// first-seen order. Used by `constitution show` to summarise which
// sources contributed without listing each file individually.
func uniqueSourceNames(files []constitution.File) []string {
	seen := make(map[string]struct{}, len(files))
	var names []string
	for _, f := range files {
		if f.Source == "" {
			continue
		}
		if _, dup := seen[f.Source]; dup {
			continue
		}
		seen[f.Source] = struct{}{}
		names = append(names, f.Source)
	}
	sort.Strings(names)
	return names
}

// hasArg reports whether `key` appears as a bare flag in args (e.g.
// `--force`). It does NOT match `--force=value` because the constitution
// flags are pure booleans.
func hasArg(args []string, key string) bool {
	for _, a := range args {
		if a == key {
			return true
		}
	}
	return false
}

// defaultConstitutionEntry is the seed content for `constitution.md`.
// It documents the file format (an ordered markdown list) and includes
// a pointer to the starter engineering.md so a fresh scaffold has a
// working two-file resolution path out of the box.
const defaultConstitutionEntry = `# Stringwork Constitution

This file is the entry point of your Stringwork constitution. Every
worker reads it before doing any task. Think of it as the persistent
"team rules" prepended to every prompt.

## Reading order

The ordered list below defines the precedence when files conflict —
earlier files win. Add or reorder entries as your team's rules grow:

1. ` + "`engineering.md`" + `

## How this directory works

- Add Markdown files alongside this one. Any ` + "`*.md`" + ` is picked up.
- Files that are not in the ordered list above appear after the listed
  files, in alphabetical order.
- Run ` + "`mcp-stringwork constitution show`" + ` to preview what a worker
  will see; pass ` + "`--inline`" + ` to view the full inlined prompt.
`

// defaultEngineeringRules is a deliberately short starter rule file —
// users are expected to delete most of it. The goal is to give them a
// concrete, non-empty file to edit so the very first `constitution
// show` produces meaningful output.
const defaultEngineeringRules = `# Engineering rules

Edit or replace these with your team's actual rules. They are inlined
into every worker prompt; keep them short and specific.

- Always run the project's lint and test commands before reporting a
  task as complete.
- Never commit secrets or large generated artefacts.
- When in doubt, prefer the smallest possible change that satisfies
  the task.
`
