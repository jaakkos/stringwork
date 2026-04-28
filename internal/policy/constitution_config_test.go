package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jaakkos/stringwork/internal/constitution"
)

func TestExpandPath_TildeAndEnv(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("CTEST_RULES", filepath.Join(t.TempDir(), "from-env"))

	cases := []struct {
		name       string
		in         string
		profileDir string
		want       string
	}{
		{name: "tilde alone", in: "~", want: home},
		{name: "tilde slash", in: "~/Development/regfin-devtools", want: filepath.Join(home, "Development/regfin-devtools")},
		{name: "env var", in: "$CTEST_RULES/sub", want: filepath.Join(os.Getenv("CTEST_RULES"), "sub")},
		{name: "profile dir token", in: "$PROFILE_DIR/rules", profileDir: "/abs/profile", want: "/abs/profile/rules"},
		{name: "profile dir bracketed", in: "${PROFILE_DIR}/x", profileDir: "/abs/profile", want: "/abs/profile/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandPath(tc.in, tc.profileDir)
			if err != nil {
				t.Fatalf("expand: %v", err)
			}
			if got != tc.want {
				t.Errorf("expand(%q, %q) = %q, want %q", tc.in, tc.profileDir, got, tc.want)
			}
		})
	}
}

func TestExpandPath_RelativeBecomesAbsolute(t *testing.T) {
	got, err := expandPath("rel/path", "")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestExpandPath_EmptyError(t *testing.T) {
	if _, err := expandPath("   ", ""); err == nil {
		t.Error("empty path should error")
	}
}

// TestExpandPath_ProfileDirRejectedInMainConfig is the regression test
// for the silent-strip bug: $PROFILE_DIR used in the main config.yaml
// (where profileDir is "") used to flow through os.ExpandEnv and
// disappear, leaving paths like "/rules" pointing at nothing. The user
// would see zero rules with no diagnostic. The expand path now refuses
// the token in that context.
func TestExpandPath_ProfileDirRejectedInMainConfig(t *testing.T) {
	cases := []string{
		"$PROFILE_DIR/rules",
		"${PROFILE_DIR}/rules",
		"~/$PROFILE_DIR",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := expandPath(raw, "")
			if err == nil {
				t.Fatalf("expected error for %q in main config, got nil", raw)
			}
			if !strings.Contains(err.Error(), "PROFILE_DIR") {
				t.Errorf("error should mention PROFILE_DIR token, got: %v", err)
			}
		})
	}
}

