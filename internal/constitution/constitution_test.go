package constitution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestDirSource_Missing_Returns_NoError(t *testing.T) {
	src := &DirSource{Path: filepath.Join(t.TempDir(), "does-not-exist")}
	files, err := src.List(Scope{})
	if err != nil {
		t.Fatalf("missing dir should not error, got %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("missing dir should yield no files, got %d", len(files))
	}
}

func TestDirSource_NonDir_Returns_Error(t *testing.T) {
	dir := t.TempDir()
	regular := writeFile(t, dir, "file.md", "body")
	src := &DirSource{Path: regular}
	if _, err := src.List(Scope{}); err == nil {
		t.Fatal("non-dir path should error")
	}
}

func TestDirSource_NoEntryPoint_AlphabeticalFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "z-rules.md", "z body")
	writeFile(t, dir, "a-rules.md", "a body")
	writeFile(t, dir, "m-rules.md", "m body")
	writeFile(t, dir, "skipped.txt", "should be filtered out")

	src := &DirSource{Path: dir}
	files, err := src.List(Scope{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 files, got %d (%v)", len(files), filenames(files))
	}
	wantOrder := []string{"a-rules.md", "m-rules.md", "z-rules.md"}
	for i, want := range wantOrder {
		if filepath.Base(files[i].Path) != want {
			t.Errorf("position %d = %q, want %q", i, filepath.Base(files[i].Path), want)
		}
	}
}

