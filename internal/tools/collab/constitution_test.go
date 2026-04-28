package collab

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/constitution"
	"github.com/jaakkos/stringwork/internal/domain"
)

// newTestServiceWithConstitutionDir wires the mockPolicy with a single
// DirSource that points at the given directory. Used to drive
// integration tests against actual file content.
func newTestServiceWithConstitutionDir(t *testing.T, dir string) (*app.CollabService, *mockRepository) {
	t.Helper()
	repo := newMockRepository()
	pol := newMockPolicy()
	pol.constitutionSources = []constitution.Source{
		&constitution.DirSource{
			SourceName: "global",
			Path:       dir,
			Include:    []string{"*.md"},
		},
	}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	return svc, repo
}

func writeConstitutionFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "constitution.md"),
		[]byte("# Constitution\n\n1. `engineering.md`\n"), 0o644); err != nil {
		t.Fatalf("write constitution.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "engineering.md"),
		[]byte("Always ship tests."), 0o644); err != nil {
		t.Fatalf("write engineering.md: %v", err)
	}
	return dir
}

func TestClaimNext_PrependsConstitutionPreamble(t *testing.T) {
	dir := writeConstitutionFiles(t)
	svc, repo := newTestServiceWithConstitutionDir(t, dir)
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Implement preamble wiring", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 2

	result, err := callTool(t, srv, "claim_next", map[string]any{
		"agent": "claude-code",
	})
	if err != nil {
		t.Fatalf("claim_next: %v", err)
	}
	text := resultText(t, result)
	if !strings.HasPrefix(text, "== Constitution ==") {
		t.Errorf("claim text should start with constitution preamble, got:\n%s", text)
	}
	if !strings.Contains(text, "constitution.md") {
		t.Errorf("preamble should list constitution.md, got:\n%s", text)
	}
	if !strings.Contains(text, "Claimed task #1") {
		t.Errorf("preamble should be followed by claim text, got:\n%s", text)
	}
}

