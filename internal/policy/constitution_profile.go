package policy

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/jaakkos/stringwork/internal/constitution"
)

// ConstitutionProfile is the on-disk shape of a team-shared rule
// profile (typically committed at <devtools-repo>/stringwork.profile.yaml).
// It mirrors the user-level `constitution.sources` block so a team can
// publish a profile and a user can opt in with a single config line:
//
//	constitution:
//	  profile: ~/Development/regfin-devtools/stringwork.profile.yaml
//
// The big difference from user-level sources is path expansion: the
// profile loader substitutes `$PROFILE_DIR` with the directory of the
// profile file itself, so a profile can ship paths like
// `$PROFILE_DIR/rules` that resolve correctly no matter where the user
// has cloned the team's devtools repo.
type ConstitutionProfile struct {
	Sources []ConstitutionSourceConfig `yaml:"sources,omitempty"`

	// TaskTemplates is accepted-but-ignored at the constitution loader.
	// A team profile can ship both `sources:` (constitution) and
	// `task_templates:` (task-template overlays) in the same file --
	// the task-template subsystem reads its own block via
	// LoadTaskTemplateProfile. Keeping the field declared here means the
	// strict-decode enabled below still catches genuine top-level typos
	// (e.g. `tsk_templates:`) without rejecting the documented co-located
	// shape. yaml.Node accepts any sub-tree without validating its inner
	// fields, which is what we want -- inner typos are caught when the
	// task-template loader strict-decodes its own block.
	TaskTemplates yaml.Node `yaml:"task_templates,omitempty"`
}

// LoadConstitutionProfile reads and parses a profile file. The
// returned `dir` is the absolute directory of the profile file —
// callers pass it to ConstitutionSourceConfig.toSource so $PROFILE_DIR
// expands correctly. A clear error is returned when:
//
//   - The file does not exist.
//   - The YAML is malformed.
//   - The path is empty (caller bug).
//
// Bad individual source entries are NOT errored out here — the caller
// (Policy.ConstitutionSources) logs and skips them, mirroring how
// user-declared sources are handled. That keeps a single typo from
// blanking the whole profile.
func LoadConstitutionProfile(path string) (profile *ConstitutionProfile, dir string, err error) {
	if path == "" {
		return nil, "", fmt.Errorf("constitution profile path is empty")
	}

	expanded, err := expandPath(path, "")
	if err != nil {
		return nil, "", fmt.Errorf("constitution profile: expand path: %w", err)
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		return nil, "", fmt.Errorf("constitution profile: read %s: %w", expanded, err)
	}

	// Strict-decode the profile so a typo in a scope key (e.g.
	// `task_kinds:` plural) surfaces as a clear error instead of
	// silently widening the rule applicability — codex's SHOULD_FIX #4.
	// The path is included in the wrapping error so the user knows
	// which file to fix when several profiles are involved.
	var p ConstitutionProfile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, "", fmt.Errorf("constitution profile: parse %s: %w", expanded, err)
	}

	return &p, filepath.Dir(expanded), nil
}

// constitutionProfileSources resolves a profile path into a slice of
// ready-to-use constitution.Source entries. Bad declarations are
// logged via the standard logger (so daemon-mode picks them up in the
// configured log file — direct os.Stderr writes get lost when the
// daemon is detached from a terminal) and skipped — consistent with
// user-level handling. Returns nil when path is empty (callers treat
// that as "no profile configured").
func constitutionProfileSources(path string) []constitution.Source {
	if path == "" {
		return nil
	}
	profile, dir, err := LoadConstitutionProfile(path)
	if err != nil {
		log.Printf("constitution: %v", err)
		return nil
	}
	if profile == nil {
		return nil
	}
	out := make([]constitution.Source, 0, len(profile.Sources))
	for _, decl := range profile.Sources {
		src, err := decl.toSource(dir)
		if err != nil {
			log.Printf("constitution profile: skipping source %q: %v", decl.Name, err)
			continue
		}
		if src != nil {
			out = append(out, src)
		}
	}
	return out
}