func TestDirSource_OrderedList_HonoursDeclaredOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "constitution.md", strings.Join([]string{
		"# Constitution",
		"",
		"Read in order:",
		"",
		"1. `project.md`",
		"2. `engineering.md`",
		"3. `workflow.md`",
		"",
	}, "\n"))
	writeFile(t, dir, "project.md", "p")
	writeFile(t, dir, "engineering.md", "e")
	writeFile(t, dir, "workflow.md", "w")
	writeFile(t, dir, "extra.md", "x")

	src := &DirSource{Path: dir}
	files, err := src.List(Scope{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := filenames(files)
	want := []string{"constitution.md", "project.md", "engineering.md", "workflow.md", "extra.md"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDirSource_OrderedList_IgnoresUnknownPaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "constitution.md", "1. `nope.md`\n2. `present.md`\n")
	writeFile(t, dir, "present.md", "body")

	src := &DirSource{Path: dir}
	files, err := src.List(Scope{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := filenames(files)
	want := []string{"constitution.md", "present.md"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDirSource_IncludeFilter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rule.md", "md")
	writeFile(t, dir, "rule.txt", "txt")
	writeFile(t, dir, "skip.json", "json")

	src := &DirSource{Path: dir, Include: []string{"*.md", "*.txt"}}
	files, err := src.List(Scope{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := filenames(files)
	want := []string{"rule.md", "rule.txt"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDirSource_BadIncludeGlob_ReturnsError is the regression test for
// the silent-bad-glob bug. Previously scanDir's filepath.Match call
// dropped the error, so a typo like `*[md` produced zero matches with
// no diagnostic and the user never knew their include list was broken.
func TestDirSource_BadIncludeGlob_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rule.md", "ok")
	src := &DirSource{Path: dir, Include: []string{"*[md"}}
	if _, err := src.List(Scope{}); err == nil {
		t.Fatal("expected error for malformed glob, got nil")
	}
}

func TestDirSource_ScopeFilter_TaskKind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "checklist.md", "review checklist")

	src := &DirSource{
		Path:  dir,
		Scope: ScopeFilter{TaskKind: []string{"review"}},
	}

	hit, err := src.List(Scope{TaskKind: "review"})
	if err != nil {
		t.Fatalf("list(review): %v", err)
	}
	if len(hit) != 1 {
		t.Fatalf("review scope should yield 1 file, got %d", len(hit))
	}

	miss, err := src.List(Scope{TaskKind: "feature"})
	if err != nil {
		t.Fatalf("list(feature): %v", err)
	}
	if len(miss) != 0 {
		t.Fatalf("feature scope should yield 0 files, got %d", len(miss))
	}
}

func TestDirSource_Name_DefaultsToBasename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "regfin-rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := &DirSource{Path: dir}
	if got := src.Name(); got != "regfin-rules" {
		t.Errorf("Name() = %q, want %q", got, "regfin-rules")
	}
	src.SourceName = "custom"
	if got := src.Name(); got != "custom" {
		t.Errorf("Name() with override = %q, want %q", got, "custom")
	}
}

func TestResolve_ConcatenatesSourcesInDeclarationOrder(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	writeFile(t, dir1, "alpha.md", "from dir1")
	writeFile(t, dir2, "beta.md", "from dir2")

	a := &DirSource{SourceName: "first", Path: dir1}
	b := &DirSource{SourceName: "second", Path: dir2}

	files, err := Resolve([]Source{a, b}, Scope{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if files[0].Source != "first" || files[1].Source != "second" {
		t.Errorf("source order lost: got [%s, %s]", files[0].Source, files[1].Source)
	}
}

func TestResolve_NilEntries_AreSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rule.md", "ok")
	files, err := Resolve([]Source{nil, &DirSource{Path: dir}, nil}, Scope{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
}

func TestResolve_EmptySources_ReturnsNil(t *testing.T) {
	files, err := Resolve(nil, Scope{})
	if err != nil {
		t.Fatalf("resolve(nil): %v", err)
	}
	if files != nil {
		t.Errorf("expected nil, got %v", files)
	}
}

// erroringSource is a minimal Source implementation that always returns
// the configured error. Used to verify Resolve's accumulation behavior:
// one source erroring out must not blank the rest.
type erroringSource struct {
	name string
	err  error
}

func (e *erroringSource) Name() string               { return e.name }
func (e *erroringSource) List(Scope) ([]File, error) { return nil, e.err }

// TestResolve_BadSource_DoesNotSwallowOthers is the regression test for
// the "Resolve returns early on first error" bug. The documented
// behavior is "a typo in one team's profile doesn't silently swallow
// rules from the rest" — Resolve must continue past a failing source
// and return the surviving files alongside the joined error.
func TestResolve_BadSource_DoesNotSwallowOthers(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	writeFile(t, dir1, "alpha.md", "from dir1")
	writeFile(t, dir2, "beta.md", "from dir2")

	good1 := &DirSource{SourceName: "first", Path: dir1}
	bad := &erroringSource{name: "broken", err: errSourceForTest}
	good2 := &DirSource{SourceName: "second", Path: dir2}

	files, err := Resolve([]Source{good1, bad, good2}, Scope{})
	if err == nil {
		t.Fatal("expected error from bad source, got nil")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error must name the failing source, got %q", err.Error())
	}
	if len(files) != 2 {
		t.Fatalf("want 2 surviving files (alpha + beta), got %d (%v)", len(files), filenames(files))
	}
	if files[0].Source != "first" || files[1].Source != "second" {
		t.Errorf("surviving file order lost: got [%s, %s]", files[0].Source, files[1].Source)
	}
}

var errSourceForTest = fmt.Errorf("simulated source failure")

func TestBuildPreamble_EmptyFiles_ReturnsEmpty(t *testing.T) {
	if got := BuildPreamble(nil); got != "" {
		t.Errorf("nil files preamble = %q, want empty", got)
	}
	if got := BuildPreamble([]File{}); got != "" {
		t.Errorf("empty slice preamble = %q, want empty", got)
	}
}

func TestBuildPreamble_NumbersAndDisplayPaths(t *testing.T) {
	files := []File{
		{DisplayPath: "~/.config/stringwork/constitution/a.md"},
		{DisplayPath: "~/.config/stringwork/constitution/b.md"},
	}
	got := BuildPreamble(files)
	if !strings.HasPrefix(got, "== Constitution ==\n") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "1. ~/.config/stringwork/constitution/a.md") {
		t.Errorf("missing first entry: %q", got)
	}
	if !strings.Contains(got, "2. ~/.config/stringwork/constitution/b.md") {
		t.Errorf("missing second entry: %q", got)
	}
	if !strings.Contains(got, "earlier file wins") {
		t.Errorf("preamble should explain precedence: %q", got)
	}
}

func TestBuildPreamble_PadsLineNumbersWhenMoreThanNine(t *testing.T) {
	files := make([]File, 12)
	for i := range files {
		files[i].DisplayPath = "f.md"
	}
	got := BuildPreamble(files)
	if !strings.Contains(got, " 1. f.md") {
		t.Errorf("expected padded ' 1.', got: %q", got)
	}
	if !strings.Contains(got, "10. f.md") {
		t.Errorf("expected '10.' line: %q", got)
	}
}

func TestBuildInline_IncludesContentWithSeparators(t *testing.T) {
	files := []File{
		{DisplayPath: "a.md", Content: "alpha body"},
		{DisplayPath: "b.md", Content: "beta body\n"},
	}
	got := BuildInline(files)
	if !strings.Contains(got, "== Constitution ==") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "--- a.md ---\nalpha body") {
		t.Errorf("missing first separator/body: %q", got)
	}
	if !strings.Contains(got, "--- b.md ---\nbeta body") {
		t.Errorf("missing second separator/body: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("expected trailing newline: %q", got)
	}
}

func TestBuildInline_EmptyFiles_ReturnsEmpty(t *testing.T) {
	if got := BuildInline(nil); got != "" {
		t.Errorf("nil inline = %q, want empty", got)
	}
}

func TestDirSource_End2End_ContentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "constitution.md", "# Root\n\n1. `engineering.md`\n")
	writeFile(t, dir, "engineering.md", "Always write tests.\n")

	files, err := (&DirSource{Path: dir, SourceName: "global"}).List(Scope{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	inline := BuildInline(files)
	if !strings.Contains(inline, "Always write tests.") {
		t.Errorf("expected engineering body in inline output, got:\n%s", inline)
	}
	if !strings.Contains(inline, "# Root") {
		t.Errorf("expected entry-point body in inline output, got:\n%s", inline)
	}
}

func filenames(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Base(f.Path))
	}
	return out
}

// TestGitSource_RejectsAbsolutePath is the runtime half of codex
// MUST_FIX #1: even if a misconfigured GitSource somehow makes it into
// the resolver with an absolute path entry (e.g. via a hand-rolled
// programmatic config that bypassed the policy validation), List()
// must reject it rather than join it onto CacheDir and read whatever
// happens to be there. The error message must clearly name the
// offending entry so debugging is fast.
func TestGitSource_RejectsAbsolutePath(t *testing.T) {
	cache := t.TempDir()
	g := &GitSource{
		SourceName: "team-rules",
		CacheDir:   cache,
		Paths:      []string{"/etc"},
	}
	_, err := g.List(Scope{})
	if err == nil {
		t.Fatal("expected error for absolute paths entry")
	}
	if !strings.Contains(err.Error(), "/etc") {
		t.Errorf("error must name the offending entry, got %v", err)
	}
}

// TestGitSource_RejectsParentEscape exercises the cache-confinement
// guard: a `..` segment that would lift the join above CacheDir (after
// filepath.Clean) must produce an error, not a silent read of the
// host's parent directories.
func TestGitSource_RejectsParentEscape(t *testing.T) {
	cache := t.TempDir()
	g := &GitSource{
		SourceName: "team-rules",
		CacheDir:   cache,
		Paths:      []string{"../../tmp"},
	}
	_, err := g.List(Scope{})
	if err == nil {
		t.Fatal("expected error for ..-style escape")
	}
	if !strings.Contains(err.Error(), "escape") && !strings.Contains(err.Error(), "..") {
		t.Errorf("error must indicate escape attempt, got %v", err)
	}
}

// TestGitSource_RelativePathStaysInsideCache verifies the happy path:
// a normal repo-relative subdirectory resolves to a real subdirectory
// inside the cache and yields the expected file content. Without this
// the negative tests above could accidentally pass for the wrong
// reason (List rejecting *every* path).
func TestGitSource_RelativePathStaysInsideCache(t *testing.T) {
	cache := t.TempDir()
	rulesDir := filepath.Join(cache, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, rulesDir, "alpha.md", "alpha body")

	g := &GitSource{
		SourceName: "team-rules",
		CacheDir:   cache,
		Paths:      []string{"rules"},
	}
	files, err := g.List(Scope{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file (alpha.md), got %d (%v)", len(files), filenames(files))
	}
	if filepath.Base(files[0].Path) != "alpha.md" {
		t.Errorf("got %q, want alpha.md", filepath.Base(files[0].Path))
	}
}

// TestGitSource_EmptyPathsDefaultsToRoot preserves the documented
// "empty Paths means repo root" behaviour. A regression here would
// break every existing GitSource that doesn't enumerate its
// subdirectories explicitly (the common case).
func TestGitSource_EmptyPathsDefaultsToRoot(t *testing.T) {
	cache := t.TempDir()
	writeFile(t, cache, "alpha.md", "alpha body")

	g := &GitSource{
		SourceName: "team-rules",
		CacheDir:   cache,
		// Paths intentionally left nil.
	}
	files, err := g.List(Scope{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file (root alpha.md), got %d (%v)", len(files), filenames(files))
	}
}
