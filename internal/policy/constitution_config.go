package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jaakkos/stringwork/internal/constitution"
)

// ConstitutionConfig is the YAML mapping for the `constitution:` block.
// Profile is an optional path to a team-shipped profile file (R4.c)
// whose own `sources` are merged before this block's `sources`. Sources
// is the user-level list; entries are typed (`type: dir` or
// `type: git`), and unknown types yield a clear error rather than a
// silent drop so config typos surface immediately.
type ConstitutionConfig struct {
	Profile string                     `yaml:"profile,omitempty"`
	Sources []ConstitutionSourceConfig `yaml:"sources,omitempty"`
}

// ConstitutionSourceConfig is one entry in `constitution.sources`. The
// shape is intentionally a tagged-union (Type discriminator + per-type
// fields) so we don't need separate YAML mapping types for each source
// flavour — most users will only ever write `type: dir` blocks.
//
// Field semantics by Type:
//
//	dir: Path (required), Include (default *.md). Reads on every
//	     resolve from the local filesystem; intended for paths a
//	     developer already keeps in sync via git pull.
//	git: Repo (required), Ref (default HEAD), Paths (default repo
//	     root), Include (default *.md), CacheDir (required).
//	     Network-touching sync is explicit (`constitution sync`).
//
// Scope (optional) narrows which task-kinds / agent-roles a source
// attaches to — see constitution.ScopeFilter for matching semantics.
type ConstitutionSourceConfig struct {
	Name     string                   `yaml:"name"`
	Type     string                   `yaml:"type"`
	Path     string                   `yaml:"path,omitempty"`
	Include  []string                 `yaml:"include,omitempty"`
	Repo     string                   `yaml:"repo,omitempty"`
	Ref      string                   `yaml:"ref,omitempty"`
	Paths    []string                 `yaml:"paths,omitempty"`
	CacheDir string                   `yaml:"cache_dir,omitempty"`
	Scope    *ConstitutionScopeConfig `yaml:"scope,omitempty"`
}

// ConstitutionScopeConfig narrows when a source contributes. Empty
// slices mean "match every value of that dimension" — the common case
// for unconditional sources.
type ConstitutionScopeConfig struct {
	TaskKind   []string `yaml:"task_kind,omitempty"`
	AgentRoles []string `yaml:"agent_roles,omitempty"`
}

