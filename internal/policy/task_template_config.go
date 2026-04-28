package policy

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaakkos/stringwork/internal/tasktemplates"
)

// TaskTemplateConfig is the YAML mapping for the top-level
// `task_templates:` block. Profile is an optional path to a team-shipped
// profile file whose own `sources` are merged before this block's
// `sources`. Sources is the user-level list; entries are typed
// (`type: dir`) and unknown types yield a clear error rather than a
// silent drop so config typos surface immediately.
//
// Kept intentionally separate from ConstitutionConfig because the
// semantics are different: constitution rules attach to every spawn
// scoped by task kind / agent role, whereas task templates are
// id-addressed and only consulted when the planner asks for a specific
// id. Sharing a YAML block would conflate the two.
type TaskTemplateConfig struct {
	Profile string                     `yaml:"profile,omitempty"`
	Sources []TaskTemplateSourceConfig `yaml:"sources,omitempty"`
}

// TaskTemplateSourceConfig is one entry in `task_templates.sources`.
// v1 supports only `type: dir`; the discriminator is preserved so
// future flavours (`type: git`, `type: http`) can land without
// breaking existing configs.
type TaskTemplateSourceConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type,omitempty"`
	Path string `yaml:"path,omitempty"`
}

// toSource validates a single declaration and converts it into a
// concrete tasktemplates.Source. profileDir is the directory of the
// loading profile file (used to expand $PROFILE_DIR); pass "" when the
// declaration came from the user's primary config.yaml.
func (c TaskTemplateSourceConfig) toSource(profileDir string) (tasktemplates.Source, error) {
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
		return &tasktemplates.DirSource{
			SourceName: c.Name,
			Path:       path,
		}, nil
	default:
		return nil, fmt.Errorf("source %q: unknown type %q (expected \"dir\")", c.Name, c.Type)
	}
}

// TaskTemplateProfile is the on-disk shape of a team-shared profile's
// task_templates block. Loaded from the same profile file as the
// constitution profile (typically `<devtools-repo>/stringwork.profile.yaml`)
// but parsed out of a different top-level key so the two systems stay
// independent — a team can ship template overlays without using the
// constitution and vice versa.
type TaskTemplateProfile struct {
	Sources []TaskTemplateSourceConfig `yaml:"sources,omitempty"`
}

// LoadTaskTemplateProfile reads and parses a profile file's
// `task_templates:` block. The returned `dir` is the absolute directory
// of the profile file — passed to TaskTemplateSourceConfig.toSource so
// `$PROFILE_DIR` expands correctly.
//
// The profile YAML is parsed with a permissive top-level shape: the
// loader extracts only the `task_templates:` block and silently
// ignores unrelated keys (e.g. `constitution:`). This keeps a single
// stringwork.profile.yaml shippable with both blocks side-by-side
// without forcing per-block parsers to know about each other.
func LoadTaskTemplateProfile(path string) (profile *TaskTemplateProfile, dir string, err error) {
	if path == "" {
		return nil, "", fmt.Errorf("task templates profile path is empty")
	}

	expanded, err := expandPath(path, "")
	if err != nil {
		return nil, "", fmt.Errorf("task templates profile: expand path: %w", err)
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		return nil, "", fmt.Errorf("task templates profile: read %s: %w", expanded, err)
	}

	var wrapper struct {
		TaskTemplates *TaskTemplateProfile `yaml:"task_templates"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// KnownFields(false) on the wrapper because the same file may
	// declare `constitution:` and other future blocks that this loader
	// doesn't care about. Strict-decoding a single block out of a
	// multi-block file requires intermediate looser decoding.
	dec.KnownFields(false)
	if err := dec.Decode(&wrapper); err != nil {
		return nil, "", fmt.Errorf("task templates profile: parse %s: %w", expanded, err)
	}

	out := &TaskTemplateProfile{}
	if wrapper.TaskTemplates != nil {
		out.Sources = wrapper.TaskTemplates.Sources
	}
	return out, filepath.Dir(expanded), nil
}

// taskTemplateProfileSources resolves a profile path into a slice of
// ready-to-use tasktemplates.Source entries. Bad declarations are
// logged via the standard logger (so daemon-mode picks them up in the
// configured log file) and skipped — consistent with constitution
// handling. Returns nil when path is empty (callers treat that as
// "no profile configured").
func taskTemplateProfileSources(path string) []tasktemplates.Source {
	if path == "" {
		return nil
	}
	profile, dir, err := LoadTaskTemplateProfile(path)
	if err != nil {
		log.Printf("task templates: %v", err)
		return nil
	}
	if profile == nil {
		return nil
	}
	out := make([]tasktemplates.Source, 0, len(profile.Sources))
	for _, decl := range profile.Sources {
		src, err := decl.toSource(dir)
		if err != nil {
			log.Printf("task templates profile: skipping source %q: %v", decl.Name, err)
			continue
		}
		if src != nil {
			out = append(out, src)
		}
	}
	return out
}
