package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/constitution"
)

// writeProfile drops a YAML profile next to a `rules` directory and
// returns the absolute profile path. Tests use this to mimic a team's
// devtools-repo layout.
func writeProfile(t *testing.T, body string) (profilePath, profileDir string) {
	t.Helper()
	profileDir = t.TempDir()
	profilePath = filepath.Join(profileDir, "stringwork.profile.yaml")
	if err := os.WriteFile(profilePath, []byte(body), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return profilePath, profileDir
}

func TestLoadConstitutionProfile_ParsesAndReturnsProfileDir(t *testing.T) {
	body := `
sources:
  - name: regfin-rules
    type: dir
    path: $PROFILE_DIR/rules
    include: ["*.md"]
  - name: regfin-pr-review
    type: dir
    path: $PROFILE_DIR/skills/regfin-pr-review/references
    include: ["*.md"]
    scope:
      task_kind: ["review"]
`
	profilePath, profileDir := writeProfile(t, body)

	profile, dir, err := LoadConstitutionProfile(profilePath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if profile == nil || len(profile.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %+v", profile)
	}
	if dir != profileDir {
		t.Errorf("returned dir %q, want %q", dir, profileDir)
	}
	if profile.Sources[0].Path != "$PROFILE_DIR/rules" {
		t.Errorf("path field should be untouched until source materialisation, got %q", profile.Sources[0].Path)
	}
	if profile.Sources[1].Scope == nil || len(profile.Sources[1].Scope.TaskKind) != 1 {
		t.Errorf("scope not parsed: %+v", profile.Sources[1].Scope)
	}
}

func TestLoadConstitutionProfile_ProfileDirExpansion(t *testing.T) {
	profilePath, profileDir := writeProfile(t, `
sources:
  - name: pr-review
    type: dir
    path: $PROFILE_DIR/skills/regfin-pr-review/references
    include: ["*.md"]
`)

	srcs := constitutionProfileSources(profilePath)
	if len(srcs) != 1 {
		t.Fatalf("want 1 source, got %d", len(srcs))
	}
	d, ok := srcs[0].(*constitution.DirSource)
	if !ok {
		t.Fatalf("want *DirSource, got %T", srcs[0])
	}
	want := filepath.Join(profileDir, "skills/regfin-pr-review/references")
	if d.Path != want {
		t.Errorf("expanded path = %q, want %q", d.Path, want)
	}
}

func TestLoadConstitutionProfile_MissingFile(t *testing.T) {
	_, _, err := LoadConstitutionProfile(filepath.Join(t.TempDir(), "no-such.yaml"))
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error should mention read failure, got %v", err)
	}
}

func TestLoadConstitutionProfile_MalformedYAML(t *testing.T) {
	profilePath, _ := writeProfile(t, "not: [valid")
	_, _, err := LoadConstitutionProfile(profilePath)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse failure, got %v", err)
	}
}

func TestLoadConstitutionProfile_EmptyPath(t *testing.T) {
	_, _, err := LoadConstitutionProfile("")
	if err == nil {
		t.Error("empty path should error")
	}
}

func TestPolicy_ConstitutionSources_ProfileBetweenGlobalAndUser(t *testing.T) {
	profilePath, _ := writeProfile(t, `
sources:
  - name: profile-rules
    type: dir
    path: `+t.TempDir()+`
`)
	userDir := t.TempDir()

	cfg := DefaultConfig()
	cfg.Constitution = &ConstitutionConfig{
		Profile: profilePath,
		Sources: []ConstitutionSourceConfig{
			{Name: "user-rules", Type: "dir", Path: userDir},
		},
	}
	pol := New(cfg)
	srcs := pol.ConstitutionSources()
	got := srcNames(srcs)
	want := []string{"global", "profile-rules", "user-rules"}
	if len(got) != len(want) {
		t.Fatalf("source order: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestProfile_RejectsUnknownScopeKey is the regression test for codex
// SHOULD_FIX #4: when the user typos a key inside a `scope:` block
// (e.g. `task_kinds:` plural for `task_kind:`), the previous loose
// decoder dropped the field on the floor and the source quietly
// applied to every task. Strict mode (KnownFields(true)) surfaces
// this as a parse error so the typo is fixed at config-load time.
func TestProfile_RejectsUnknownScopeKey(t *testing.T) {
	profilePath, _ := writeProfile(t, `
sources:
  - name: pr-review
    type: dir
    path: $PROFILE_DIR/skills
    scope:
      task_kinds: ["review"]
`)

	_, _, err := LoadConstitutionProfile(profilePath)
	if err == nil {
		t.Fatal("expected typo'd scope key to error, got nil")
	}
	if !strings.Contains(err.Error(), "task_kinds") {
		t.Errorf("error must name the offending key, got %v", err)
	}
	if !strings.Contains(err.Error(), profilePath) {
		t.Errorf("error must include the profile path, got %v", err)
	}
}

// TestProfile_RejectsUnknownTopLevelKey covers the other half of the
// strict-decoder coverage: a typo at the profile's top level
// (e.g. `sourcs:` for `sources:`) should also fail loudly.
func TestProfile_RejectsUnknownTopLevelKey(t *testing.T) {
	profilePath, _ := writeProfile(t, `
sourcs:
  - name: pr-review
    type: dir
    path: $PROFILE_DIR/skills
`)

	_, _, err := LoadConstitutionProfile(profilePath)
	if err == nil {
		t.Fatal("expected typo'd top-level key to error, got nil")
	}
	if !strings.Contains(err.Error(), "sourcs") {
		t.Errorf("error must name the offending key, got %v", err)
	}
}

// TestProfile_AcceptsCoLocatedTaskTemplatesBlock is the regression test
// for Phase 4 of the task-template work: a single
// stringwork.profile.yaml that ships BOTH `sources:` (constitution) and
// `task_templates:` (task-template overlay) in the same file must
// parse cleanly through LoadConstitutionProfile -- the constitution
// loader is supposed to ignore the task-template block, not strict-
// reject it as an unknown top-level field. The companion
// LoadTaskTemplateProfile already permissively decodes the same file
// to extract its own block (see task_template_config.go).
func TestProfile_AcceptsCoLocatedTaskTemplatesBlock(t *testing.T) {
	profilePath, _ := writeProfile(t, `
sources:
  - name: rules
    type: dir
    path: `+t.TempDir()+`
task_templates:
  sources:
    - name: code-review-overlay
      type: dir
      path: `+t.TempDir()+`
`)

	profile, _, err := LoadConstitutionProfile(profilePath)
	if err != nil {
		t.Fatalf("co-located task_templates block must parse cleanly, got %v", err)
	}
	if len(profile.Sources) != 1 || profile.Sources[0].Name != "rules" {
		t.Errorf("constitution sources lost or mangled: %+v", profile.Sources)
	}
}

func TestPolicy_ConstitutionSources_ProfileMissingProfile_StillReturnsGlobal(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Constitution = &ConstitutionConfig{
		Profile: filepath.Join(t.TempDir(), "missing.yaml"),
	}
	pol := New(cfg)
	srcs := pol.ConstitutionSources()
	if len(srcs) == 0 || srcs[0].Name() != "global" {
		t.Errorf("missing profile should leave global intact; got %v", srcNames(srcs))
	}
}