func TestClaimNext_NoConstitution_NoPreambleAdded(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Quick task", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 2

	result, err := callTool(t, srv, "claim_next", map[string]any{
		"agent": "claude-code",
	})
	if err != nil {
		t.Fatalf("claim_next: %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, "== Constitution ==") {
		t.Errorf("no constitution should mean no preamble, got:\n%s", text)
	}
	if !strings.HasPrefix(text, "Claimed task #1") {
		t.Errorf("claim text should start with claim line, got:\n%s", text)
	}
}

func TestClaimNext_DryRun_StillNoPreamble(t *testing.T) {
	dir := writeConstitutionFiles(t)
	svc, repo := newTestServiceWithConstitutionDir(t, dir)
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Some task", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 2

	result, err := callTool(t, srv, "claim_next", map[string]any{
		"agent":   "claude-code",
		"dry_run": true,
	})
	if err != nil {
		t.Fatalf("claim_next dry_run: %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, "== Constitution ==") {
		t.Errorf("dry_run should NOT include preamble (worker hasn't claimed yet), got:\n%s", text)
	}
}

func TestGetWorkContext_PrependsConstitutionPreamble(t *testing.T) {
	dir := writeConstitutionFiles(t)
	svc, repo := newTestServiceWithConstitutionDir(t, dir)
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Tasks = []domain.Task{
		{ID: 7, Title: "Add caching", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor", ContextID: "ctx-7"},
	}
	repo.state.WorkContexts = map[string]*domain.WorkContext{
		"ctx-7": {
			ID:            "ctx-7",
			TaskID:        7,
			RelevantFiles: []string{"cache.go"},
			Background:    "Adding LRU cache to the hot path",
		},
	}
	repo.state.NextTaskID = 8

	result, err := callTool(t, srv, "get_work_context", map[string]any{
		"task_id": float64(7),
	})
	if err != nil {
		t.Fatalf("get_work_context: %v", err)
	}
	text := resultText(t, result)
	if !strings.HasPrefix(text, "== Constitution ==") {
		t.Errorf("get_work_context should start with constitution preamble, got:\n%s", text)
	}
	if !strings.Contains(text, "Adding LRU cache to the hot path") {
		t.Errorf("preamble should be followed by work context body, got:\n%s", text)
	}
}

func TestGetWorkContext_MissingTaskContext_StillIncludesPreamble(t *testing.T) {
	dir := writeConstitutionFiles(t)
	svc, repo := newTestServiceWithConstitutionDir(t, dir)
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Tasks = []domain.Task{
		{ID: 9, Title: "Bare task", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 10

	result, err := callTool(t, srv, "get_work_context", map[string]any{
		"task_id": float64(9),
	})
	if err != nil {
		t.Fatalf("get_work_context: %v", err)
	}
	text := resultText(t, result)
	if !strings.HasPrefix(text, "== Constitution ==") {
		t.Errorf("preamble should still appear when no work context exists, got:\n%s", text)
	}
	if !strings.Contains(text, "No work context for task #9") {
		t.Errorf("expected 'no work context' message in body, got:\n%s", text)
	}
}

func TestTaskKindFromTitle(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Code review for PR #123", "review"},
		{"PR review of caching diff", "review"},
		{"Review the auth module", "review"},
		{"Implement caching", ""},
		{"Build worker pool", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := constitution.TaskKindFromTitle(tt.title); got != tt.want {
			t.Errorf("TaskKindFromTitle(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}

// erroringSource is a test-only constitution.Source that always
// returns the configured error. Local copy of the helper used in the
// constitution package's own tests — duplicated here to avoid an
// awkward cross-package test dependency, since the type only needs
// to satisfy the Source interface.
type erroringSource struct {
	name string
	err  error
}

func (e *erroringSource) Name() string { return e.name }
func (e *erroringSource) List(constitution.Scope) ([]constitution.File, error) {
	return nil, e.err
}

// TestConstitutionPreamble_PartialFailureKeepsGoodSource is the
// regression test for codex's MUST_FIX #2: when one source errors but
// another resolves cleanly, the rendered preamble must still include
// the good source's content. The previous implementation returned ""
// on any non-nil error, which silently dropped surviving rules from
// the worker prompt. The fix logs the error but keeps the good files.
func TestConstitutionPreamble_PartialFailureKeepsGoodSource(t *testing.T) {
	goodDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(goodDir, "alpha.md"),
		[]byte("alpha rule body"), 0o644); err != nil {
		t.Fatalf("write alpha.md: %v", err)
	}

	repo := newMockRepository()
	pol := newMockPolicy()
	pol.constitutionSources = []constitution.Source{
		&constitution.DirSource{SourceName: "good", Path: goodDir, Include: []string{"*.md"}},
		&erroringSource{name: "broken", err: io.ErrUnexpectedEOF},
	}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	srv := testServer(svc, logger)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Implement caching", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 2

	result, err := callTool(t, srv, "claim_next", map[string]any{
		"agent": "claude-code",
	})
	if err != nil {
		t.Fatalf("claim_next: %v", err)
	}
	text := resultText(t, result)
	if !strings.HasPrefix(text, "== Constitution ==") {
		t.Fatalf("partial-resolve must still render preamble, got:\n%s", text)
	}
	if !strings.Contains(text, "alpha.md") {
		t.Errorf("preamble must include surviving source, got:\n%s", text)
	}
}

// TestEndToEnd_PartialFailureSurvivesAcrossCallSites locks in MUST_FIX
// #2 across the hot-path tool callers of constitution.Resolve() —
// claim_next and get_work_context. Both must keep the surviving
// source's content when a sibling source returns an error. The third
// call site (worker spawn-prompt builder) is covered by
// TestResolvedConstitution_PartialFailureSurvivesAtSpawn in
// internal/app/worker_manager_test.go because resolvedConstitution
// lives in package app and is unexported.
//
// Without this guarantee, a single broken team source would silently
// strip every team rule from every worker prompt — the regression
// codex's MUST_FIX #2 caught.
func TestEndToEnd_PartialFailureSurvivesAcrossCallSites(t *testing.T) {
	goodDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(goodDir, "team-rules.md"),
		[]byte("team rule body"), 0o644); err != nil {
		t.Fatalf("write team-rules.md: %v", err)
	}

	repo := newMockRepository()
	pol := newMockPolicy()
	pol.constitutionSources = []constitution.Source{
		&constitution.DirSource{SourceName: "team", Path: goodDir, Include: []string{"*.md"}},
		&erroringSource{name: "remote-broken", err: io.ErrUnexpectedEOF},
	}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	srv := testServer(svc, logger)

	repo.state.Tasks = []domain.Task{
		{ID: 11, Title: "Touch all call sites", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3, ContextID: "ctx-11"},
	}
	repo.state.WorkContexts = map[string]*domain.WorkContext{
		"ctx-11": {
			ID:         "ctx-11",
			TaskID:     11,
			Background: "End-to-end partial-resolve test",
		},
	}
	repo.state.NextTaskID = 12

	claim, err := callTool(t, srv, "claim_next", map[string]any{"agent": "claude-code"})
	if err != nil {
		t.Fatalf("claim_next: %v", err)
	}
	claimText := resultText(t, claim)
	if !strings.HasPrefix(claimText, "== Constitution ==") {
		t.Fatalf("claim_next must still render preamble, got:\n%s", claimText)
	}
	if !strings.Contains(claimText, "team-rules.md") {
		t.Errorf("claim_next preamble should include surviving source, got:\n%s", claimText)
	}

	wc, err := callTool(t, srv, "get_work_context", map[string]any{"task_id": float64(11)})
	if err != nil {
		t.Fatalf("get_work_context: %v", err)
	}
	wcText := resultText(t, wc)
	if !strings.HasPrefix(wcText, "== Constitution ==") {
		t.Fatalf("get_work_context must still render preamble, got:\n%s", wcText)
	}
	if !strings.Contains(wcText, "team-rules.md") {
		t.Errorf("get_work_context preamble should include surviving source, got:\n%s", wcText)
	}
}

// blockingSource is a constitution.Source whose List blocks until
// the test releases it. Used to assert that the CollabService global
// mutex is NOT held during constitution.Resolve (Finding 2 from
// claude-code-task-34).
type blockingSource struct {
	name    string
	release <-chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (b *blockingSource) Name() string { return b.name }

func (b *blockingSource) List(constitution.Scope) ([]constitution.File, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil, nil
}

// TestClaimNext_DoesNotHoldServiceLockDuringConstitutionResolve locks
// in claude-code-task-34's Finding 2: the original implementation
// called constitutionPreambleForTask from inside the svc.Run callback,
// which holds the global CollabService mutex. A slow filesystem (NFS,
// network-mounted git cache, etc.) would head-of-line block every
// other tool call until the constitution scan finished. After the
// fix, the scope is captured under the lock and the I/O happens after
// Run returns.
//
// The test wires a blocking constitution Source: its List() parks
// indefinitely until released. While claim_next is parked there, a
// second goroutine fires send_message (which takes the same global
// lock via svc.Run). If the lock were still held, send_message would
// block; after the fix, it should complete promptly.
func TestClaimNext_DoesNotHoldServiceLockDuringConstitutionResolve(t *testing.T) {
	release := make(chan struct{})
	blocker := &blockingSource{
		name:    "slow-team-rules",
		release: release,
		entered: make(chan struct{}),
	}

	repo := newMockRepository()
	pol := newMockPolicy()
	pol.constitutionSources = []constitution.Source{blocker}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	srv := testServer(svc, logger)

	// Register both agents so ValidateAgent in claim_next +
	// send_message accepts them. send_message validates "to" with
	// allowAll=true, but "from" requires a known agent.
	repo.state.RegisteredAgents["claude-code"] = &domain.RegisteredAgent{Name: "claude-code", RegisteredAt: time.Now(), LastSeen: time.Now()}
	repo.state.RegisteredAgents["cursor"] = &domain.RegisteredAgent{Name: "cursor", RegisteredAt: time.Now(), LastSeen: time.Now()}
	repo.state.AgentInstances["claude-code"] = &domain.AgentInstance{InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, MaxTasks: 1, LastHeartbeat: time.Now()}
	repo.state.AgentInstances["cursor"] = &domain.AgentInstance{InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, MaxTasks: 1, LastHeartbeat: time.Now()}

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Drive lock test", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 2

	claimDone := make(chan struct{})
	go func() {
		defer close(claimDone)
		_, _ = callTool(t, srv, "claim_next", map[string]any{"agent": "claude-code"})
	}()

	// Give claim_next time to enter the blocking Source.List. If we
	// time out here, claim_next hasn't reached the lock-free I/O
	// stage at all — which is itself a regression worth failing on.
	select {
	case <-blocker.entered:
	case <-time.After(5 * time.Second):
		close(release)
		<-claimDone
		t.Fatal("claim_next never reached blocking constitution source: I/O may still be inside the locked region")
	}

	// At this point claim_next is parked inside Source.List. If the
	// fix holds, the global mutex is NOT held — so an unrelated
	// svc.Run-bound tool call should complete promptly. If the fix
	// regresses, this call deadlocks until the test times out and
	// `release` fires.
	probeDone := make(chan error, 1)
	go func() {
		_, err := callTool(t, srv, "send_message", map[string]any{
			"from":    "cursor",
			"to":      "claude-code",
			"content": "lock probe",
		})
		probeDone <- err
	}()

	probeFailed := false
	select {
	case err := <-probeDone:
		if err != nil {
			t.Errorf("probe send_message failed: %v", err)
			probeFailed = true
		}
	case <-time.After(5 * time.Second):
		// 5s budget so a healthy CI box (where the probe round-trip
		// is sub-50ms) still fails fast on a real lock leak — the
		// genuine deadlock holds until `release` fires after the
		// probeFailed branch — while absorbing the goroutine
		// scheduling jitter we've seen comparable Go tests stretch
		// to ~1-3s under CPU contention on shared CI runners.
		t.Error("send_message blocked while claim_next was inside constitution.Resolve — global lock leaked into the I/O path")
		probeFailed = true
	}

	// Always release claim_next so the goroutine exits before the
	// test returns; otherwise the test process leaks goroutines and
	// `<-claimDone` deadlocks.
	close(release)
	<-claimDone
	if probeFailed {
		t.FailNow()
	}
}

// TestGetWorkContext_DoesNotHoldServiceLockDuringConstitutionResolve
// is the get_work_context twin of the claim_next lock test above.
// Same Finding 2 motivation, different call site.
func TestGetWorkContext_DoesNotHoldServiceLockDuringConstitutionResolve(t *testing.T) {
	release := make(chan struct{})
	blocker := &blockingSource{
		name:    "slow-team-rules",
		release: release,
		entered: make(chan struct{}),
	}

	repo := newMockRepository()
	pol := newMockPolicy()
	pol.constitutionSources = []constitution.Source{blocker}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	srv := testServer(svc, logger)

	repo.state.RegisteredAgents["claude-code"] = &domain.RegisteredAgent{Name: "claude-code", RegisteredAt: time.Now(), LastSeen: time.Now()}
	repo.state.RegisteredAgents["cursor"] = &domain.RegisteredAgent{Name: "cursor", RegisteredAt: time.Now(), LastSeen: time.Now()}
	repo.state.AgentInstances["claude-code"] = &domain.AgentInstance{InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, MaxTasks: 1, LastHeartbeat: time.Now()}
	repo.state.AgentInstances["cursor"] = &domain.AgentInstance{InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, MaxTasks: 1, LastHeartbeat: time.Now()}

	repo.state.Tasks = []domain.Task{
		{ID: 7, Title: "Drive lock test", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor", ContextID: "ctx-7"},
	}
	repo.state.WorkContexts = map[string]*domain.WorkContext{
		"ctx-7": {ID: "ctx-7", TaskID: 7, Background: "lock test"},
	}
	repo.state.NextTaskID = 8

	wcDone := make(chan struct{})
	go func() {
		defer close(wcDone)
		_, _ = callTool(t, srv, "get_work_context", map[string]any{"task_id": float64(7)})
	}()

	select {
	case <-blocker.entered:
	case <-time.After(5 * time.Second):
		close(release)
		<-wcDone
		t.Fatal("get_work_context never reached blocking constitution source: I/O may still be inside the locked region")
	}

	probeDone := make(chan error, 1)
	go func() {
		_, err := callTool(t, srv, "send_message", map[string]any{
			"from":    "cursor",
			"to":      "claude-code",
			"content": "lock probe",
		})
		probeDone <- err
	}()

	probeFailed := false
	select {
	case err := <-probeDone:
		if err != nil {
			t.Errorf("probe send_message failed: %v", err)
			probeFailed = true
		}
	case <-time.After(5 * time.Second):
		// See TestClaimNext_DoesNotHoldServiceLockDuringConstitutionResolve
		// for the full rationale; same jitter budget here.
		t.Error("send_message blocked while get_work_context was inside constitution.Resolve — global lock leaked into the I/O path")
		probeFailed = true
	}

	close(release)
	<-wcDone
	if probeFailed {
		t.FailNow()
	}
}

func TestClaimNext_ScopeFilter_ReviewSourceAttachesOnlyToReviewTasks(t *testing.T) {
	globalDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(globalDir, "engineering.md"), []byte("ship tests"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reviewDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(reviewDir, "review-checklist.md"), []byte("look at error handling"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	repo := newMockRepository()
	pol := newMockPolicy()
	pol.constitutionSources = []constitution.Source{
		&constitution.DirSource{SourceName: "global", Path: globalDir, Include: []string{"*.md"}},
		&constitution.DirSource{
			SourceName: "review-rules",
			Path:       reviewDir,
			Include:    []string{"*.md"},
			Scope:      constitution.ScopeFilter{TaskKind: []string{"review"}},
		},
	}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	srv := testServer(svc, logger)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Implement feature X", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3},
		{ID: 2, Title: "Code review for PR #777", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 3

	res1, err := callTool(t, srv, "claim_next", map[string]any{"agent": "claude-code"})
	if err != nil {
		t.Fatalf("claim_next #1: %v", err)
	}
	text1 := resultText(t, res1)
	if !strings.Contains(text1, "engineering.md") {
		t.Errorf("feature task should attach engineering.md, got:\n%s", text1)
	}
	if strings.Contains(text1, "review-checklist.md") {
		t.Errorf("feature task should NOT attach review-checklist.md, got:\n%s", text1)
	}

	for i := range repo.state.Tasks {
		if repo.state.Tasks[i].ID == 1 {
			repo.state.Tasks[i].Status = "completed"
		}
	}

	res2, err := callTool(t, srv, "claim_next", map[string]any{"agent": "claude-code"})
	if err != nil {
		t.Fatalf("claim_next #2: %v", err)
	}
	text2 := resultText(t, res2)
	if !strings.Contains(text2, "engineering.md") {
		t.Errorf("review task should attach engineering.md, got:\n%s", text2)
	}
	if !strings.Contains(text2, "review-checklist.md") {
		t.Errorf("review task should attach review-checklist.md, got:\n%s", text2)
	}
}