// toSource validates a single declaration and converts it into a
// concrete constitution.Source. profileDir is the directory of the
// loading profile file, used to expand $PROFILE_DIR; pass "" when the
// declaration came from the user's primary config.yaml (which doesn't
// support $PROFILE_DIR — that token is profile-specific).
//
// A nil source + nil error is a deliberate skip (e.g. an unset
// optional). A non-nil error indicates a malformed declaration and
// the caller logs and continues; the bad entry never reaches the
// resolver.
func (c ConstitutionSourceConfig) toSource(profileDir string) (constitution.Source, error) {
	if strings.TrimSpace(c.Name) == "" {
		return nil, fmt.Errorf("source missing name")
	}
	switch strings.ToLower(strings.TrimSpace(c.Type)) {
	case "", "dir":
		if c.Path == "" {
			return nil, fmt.Errorf("source %q (dir): path is required", c.Name)
		}
		path, err := expandPath(c.Path, profileDir)
		if err != nil {
			return nil, fmt.Errorf("source %q: expand path: %w", c.Name, err)
		}
		return &constitution.DirSource{
			SourceName: c.Name,
			Path:       path,
			Include:    c.Include,
			Scope:      toScopeFilter(c.Scope),
		}, nil
	case "git":
		if c.Repo == "" {
			return nil, fmt.Errorf("source %q (git): repo is required", c.Name)
		}
		if c.CacheDir == "" {
			return nil, fmt.Errorf("source %q (git): cache_dir is required", c.Name)
		}
		cache, err := expandPath(c.CacheDir, profileDir)
		if err != nil {
			return nil, fmt.Errorf("source %q: expand cache_dir: %w", c.Name, err)
		}
		// Git source `paths` are *repo-relative*, not filesystem
		// paths — they identify subdirectories *inside* the cloned
		// CacheDir. Running them through expandPath turned a
		// repo-relative entry like "rules" into the host's CWD plus
		// "rules" (an absolute path that points outside the cache),
		// and a leading `~` even produced a HOME-relative path —
		// either bypassing the cache entirely or, with a malicious
		// profile, reading arbitrary host files when joined to the
		// cache via filepath.Join. The validation below rejects
		// empty / absolute / traversing entries up front so a config
		// typo cannot silently widen the source's footprint; the
		// in-process containment check happens again in
		// GitSource.List() as defence in depth.
		validatedPaths := make([]string, 0, len(c.Paths))
		for _, p := range c.Paths {
			trimmed := strings.TrimSpace(p)
			if trimmed == "" {
				return nil, fmt.Errorf("source %q (git): paths entry is empty", c.Name)
			}
			if filepath.IsAbs(trimmed) {
				return nil, fmt.Errorf("source %q (git): paths entry %q must be repo-relative, not absolute", c.Name, trimmed)
			}
			cleaned := filepath.Clean(trimmed)
			if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, `..\`) {
				return nil, fmt.Errorf("source %q (git): paths entry %q escapes the cloned repo via parent reference", c.Name, trimmed)
			}
			validatedPaths = append(validatedPaths, cleaned)
		}
		return &constitution.GitSource{
			SourceName: c.Name,
			Repo:       c.Repo,
			Ref:        c.Ref,
			Paths:      validatedPaths,
			Include:    c.Include,
			CacheDir:   cache,
			Scope:      toScopeFilter(c.Scope),
		}, nil
	default:
		return nil, fmt.Errorf("source %q: unknown type %q (expected \"dir\" or \"git\")", c.Name, c.Type)
	}
}

func toScopeFilter(s *ConstitutionScopeConfig) constitution.ScopeFilter {
	if s == nil {
		return constitution.ScopeFilter{}
	}
	return constitution.ScopeFilter{
		TaskKind:   append([]string(nil), s.TaskKind...),
		AgentRoles: append([]string(nil), s.AgentRoles...),
	}
}

// expandPath turns a config-form path into an absolute filesystem
// path, supporting:
//
//   - Leading `~` / `~/` → user home directory.
//   - Environment variables in `$VAR` and `${VAR}` form via os.ExpandEnv.
//   - `$PROFILE_DIR` → the directory of the loading profile file
//     (only meaningful when called from a profile loader). Using this
//     token from the user's primary config.yaml is a config error —
//     the loader passes profileDir="" in that case and we surface a
//     clear diagnostic rather than letting os.ExpandEnv silently
//     strip the token and leave a half-resolved path like "/rules"
//     pointing at nothing.
//
// The result is always absolute; relative paths are resolved against
// the current working directory by os.Abs.
func expandPath(raw, profileDir string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}

	// $PROFILE_DIR is profile-only. Substitute first so subsequent
	// expansion can still see ~ and other env vars relative to it.
	if profileDir != "" {
		p = strings.ReplaceAll(p, "${PROFILE_DIR}", profileDir)
		p = strings.ReplaceAll(p, "$PROFILE_DIR", profileDir)
	} else if strings.Contains(p, "$PROFILE_DIR") || strings.Contains(p, "${PROFILE_DIR}") {
		// Catch the misuse before os.ExpandEnv silently strips it
		// and produces a confusing "/rules"-style absolute path.
		// Match both ${PROFILE_DIR} (brace) and $PROFILE_DIR (bare)
		// — strings.Contains for the bare form does not match the
		// braced form because '{' breaks the substring.
		return "", fmt.Errorf("$PROFILE_DIR is only valid in profile files (stringwork.profile.yaml); main config.yaml must use absolute or ~-prefixed paths")
	}

	p = os.ExpandEnv(p)

	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		switch {
		case p == "~":
			p = home
		case strings.HasPrefix(p, "~/"):
			p = filepath.Join(home, p[2:])
		default:
			// e.g. "~alice/rules" — POSIX user-home expansion is
			// not supported. Falling through to filepath.Abs would
			// silently anchor the path to the daemon's CWD, which
			// is never what `~alice` means anywhere on POSIX, so
			// the consumer would surface a confusing
			// "directory not found" rather than the actual
			// configuration mistake.
			return "", fmt.Errorf("path %q: ~user/... home expansion is not supported; use absolute or ~/ paths", raw)
		}
	}

	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("absolutize %q: %w", p, err)
		}
		p = abs
	}
	return filepath.Clean(p), nil
}