func TestExpandPath_ProfileDirAcceptedFromProfile(t *testing.T) {
	dir := t.TempDir()
	got, err := expandPath("$PROFILE_DIR/rules", dir)
	if err != nil {
		t.Fatalf("expand from profile: %v", err)
	}
	want := filepath.Join(dir, "rules")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSourceConfig_ToSource_Dir(t *testing.T) {
	tmp := t.TempDir()
	cfg := ConstitutionSourceConfig{
		Name:    "team-rules",
		Type:    "dir",
		Path:    tmp,
		Include: []string{"*.md"},
		Scope: &ConstitutionScopeConfig{
			TaskKind: []string{"review"},
		},
	}
	src, err := cfg.toSource("")
	if err != nil {
		t.Fatalf("toSource: %v", err)
	}
	d, ok := src.(*constitution.DirSource)
	if !ok {
		t.Fatalf("expected *DirSource, got %T", src)
	}
	if d.SourceName != "team-rules" {
		t.Errorf("name = %q, want team-rules", d.SourceName)
	}
	if d.Path != tmp {
		t.Errorf("path = %q, want %q", d.Path, tmp)
	}
	if len(d.Scope.TaskKind) != 1 || d.Scope.TaskKind[0] != "review" {
		t.Errorf("scope task_kind = %v, want [review]", d.Scope.TaskKind)
	}
}

func TestSourceConfig_ToSource_Git(t *testing.T) {
	cfg := ConstitutionSourceConfig{
		Name:     "team-rules-remote",
		Type:     "git",
		Repo:     "git@github.com:org/repo.git",
		Ref:      "main",
		Paths:    []string{"rules", "instructions"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	}
	src, err := cfg.toSource("")
	if err != nil {
		t.Fatalf("toSource: %v", err)
	}
	g, ok := src.(*constitution.GitSource)
	if !ok {
		t.Fatalf("expected *GitSource, got %T", src)
	}
	if g.Repo != cfg.Repo {
		t.Errorf("repo = %q, want %q", g.Repo, cfg.Repo)
	}
	if len(g.Paths) != 2 {
		t.Errorf("paths len = %d, want 2", len(g.Paths))
	}
	// Git source `paths` are repo-relative, not host-absolute.
	// Joining is the GitSource's job (cache_dir + sub) and host
	// absolutising the entries — the previous behaviour — is the
	// security bug codex flagged: an entry like "/etc" on the host
	// would have leaked through unchanged. After the fix they must
	// stay relative *and* free of leading-`..` traversal.
	for _, p := range g.Paths {
		if filepath.IsAbs(p) {
			t.Errorf("path %q must remain repo-relative, never host-absolute", p)
		}
		if strings.HasPrefix(p, "..") {
			t.Errorf("path %q escapes the repo root", p)
		}
	}
}

// TestSourceConfig_ToSource_Git_RejectsAbsolutePath is the
// loader-side regression for codex MUST_FIX #1: an absolute `paths`
// entry must error out at config-load time, not be expanded into a
// host-relative path that joins arbitrary FS contents into the
// resolver's view.
func TestSourceConfig_ToSource_Git_RejectsAbsolutePath(t *testing.T) {
	cfg := ConstitutionSourceConfig{
		Name:     "team-rules-remote",
		Type:     "git",
		Repo:     "git@github.com:org/repo.git",
		Paths:    []string{"/etc"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	}
	_, err := cfg.toSource("")
	if err == nil {
		t.Fatal("expected error for absolute paths entry, got nil")
	}
	if !strings.Contains(err.Error(), "/etc") {
		t.Errorf("error must name the offending entry, got %v", err)
	}
}

// TestSourceConfig_ToSource_Git_RejectsParentEscape covers the other
// half of the loader's defence: `..`-style traversal must error at
// config-load time. The runtime guard in GitSource.List() catches
// this too, but failing fast at load time gives the user a clearer
// error surface ("which config line is wrong?") instead of a
// resolver-time message.
func TestSourceConfig_ToSource_Git_RejectsParentEscape(t *testing.T) {
	cfg := ConstitutionSourceConfig{
		Name:     "team-rules-remote",
		Type:     "git",
		Repo:     "git@github.com:org/repo.git",
		Paths:    []string{"../../etc"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	}
	_, err := cfg.toSource("")
	if err == nil {
		t.Fatal("expected error for ..-style escape, got nil")
	}
	if !strings.Contains(err.Error(), "escape") && !strings.Contains(err.Error(), "..") {
		t.Errorf("error must indicate escape attempt, got %v", err)
	}
}

// TestSourceConfig_ToSource_Git_RejectsEmptyPath protects against
// silently empty entries (e.g. a stray YAML list item with no value)
// degrading the source contract. Empty Paths overall still means
// "repo root" via List()'s default; an explicit "" entry is a typo.
func TestSourceConfig_ToSource_Git_RejectsEmptyPath(t *testing.T) {
	cfg := ConstitutionSourceConfig{
		Name:     "team-rules-remote",
		Type:     "git",
		Repo:     "git@github.com:org/repo.git",
		Paths:    []string{""},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	}
	_, err := cfg.toSource("")
	if err == nil {
		t.Fatal("expected error for empty paths entry, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error must call out the empty entry, got %v", err)
	}
}

func TestSourceConfig_ToSource_Errors(t *testing.T) {
	tests := []struct {
		name string
		cfg  ConstitutionSourceConfig
		want string
	}{
		{
			name: "missing name",
			cfg:  ConstitutionSourceConfig{Type: "dir", Path: "/x"},
			want: "missing name",
		},
		{
			name: "dir without path",
			cfg:  ConstitutionSourceConfig{Name: "x", Type: "dir"},
			want: "path is required",
		},
		{
			name: "git without repo",
			cfg:  ConstitutionSourceConfig{Name: "x", Type: "git", CacheDir: "/c"},
			want: "repo is required",
		},
		{
			name: "git without cache",
			cfg:  ConstitutionSourceConfig{Name: "x", Type: "git", Repo: "r"},
			want: "cache_dir is required",
		},
		{
			name: "unknown type",
			cfg:  ConstitutionSourceConfig{Name: "x", Type: "ftp"},
			want: "unknown type",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.cfg.toSource("")
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// TestPolicy_ConstitutionSources_RaceFreeUnderConcurrentReads locks
// in claude-code-task-34's Finding 3: Policy.ConstitutionSources used
// to capture the *ConstitutionConfig pointer under RLock and then
// dereference cc.Profile / cc.Sources after RUnlock. The invariant
// that nothing mutates *p.config.Constitution post-load was undocumented
// and unenforced. The fix copies scalar/slice fields out under the
// lock. Run under -race to catch a regression if a future contributor
// reverts to the old pointer-then-deref pattern AND wires up a
// hot-swap path that mutates Constitution in place.
func TestPolicy_ConstitutionSources_RaceFreeUnderConcurrentReads(t *testing.T) {
	tmpRules := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpRules, "rule.md"), []byte("rule"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Constitution = &ConstitutionConfig{
		Profile: "",
		Sources: []ConstitutionSourceConfig{
			{Name: "team-rules", Type: "dir", Path: tmpRules},
		},
	}
	pol := New(cfg)

	const goroutines = 16
	const iterations = 50
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < iterations; j++ {
				srcs := pol.ConstitutionSources()
				if len(srcs) < 1 {
					t.Errorf("want at least global source, got %d", len(srcs))
					return
				}
				if srcs[0].Name() != "global" {
					t.Errorf("first source = %q, want global", srcs[0].Name())
					return
				}
			}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

func TestPolicy_ConstitutionSources_AppendsDeclared(t *testing.T) {
	tmpRules := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpRules, "rule.md"), []byte("rule"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Constitution = &ConstitutionConfig{
		Sources: []ConstitutionSourceConfig{
			{Name: "team-rules", Type: "dir", Path: tmpRules},
		},
	}
	pol := New(cfg)
	srcs := pol.ConstitutionSources()
	if len(srcs) != 2 {
		t.Fatalf("want 2 sources (global + declared), got %d", len(srcs))
	}
	if srcs[0].Name() != "global" {
		t.Errorf("first source = %q, want global", srcs[0].Name())
	}
	if srcs[1].Name() != "team-rules" {
		t.Errorf("second source = %q, want team-rules", srcs[1].Name())
	}
}

func TestPolicy_ConstitutionSources_SkipsBadEntries(t *testing.T) {
	tmpGood := t.TempDir()
	cfg := DefaultConfig()
	cfg.Constitution = &ConstitutionConfig{
		Sources: []ConstitutionSourceConfig{
			{Name: "bad", Type: "dir"}, // missing path -> skipped
			{Name: "good", Type: "dir", Path: tmpGood},
		},
	}
	pol := New(cfg)
	srcs := pol.ConstitutionSources()
	if len(srcs) != 2 {
		t.Fatalf("want 2 sources (global + good), got %d (%v)", len(srcs), srcNames(srcs))
	}
	if srcs[1].Name() != "good" {
		t.Errorf("second source = %q, want good", srcs[1].Name())
	}
}

func TestLoadConfig_ParsesConstitutionBlock(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yamlBody := `
constitution:
  sources:
    - name: regfin-rules
      type: dir
      path: ` + rulesDir + `
      include: ["*.md"]
      scope:
        task_kind: ["review", "code-review"]
    - name: regfin-mirror
      type: git
      repo: file:///nope
      ref: main
      paths: ["rules"]
      cache_dir: ` + filepath.Join(dir, "cache") + `
`
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Constitution == nil {
		t.Fatal("constitution block missing")
	}
	if len(cfg.Constitution.Sources) != 2 {
		t.Fatalf("want 2 declared sources, got %d", len(cfg.Constitution.Sources))
	}
	if cfg.Constitution.Sources[0].Type != "dir" {
		t.Errorf("first source type = %q, want dir", cfg.Constitution.Sources[0].Type)
	}
	if scope := cfg.Constitution.Sources[0].Scope; scope == nil || len(scope.TaskKind) != 2 {
		t.Errorf("scope.task_kind not parsed: %+v", cfg.Constitution.Sources[0].Scope)
	}
	if cfg.Constitution.Sources[1].Type != "git" {
		t.Errorf("second source type = %q, want git", cfg.Constitution.Sources[1].Type)
	}

	pol := New(cfg)
	srcs := pol.ConstitutionSources()
	if len(srcs) != 3 {
		t.Errorf("want 3 sources via policy, got %d (%v)", len(srcs), srcNames(srcs))
	}
}

func TestPolicy_ConstitutionSources_ScopeForwardsThroughToResolve(t *testing.T) {
	rulesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rulesDir, "general.md"), []byte("general rule"), 0o644); err != nil {
		t.Fatalf("seed general: %v", err)
	}
	checklistDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(checklistDir, "review.md"), []byte("review checklist"), 0o644); err != nil {
		t.Fatalf("seed checklist: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Constitution = &ConstitutionConfig{
		Sources: []ConstitutionSourceConfig{
			{Name: "general", Type: "dir", Path: rulesDir},
			{
				Name: "review-only",
				Type: "dir",
				Path: checklistDir,
				Scope: &ConstitutionScopeConfig{
					TaskKind: []string{"review"},
				},
			},
		},
	}
	pol := New(cfg)
	srcs := pol.ConstitutionSources()

	general, errG := constitution.Resolve(srcs, constitution.Scope{TaskKind: ""})
	if errG != nil {
		t.Fatalf("resolve general: %v", errG)
	}
	for _, f := range general {
		if f.Source == "review-only" {
			t.Errorf("review-only file leaked into non-review scope: %v", f)
		}
	}

	reviewFiles, err := constitution.Resolve(srcs, constitution.Scope{TaskKind: "review"})
	if err != nil {
		t.Fatalf("resolve review: %v", err)
	}
	hit := false
	for _, f := range reviewFiles {
		if f.Source == "review-only" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("review-only source did not attach to review scope; sources seen: %v", reviewFiles)
	}
}

func TestUnmarshalSourceConfig_Roundtrip(t *testing.T) {
	body := `
name: foo
type: dir
path: ~/x
include: ["*.md"]
scope:
  task_kind: ["review"]
`
	var c ConstitutionSourceConfig
	if err := yaml.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Name != "foo" || c.Type != "dir" || c.Path != "~/x" {
		t.Errorf("roundtrip lost fields: %+v", c)
	}
	if c.Scope == nil || len(c.Scope.TaskKind) != 1 {
		t.Errorf("scope not parsed: %+v", c.Scope)
	}
}

func srcNames(srcs []constitution.Source) []string {
	out := make([]string, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, s.Name())
	}
	return out
}
