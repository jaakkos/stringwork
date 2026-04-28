package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/constitution"
)

// erroringSource is a test-only constitution.Source used to simulate
// the broken-team-source scenario in renderConstitutionShow tests.
type erroringSource struct {
	name string
	err  error
}

func (e *erroringSource) Name() string { return e.name }
func (e *erroringSource) List(constitution.Scope) ([]constitution.File, error) {
	return nil, e.err
}

func TestScaffoldConstitution_CreatesScaffoldOnEmptyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "constitution")
	created, skipped, err := scaffoldConstitution(dir, false)
	if err != nil {
		t.Fatalf("scaffoldConstitution: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("empty dir scaffold should skip nothing, got %v", skipped)
	}
	gotNames := basenames(created)
	wantNames := []string{"constitution.md", "engineering.md"}
	sort.Strings(gotNames)
	sort.Strings(wantNames)
	if !equalSlices(gotNames, wantNames) {
		t.Errorf("created files = %v, want %v", gotNames, wantNames)
	}
	for _, name := range wantNames {
		body, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Errorf("expected scaffold file %s to exist: %v", name, readErr)
			continue
		}
		if len(body) == 0 {
			t.Errorf("scaffold file %s is empty", name)
		}
	}
}

func TestScaffoldConstitution_SkipsExistingFilesByDefault(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "constitution.md")
	if err := os.WriteFile(existing, []byte("EXISTING"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	created, skipped, err := scaffoldConstitution(dir, false)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if !sliceContains(basenames(created), "engineering.md") {
		t.Errorf("expected engineering.md to be created, got %v", created)
	}
	if !sliceContains(basenames(skipped), "constitution.md") {
		t.Errorf("expected constitution.md to be skipped, got %v", skipped)
	}
	body, _ := os.ReadFile(existing)
	if string(body) != "EXISTING" {
		t.Errorf("existing constitution.md was clobbered without --force: %q", string(body))
	}
}

func TestScaffoldConstitution_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "constitution.md")
	if err := os.WriteFile(existing, []byte("OLD"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	created, skipped, err := scaffoldConstitution(dir, true)
	if err != nil {
		t.Fatalf("scaffold force: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("--force should not skip anything, got %v", skipped)
	}
	if len(created) != 2 {
		t.Errorf("--force should write all scaffold files, got %v", created)
	}
	body, _ := os.ReadFile(existing)
	if string(body) == "OLD" {
		t.Errorf("--force should have overwritten constitution.md")
	}
}

func TestScaffoldConstitution_EmptyDirError(t *testing.T) {
	if _, _, err := scaffoldConstitution("", false); err == nil {
		t.Error("empty dir should return error")
	}
}

func TestScaffoldedDir_ResolvesViaDirSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "constitution")
	if _, _, err := scaffoldConstitution(dir, false); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	src := &constitution.DirSource{Path: dir}
	files, err := constitution.Resolve([]constitution.Source{src}, constitution.Scope{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("scaffold should yield 2 files, got %d", len(files))
	}
	if filepath.Base(files[0].Path) != "constitution.md" {
		t.Errorf("constitution.md should be first, got %s", files[0].Path)
	}
	preamble := constitution.BuildPreamble(files)
	if !strings.Contains(preamble, "engineering.md") {
		t.Errorf("preamble should mention engineering.md:\n%s", preamble)
	}
	inline := constitution.BuildInline(files)
	if !strings.Contains(inline, "Edit or replace these with your team's actual rules.") {
		t.Errorf("inline should include engineering body:\n%s", inline)
	}
}

func TestUniqueSourceNames_DedupesAndSorts(t *testing.T) {
	files := []constitution.File{
		{Source: "team-rules"},
		{Source: "global"},
		{Source: "team-rules"},
		{Source: ""},
	}
	got := uniqueSourceNames(files)
	want := []string{"global", "team-rules"}
	if !equalSlices(got, want) {
		t.Errorf("uniqueSourceNames = %v, want %v", got, want)
	}
}

func TestHasArg(t *testing.T) {
	if !hasArg([]string{"--inline"}, "--inline") {
		t.Error("expected hasArg --inline to match")
	}
	if hasArg([]string{"--inline=true"}, "--inline") {
		t.Error("hasArg should not match key=value form for boolean flags")
	}
	if hasArg([]string{"--other"}, "--inline") {
		t.Error("hasArg should not match unrelated flag")
	}
}

func basenames(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sliceContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func TestSelectGitSources_FiltersByType(t *testing.T) {
	dir := t.TempDir()
	srcs := []constitution.Source{
		&constitution.DirSource{SourceName: "general", Path: dir},
		&constitution.GitSource{SourceName: "team", Repo: "r", CacheDir: "c"},
		&constitution.GitSource{SourceName: "ci", Repo: "r2", CacheDir: "c2"},
	}
	all := selectGitSources(srcs, "")
	if len(all) != 2 {
		t.Errorf("expected 2 git sources, got %d", len(all))
	}
	one := selectGitSources(srcs, "team")
	if len(one) != 1 || one[0].Name() != "team" {
		t.Errorf("named filter failed, got %v", one)
	}
	none := selectGitSources(srcs, "nope")
	if len(none) != 0 {
		t.Errorf("unknown name should yield none, got %d", len(none))
	}
}

func TestDoctorDirSource_OK(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rule.md"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := doctorDirSource(&constitution.DirSource{Path: dir}); err != nil {
		t.Errorf("doctorDirSource: %v", err)
	}
}

func TestDoctorDirSource_FailsOnMissingDir(t *testing.T) {
	err := doctorDirSource(&constitution.DirSource{Path: filepath.Join(t.TempDir(), "no-such")})
	if err == nil {
		t.Error("missing dir should error")
	}
	// errors.Is must walk the wrap chain so the doctor can demote a
	// missing global directory to WARN. If this assertion ever fails,
	// the doctor will go back to ERRORing on a fresh install.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing dir error should wrap fs.ErrNotExist, got %v", err)
	}
}

