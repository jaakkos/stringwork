package constitution

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GitSource is a constitution source backed by a remote git repository.
// The remote is cloned into CacheDir (shallow, single-branch) and every
// List call scans the cache directory — no network I/O happens during
// resolve. Refresh is explicit via Sync (typically wired into
// `mcp-stringwork constitution sync`); rationale: auto-pulling on every
// claim adds network latency, surprise behaviour, and auth-prompt risk
// to a hot path that must stay local.
//
// Repo is anything `git clone` understands (HTTPS URL, SSH URL, file://
// path, absolute path on disk). Ref is the branch or tag to track;
// empty defaults to the remote's HEAD via `git fetch` + reset to
// FETCH_HEAD. Paths is the list of sub-paths within the working tree
// to expose; each is treated as its own one-level DirSource-style
// listing, with declaration order preserved across paths. An empty
// Paths means "the repo root".
//
// Include narrows the file-name globs (default "*.md", same as
// DirSource). Scope works identically to DirSource. SourceName
// overrides the displayed source label; when empty the source uses the
// last component of CacheDir.
type GitSource struct {
	SourceName string
	Repo       string
	Ref        string
	Paths      []string
	Include    []string
	CacheDir   string
	Scope      ScopeFilter
}

// Name returns the configured SourceName or the basename of CacheDir.
// Used for diagnostics ("which source did this file come from?") and
// MUST be stable across runs so `constitution show` is deterministic.
func (g *GitSource) Name() string {
	if g.SourceName != "" {
		return g.SourceName
	}
	if g.CacheDir == "" {
		return "(unnamed)"
	}
	return filepath.Base(g.CacheDir)
}

// List returns the resolved files for this git source. It does NOT
// touch the network: an absent or empty cache directory yields zero
// files (with no error) so the resolver doesn't blow up before the
// user has run `constitution sync`. Per-file read errors inherit
// DirSource's "skip and continue" behaviour to keep one bad file from
// blanking the rest of the source.
func (g *GitSource) List(scope Scope) ([]File, error) {
	if g == nil {
		return nil, nil
	}
	if !g.Scope.matches(scope) {
		return nil, nil
	}
	if g.CacheDir == "" {
		return nil, nil
	}
	info, err := os.Stat(g.CacheDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", g.CacheDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", g.CacheDir)
	}

	paths := g.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}

	// Resolve CacheDir to an absolute, lexically-cleaned root *once*
	// and require every joined sub-path to remain inside it. We use
	// filepath.Abs + Clean rather than EvalSymlinks: Abs only
	// absolutises (a symlinked CacheDir keeps its symlink shape),
	// but the textual containment check below is still safe because
	// (a) the loader rejects abs/`..` entries up front, and (b)
	// filepath.Rel(cleanCacheDir, full) is purely textual — both
	// sides go through the same Abs+Clean transform. A symlink
	// PLANTED INSIDE the cache (e.g. cache/foo -> /etc) would
	// bypass this textual check; if that ever becomes a real threat
	// model, switch to EvalSymlinks at the cost of one stat per
	// List call (this is not a hot path inside the global lock).
	//
	// The loader-side validation in policy.ConstitutionSourceConfig
	// rejects malformed entries up front, but List() runs every time
	// the resolver is called and is the last line of defence: a
	// misconfigured cache directory must never cause the resolver to
	// read arbitrary host files via a crafted path.
	cleanCacheDir, err := filepath.Abs(g.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("git source %q: resolve cache dir: %w", g.Name(), err)
	}
	cleanCacheDir = filepath.Clean(cleanCacheDir)

	sourceName := g.Name()
	// Accumulate per-path errors instead of bailing on the first
	// failure. The package-level partial-failure contract that
	// constitution.Resolve documents — "earlier files survive when
	// a later source breaks" — extends to "earlier paths within a
	// source survive when a later path breaks". A typo on the
	// second of three paths must not nuke the rules from the first
	// one. errors.Join + the resolver's existing best-effort wiring
	// surfaces the broken path to operators while keeping good
	// content flowing to workers.
	var (
		out  []File
		errs []error
	)
	for _, sub := range paths {
		if filepath.IsAbs(sub) {
			errs = append(errs, fmt.Errorf("git source %q: paths entry %q must be repo-relative, not absolute", sourceName, sub))
			continue
		}
		full := filepath.Clean(filepath.Join(cleanCacheDir, sub))
		rel, err := filepath.Rel(cleanCacheDir, full)
		if err != nil {
			errs = append(errs, fmt.Errorf("git source %q path %q: containment check failed: %w", sourceName, sub, err))
			continue
		}
		// rel == ".." or starts with "../" means the join landed
		// outside CacheDir. Surface as a per-path error and keep
		// going so a single misconfiguration is loud but does not
		// blank out the rest of the source.
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			errs = append(errs, fmt.Errorf("git source %q: paths entry %q escapes cache directory", sourceName, sub))
			continue
		}
		// DirSource handles scope filtering, glob matching, ordering
		// and read errors. Reusing it here keeps GitSource focused on
		// clone/refresh; the inner DirSource borrows the parent
		// SourceName so files report the configured git source label
		// instead of the on-disk subdirectory name.
		inner := &DirSource{
			SourceName: sourceName,
			Path:       full,
			Include:    g.Include,
		}
		files, listErr := inner.List(scope)
		if listErr != nil {
			errs = append(errs, fmt.Errorf("git source %q path %q: %w", sourceName, sub, listErr))
			continue
		}
		out = append(out, files...)
	}
	return out, errors.Join(errs...)
}

