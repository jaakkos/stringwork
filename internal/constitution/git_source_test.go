package constitution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireGit short-circuits the test when the git binary is unavailable
// (e.g. on a stripped CI image). Network is never used: every fixture
// is a local bare repository.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func runOrFail(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed in %s: %v\n%s", name, args, dir, err, out)
	}
}

// makeUpstreamRepo creates a regular (non-bare) git repo with `files`
// committed on the given branch. Returns the repo path; subsequent
// commits flow through commitToUpstream against the same path. We
// deliberately avoid bare repos + push — corporate git hooks on some
// machines block pushes to "outside" remotes. Cloning from a regular
// repo works the same way for our purposes (clone follows HEAD; fetch
// sees new commits) without ever invoking push.
func makeUpstreamRepo(t *testing.T, branch string, files map[string]string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "upstream")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir upstream: %v", err)
	}

	runOrFail(t, repo, "git", "init", "-q", "-b", branch, ".")
	runOrFail(t, repo, "git", "config", "user.email", "test@example.com")
	runOrFail(t, repo, "git", "config", "user.name", "test")
	// Allow clone clients to clone from a non-bare repo even though
	// HEAD is on a checked-out branch (default-deny on newer gits).
	runOrFail(t, repo, "git", "config", "receive.denyCurrentBranch", "ignore")

	for rel, body := range files {
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	runOrFail(t, repo, "git", "add", "-A")
	runOrFail(t, repo, "git", "commit", "-q", "-m", "init")
	return repo
}

// commitToUpstream adds a file directly to the upstream repo's working
// tree and commits it. No push needed — we clone from this repo
// directly, so its HEAD is what `fetch` sees.
func commitToUpstream(t *testing.T, repo, rel, body string) {
	t.Helper()
	full := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runOrFail(t, repo, "git", "add", rel)
	runOrFail(t, repo, "git", "commit", "-q", "-m", "add "+rel)
}