func TestDoctorDirSource_FailsOnNoMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := doctorDirSource(&constitution.DirSource{Path: dir})
	if err == nil {
		t.Error("expected error when no files match include")
	}
	if !strings.Contains(err.Error(), "match include patterns") {
		t.Errorf("error should mention include patterns, got %v", err)
	}
}

func TestDoctorDirSource_HonoursCustomInclude(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rule.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := doctorDirSource(&constitution.DirSource{Path: dir, Include: []string{"*.txt"}})
	if err != nil {
		t.Errorf("custom include should accept .txt, got %v", err)
	}
}

// TestDoctorDirSource_ReportsBadGlob locks in SHOULD_FIX #5: doctor
// must surface malformed include patterns rather than silently exit OK
// the way the old hand-rolled validation did. A worker would reject
// the same pattern on List(), so doctor must too.
func TestDoctorDirSource_ReportsBadGlob(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rule.md"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := doctorDirSource(&constitution.DirSource{Path: dir, Include: []string{"*[md"}})
	if err == nil {
		t.Fatal("expected doctor to surface bad glob; got nil")
	}
	if !strings.Contains(err.Error(), "include pattern") && !strings.Contains(err.Error(), "syntax") {
		t.Errorf("error should describe the malformed pattern, got %v", err)
	}
}

// TestRenderConstitutionShow_PartialResolveSurvives locks in the CLI
// sibling of MUST_FIX #2 (claude-code-task-34's Finding 1): when one
// source errors but another resolves cleanly, `constitution show`
// must still render the surviving files and surface the err to stderr
// as a warning — not hard-exit. Without this, a user running `show`
// to debug a broken team source loses sight of the rest of their
// constitution at the exact moment they need it.
func TestRenderConstitutionShow_PartialResolveSurvives(t *testing.T) {
	goodDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(goodDir, "alpha.md"), []byte("alpha rule body"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srcs := []constitution.Source{
		&constitution.DirSource{SourceName: "good", Path: goodDir, Include: []string{"*.md"}},
		&erroringSource{name: "broken", err: io.ErrUnexpectedEOF},
	}

	var stdout, stderr bytes.Buffer
	code := renderConstitutionShow(srcs, constitution.Scope{}, false, "/some/builtin/dir", &stdout, &stderr)

	if code != 0 {
		t.Errorf("partial-resolve should not exit non-zero, got %d", code)
	}
	if !strings.Contains(stdout.String(), "alpha.md") {
		t.Errorf("stdout should render the surviving source, got: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("stderr should warn about the broken source, got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "broken") {
		t.Errorf("stderr should name the broken source, got: %q", stderr.String())
	}
}

// TestRenderConstitutionShow_AllSourcesFail_ExitsNonZero verifies the
// other half of partial-resolve handling: when no source resolves AND
// an error occurred, the user sees an error and the CLI exits 1. This
// distinguishes "broken setup" from "empty setup".
func TestRenderConstitutionShow_AllSourcesFail_ExitsNonZero(t *testing.T) {
	srcs := []constitution.Source{
		&erroringSource{name: "broken-1", err: io.ErrUnexpectedEOF},
		&erroringSource{name: "broken-2", err: errors.New("network down")},
	}

	var stdout, stderr bytes.Buffer
	code := renderConstitutionShow(srcs, constitution.Scope{}, false, "/some/builtin/dir", &stdout, &stderr)

	if code != 1 {
		t.Errorf("all-sources-fail should exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "error") {
		t.Errorf("stderr should print error, got: %q", stderr.String())
	}
}

// TestRenderConstitutionShow_NoSourcesNoError_TipsUser preserves the
// "fresh install" hint: empty sources with no error should print the
// `constitution init` tip rather than exit non-zero.
func TestRenderConstitutionShow_NoSourcesNoError_TipsUser(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := renderConstitutionShow(nil, constitution.Scope{}, false, "/some/builtin/dir", &stdout, &stderr)

	if code != 0 {
		t.Errorf("empty sources should exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "No constitution files resolved.") {
		t.Errorf("stdout should print empty-set message, got: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "constitution init") {
		t.Errorf("stdout should hint at `constitution init`, got: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty when no error, got: %q", stderr.String())
	}
}

// TestDoctorDirSource_ScopedSourceStillResolves guards a regression
// that surfaced once doctor started delegating to List(): a source
// scoped to (e.g.) review-only tasks would otherwise return zero
// files for the doctor's empty default scope, even though the path
// and globs are perfectly valid. doctor must validate the static
// configuration regardless of scope, so the user is not forced to
// file a review task just to confirm the path resolves.
func TestDoctorDirSource_ScopedSourceStillResolves(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "checklist.md"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := doctorDirSource(&constitution.DirSource{
		Path:    dir,
		Include: []string{"*.md"},
		Scope:   constitution.ScopeFilter{TaskKind: []string{"review"}},
	})
	if err != nil {
		t.Errorf("doctor must accept a scoped source whose path resolves; got %v", err)
	}
}

// TestDoctorGitSource_ReportsZeroResolvedFiles locks in SHOULD_FIX #5
// for git sources: a populated cache that resolves to zero files
// (typically due to a typo in `paths` or include globs) must surface
// at doctor time. Otherwise workers see an empty constitution and
// silently lose every team rule.
func TestDoctorGitSource_ReportsZeroResolvedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	upstream := makeUpstreamForCLI(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	g := &constitution.GitSource{
		Repo:     upstream,
		Ref:      "main",
		CacheDir: cacheDir,
		Paths:    []string{"does-not-exist"},
	}
	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	level, msg := doctorGitSource(context.Background(), g)
	if level != "WARN" && level != "ERROR" {
		t.Errorf("zero-resolved-files should be WARN or ERROR, got %q (%s)", level, msg)
	}
	if !strings.Contains(msg, "zero files") && !strings.Contains(msg, "list failed") {
		t.Errorf("verdict should explain the zero-resolved condition, got %q", msg)
	}
}

// TestDoctorGitSource_ReportsListErrorFromBadGlob locks in the
// containment side of SHOULD_FIX #5: a populated cache combined with
// a malformed Include glob must surface as ERROR. Without doctor's
// new List call, this would have gone unnoticed until a worker spawn
// hit it.
func TestDoctorGitSource_ReportsListErrorFromBadGlob(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	upstream := makeUpstreamForCLI(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	g := &constitution.GitSource{
		Repo:     upstream,
		Ref:      "main",
		CacheDir: cacheDir,
		Include:  []string{"*[md"},
	}
	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	level, msg := doctorGitSource(context.Background(), g)
	if level != "ERROR" {
		t.Errorf("bad include glob should be ERROR, got %q (%s)", level, msg)
	}
}

func TestDoctorGitSource_RejectsMissingFields(t *testing.T) {
	if l, _ := doctorGitSource(context.Background(), &constitution.GitSource{}); l != "ERROR" {
		t.Errorf("missing repo/cache should be ERROR, got %q", l)
	}
	if l, _ := doctorGitSource(context.Background(), &constitution.GitSource{Repo: "r"}); l != "ERROR" {
		t.Errorf("missing cache should be ERROR, got %q", l)
	}
}

func TestDoctorGitSource_WarnsWhenCacheEmpty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	upstream := makeUpstreamForCLI(t)
	g := &constitution.GitSource{
		Repo:     upstream,
		Ref:      "main",
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	}
	level, msg := doctorGitSource(context.Background(), g)
	if level != "WARN" {
		t.Errorf("empty cache should be WARN, got %q (%s)", level, msg)
	}
	if !strings.Contains(msg, "cache empty") {
		t.Errorf("WARN message should mention empty cache, got %q", msg)
	}
}

func TestDoctorGitSource_ErrorOnUnreachableRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	g := &constitution.GitSource{
		Repo:     filepath.Join(t.TempDir(), "does-not-exist.git"),
		Ref:      "main",
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	}
	level, msg := doctorGitSource(context.Background(), g)
	if level != "ERROR" {
		t.Errorf("unreachable remote should be ERROR, got %q (%s)", level, msg)
	}
}

// makeUpstreamForCLI creates a tiny non-bare git repo for CLI tests
// without coupling them to the upstream-helper in the constitution
// package's test binary. Only one commit is made; that's enough for
// `git ls-remote` to succeed.
func makeUpstreamForCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustRun("init", "-q", "-b", "main", ".")
	mustRun("config", "user.email", "test@example.com")
	mustRun("config", "user.name", "test")
	mustRun("config", "receive.denyCurrentBranch", "ignore")
	if err := os.WriteFile(filepath.Join(dir, "hello.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mustRun("add", "-A")
	mustRun("commit", "-q", "-m", "init")
	return dir
}