// Sync performs the network-touching half of the source: clone if
// CacheDir is empty, otherwise `git fetch` + hard reset to the tracked
// ref. Run from `mcp-stringwork constitution sync`. Returns a wrapped
// error with the underlying git invocation's combined output so users
// can diagnose auth failures, missing remotes, and similar.
//
// Sync is intentionally idempotent and safe to re-run: a successful
// run leaves the working tree at the tip of Ref; a failed run leaves
// the cache in whatever state it was before (the reset is the very
// last step).
func (g *GitSource) Sync(ctx context.Context) error {
	if g.Repo == "" {
		return fmt.Errorf("git source %q: missing repo", g.Name())
	}
	if g.CacheDir == "" {
		return fmt.Errorf("git source %q: missing cache_dir", g.Name())
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git source %q: git binary not found in PATH: %w", g.Name(), err)
	}

	// Clone if the cache is empty / missing. Treat "no .git
	// subdirectory" as "not a clone" — covers both first-time setup
	// and a half-deleted cache that the user wants to re-create.
	if _, err := os.Stat(filepath.Join(g.CacheDir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(g.CacheDir), 0o755); err != nil {
			return fmt.Errorf("create cache parent: %w", err)
		}
		args := []string{"clone", "--depth", "1"}
		if g.Ref != "" {
			args = append(args, "--branch", g.Ref, "--single-branch")
		}
		args = append(args, g.Repo, g.CacheDir)
		if out, err := runGit(ctx, "", args...); err != nil {
			return fmt.Errorf("git source %q: clone failed: %w\n%s", g.Name(), err, out)
		}
		return nil
	}

	// Already cloned: fetch the tracked ref, then hard-reset onto it.
	// `--depth 1` keeps the cache shallow. We deliberately use
	// origin/<ref> when Ref is set so the reset target is unambiguous;
	// FETCH_HEAD is the fallback for "track whatever HEAD points at".
	target := "FETCH_HEAD"
	fetchArgs := []string{"fetch", "--depth", "1", "origin"}
	if g.Ref != "" {
		fetchArgs = append(fetchArgs, g.Ref)
		target = "origin/" + g.Ref
	}
	if out, err := runGit(ctx, g.CacheDir, fetchArgs...); err != nil {
		return fmt.Errorf("git source %q: fetch failed: %w\n%s", g.Name(), err, out)
	}
	if out, err := runGit(ctx, g.CacheDir, "reset", "--hard", target); err != nil {
		return fmt.Errorf("git source %q: reset failed: %w\n%s", g.Name(), err, out)
	}
	return nil
}

// runGit invokes the user's git binary with the supplied args and
// returns the combined output. Centralised so we only have one place
// to grow timeouts, env scrubbing, or output redaction in the future.
//
// If the caller's context has no deadline, runGit installs a
// defensive 5-minute fallback so a stuck remote can't hang the
// process indefinitely. Callers that already wrap with a deadline
// (e.g. runConstitutionSync's 5-minute parent ctx) are unaffected —
// the existing deadline takes precedence.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
