package sqlite

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestLogger() (*log.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return log.New(&buf, "", 0), &buf
}

// listBackups returns auto-generated backup files only (state.sqlite.bak.<digits>).
func listAutoBackups(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "state.sqlite.bak.*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	re := regexp.MustCompile(`\.bak\.\d+$`)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if re.MatchString(m) {
			out = append(out, m)
		}
	}
	return out
}

func TestRotateBackups_NoSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	logger, buf := newTestLogger()

	RotateBackups(path, BackupOptions{Enabled: true, KeepN: 5}, logger)

	if got := listAutoBackups(t, dir); len(got) != 0 {
		t.Errorf("expected no backups when source is missing, got %v", got)
	}
	if strings.Contains(buf.String(), "stat") {
		t.Errorf("expected silent no-op for missing source, log=%q", buf.String())
	}
}

func TestRotateBackups_DisabledIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	logger, buf := newTestLogger()

	RotateBackups(path, BackupOptions{Enabled: false, KeepN: 5}, logger)

	if got := listAutoBackups(t, dir); len(got) != 0 {
		t.Errorf("expected no backups when disabled, got %v", got)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output when disabled, got %q", buf.String())
	}
}

func TestRotateBackups_ZeroByteSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	logger, _ := newTestLogger()

	RotateBackups(path, BackupOptions{Enabled: true, KeepN: 5}, logger)

	if got := listAutoBackups(t, dir); len(got) != 0 {
		t.Errorf("expected no backup for zero-byte source, got %v", got)
	}
}

func TestRotateBackups_CreatesBackupWithUnixSecondsName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	payload := []byte("hello world")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	logger, _ := newTestLogger()

	RotateBackups(path, BackupOptions{Enabled: true, KeepN: 5}, logger)

	got := listAutoBackups(t, dir)
	if len(got) != 1 {
		t.Fatalf("expected 1 backup, got %v", got)
	}
	if !regexp.MustCompile(`state\.sqlite\.bak\.\d{9,11}$`).MatchString(got[0]) {
		t.Errorf("backup name %q does not match state.sqlite.bak.<unix-seconds>", got[0])
	}

	// Content must match the source byte-for-byte.
	contents, err := os.ReadFile(got[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(contents, payload) {
		t.Errorf("backup content mismatch: got %q, want %q", contents, payload)
	}
}

func TestRotateBackups_PrunesOldestKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, []byte("live"), 0600); err != nil {
		t.Fatalf("write live: %v", err)
	}

	// Pre-seed 5 existing backups with strictly increasing timestamps in the
	// past (so the new one written by RotateBackups becomes the 6th and the
	// oldest must be pruned).
	now := time.Now().Unix()
	for i := 1; i <= 5; i++ {
		name := filepath.Join(dir, "state.sqlite.bak.")
		ts := now - int64(1000-i) // 995, 996, 997, 998, 999 seconds ago
		if err := os.WriteFile(name+formatInt(ts), []byte("old"), 0600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	logger, _ := newTestLogger()
	RotateBackups(path, BackupOptions{Enabled: true, KeepN: 5}, logger)

	got := listAutoBackups(t, dir)
	if len(got) != 5 {
		t.Fatalf("expected exactly KeepN=5 backups after rotation, got %d: %v", len(got), got)
	}

	// The oldest seeded one (now-999) must be gone.
	pruned := filepath.Join(dir, "state.sqlite.bak."+formatInt(now-999))
	if _, err := os.Stat(pruned); !os.IsNotExist(err) {
		t.Errorf("expected oldest backup %s to be pruned, stat err=%v", pruned, err)
	}

	// The newly-written backup (timestamp ~= now) must exist.
	hasNew := false
	for _, p := range got {
		base := filepath.Base(p)
		if base > "state.sqlite.bak."+formatInt(now-1) {
			hasNew = true
			break
		}
	}
	if !hasNew {
		t.Errorf("expected the newly-written backup with current timestamp, got %v", got)
	}
}

func TestRotateBackups_LeavesUserNamedBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, []byte("live"), 0600); err != nil {
		t.Fatalf("write live: %v", err)
	}

	manual := filepath.Join(dir, "state.sqlite.bak.before-migration")
	if err := os.WriteFile(manual, []byte("manual"), 0600); err != nil {
		t.Fatalf("write manual: %v", err)
	}

	// Create more auto-backups than KeepN so pruning runs, then verify the
	// manual one survives even though it matches state.sqlite.bak.*.
	now := time.Now().Unix()
	for i := 0; i < 6; i++ {
		ts := now - int64(100-i)
		if err := os.WriteFile(filepath.Join(dir, "state.sqlite.bak."+formatInt(ts)), []byte("auto"), 0600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	logger, _ := newTestLogger()
	RotateBackups(path, BackupOptions{Enabled: true, KeepN: 3}, logger)

	if _, err := os.Stat(manual); err != nil {
		t.Errorf("user-named backup must be preserved, stat err=%v", err)
	}
	if got := listAutoBackups(t, dir); len(got) != 3 {
		t.Errorf("auto-backups should be pruned to KeepN=3, got %d: %v", len(got), got)
	}
}

func TestRotateBackups_NilLoggerDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RotateBackups panicked with nil logger: %v", r)
		}
	}()
	RotateBackups(path, BackupOptions{Enabled: true, KeepN: 5}, nil)
}

func TestRotateBackups_ReadOnlyDirIsBestEffort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based read-only dirs do not work the same on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod-based perms are bypassed")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod ro: %v", err)
	}
	// Restore perms so t.TempDir() cleanup can remove the directory.
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	logger, buf := newTestLogger()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RotateBackups panicked on read-only dir: %v", r)
		}
	}()
	RotateBackups(path, BackupOptions{Enabled: true, KeepN: 5}, logger)

	if !strings.Contains(buf.String(), "backup:") {
		t.Errorf("expected a 'backup:' log entry on copy failure, got %q", buf.String())
	}

	// No new file should exist (and pre-existing file is the only one).
	matches, _ := filepath.Glob(filepath.Join(dir, "state.sqlite.bak.*"))
	if len(matches) != 0 {
		t.Errorf("expected no backups created on read-only dir, got %v", matches)
	}
}

// formatInt is a tiny helper so the seed loops above don't pull in fmt just
// to build filenames.
func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