func TestGitSource_List_NoCache_ReturnsNil(t *testing.T) {
	g := &GitSource{
		SourceName: "team-rules",
		Repo:       "ignored",
		Ref:        "main",
		Paths:      []string{"rules"},
		CacheDir:   filepath.Join(t.TempDir(), "missing"),
	}
	files, err := g.List(Scope{})
	if err != nil {
		t.Fatalf("missing cache should not error, got %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("missing cache should yield no files, got %d", len(files))
	}
}

func TestGitSource_Sync_ClonesAndExposesFiles(t *testing.T) {
	requireGit(t)

	upstream := makeUpstreamRepo(t, "main", map[string]string{
		"rules/safe.md":         "be safe",
		"rules/secrets.md":      "no secrets",
		"instructions/style.md": "follow style",
	})
	cache := filepath.Join(t.TempDir(), "cache")
	g := &GitSource{
		SourceName: "team-rules",
		Repo:       upstream,
		Ref:        "main",
		Paths:      []string{"rules", "instructions"},
		Include:    []string{"*.md"},
		CacheDir:   cache,
	}

	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	files, err := g.List(Scope{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 files, got %d (%v)", len(files), filenamesGS(files))
	}
	for _, f := range files {
		if f.Source != "team-rules" {
			t.Errorf("file %s reported source %q, want team-rules", f.Path, f.Source)
		}
	}

	// Across-paths order: `rules` declared first, then `instructions`.
	wantOrder := []string{"safe.md", "secrets.md", "style.md"}
	got := filenamesGS(files)
	for i := range wantOrder {
		if got[i] != wantOrder[i] {
			t.Errorf("position %d = %q, want %q (order = %v)", i, got[i], wantOrder[i], got)
		}
	}
}

func TestGitSource_Sync_PicksUpRemoteUpdates(t *testing.T) {
	requireGit(t)

	upstream := makeUpstreamRepo(t, "main", map[string]string{
		"rules/safe.md": "v1",
	})
	cache := filepath.Join(t.TempDir(), "cache")
	g := &GitSource{
		SourceName: "team-rules",
		Repo:       upstream,
		Ref:        "main",
		Paths:      []string{"rules"},
		CacheDir:   cache,
	}
	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	files, _ := g.List(Scope{})
	if len(files) != 1 || !strings.Contains(files[0].Content, "v1") {
		t.Fatalf("first sync content unexpected: %v", files)
	}

	commitToUpstream(t, upstream, "rules/new.md", "added later")

	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	files, _ = g.List(Scope{})
	if len(files) != 2 {
		t.Errorf("after second sync want 2 files, got %d (%v)", len(files), filenamesGS(files))
	}

	commitToUpstream(t, upstream, "rules/safe.md", "v2")
	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("third sync: %v", err)
	}
	files, _ = g.List(Scope{})
	for _, f := range files {
		if filepath.Base(f.Path) == "safe.md" && !strings.Contains(f.Content, "v2") {
			t.Errorf("safe.md should reflect v2 after sync, got %q", f.Content)
		}
	}
}

func TestGitSource_Sync_RejectsMissingRepoOrCache(t *testing.T) {
	requireGit(t)

	g := &GitSource{Repo: "", CacheDir: t.TempDir()}
	if err := g.Sync(context.Background()); err == nil {
		t.Error("missing repo should error")
	}

	g = &GitSource{Repo: "any", CacheDir: ""}
	if err := g.Sync(context.Background()); err == nil {
		t.Error("missing cache_dir should error")
	}
}

func TestGitSource_Sync_ReportsCloneFailure(t *testing.T) {
	requireGit(t)

	g := &GitSource{
		SourceName: "broken",
		Repo:       filepath.Join(t.TempDir(), "does-not-exist.git"),
		Ref:        "main",
		CacheDir:   filepath.Join(t.TempDir(), "cache"),
	}
	err := g.Sync(context.Background())
	if err == nil {
		t.Fatal("clone of missing repo should fail")
	}
	if !strings.Contains(err.Error(), "clone failed") {
		t.Errorf("error should mention 'clone failed', got %v", err)
	}
}

func TestGitSource_ScopeFilter(t *testing.T) {
	requireGit(t)

	upstream := makeUpstreamRepo(t, "main", map[string]string{
		"checklist.md": "look at error handling",
	})
	cache := filepath.Join(t.TempDir(), "cache")
	g := &GitSource{
		SourceName: "review-rules",
		Repo:       upstream,
		Ref:        "main",
		Paths:      []string{"."},
		CacheDir:   cache,
		Scope:      ScopeFilter{TaskKind: []string{"review"}},
	}
	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if hit, _ := g.List(Scope{TaskKind: "review"}); len(hit) != 1 {
		t.Errorf("review scope should yield 1 file, got %d", len(hit))
	}
	if miss, _ := g.List(Scope{TaskKind: "feature"}); len(miss) != 0 {
		t.Errorf("feature scope should yield 0 files, got %d", len(miss))
	}
}

func TestGitSource_Name_DefaultsToCacheDirBase(t *testing.T) {
	g := &GitSource{CacheDir: filepath.Join(t.TempDir(), "regfin-mirror")}
	if got := g.Name(); got != "regfin-mirror" {
		t.Errorf("Name() = %q, want %q", got, "regfin-mirror")
	}
	g.SourceName = "custom"
	if got := g.Name(); got != "custom" {
		t.Errorf("Name() with override = %q, want %q", got, "custom")
	}
}

func TestGitSource_Sync_NoBranch_DefaultsToHead(t *testing.T) {
	requireGit(t)

	upstream := makeUpstreamRepo(t, "main", map[string]string{
		"hello.md": "hi",
	})
	cache := filepath.Join(t.TempDir(), "cache")
	g := &GitSource{
		SourceName: "no-ref",
		Repo:       upstream,
		// Ref intentionally empty.
		Paths:    []string{"."},
		CacheDir: cache,
	}
	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if files, _ := g.List(Scope{}); len(files) != 1 {
		t.Errorf("expected 1 file after head-tracking sync, got %d", len(files))
	}

	// Second sync should hit the fetch+reset path with FETCH_HEAD.
	if err := g.Sync(context.Background()); err != nil {
		t.Fatalf("second sync: %v", err)
	}
}

func filenamesGS(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Base(f.Path))
	}
	return out
}
