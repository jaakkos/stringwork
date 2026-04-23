package sqlite

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectState_MissingPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")

	got := InspectState(path)

	if !got.Fresh {
		t.Errorf("Fresh = false, want true for missing file")
	}
	if got.PreOpenSize != 0 {
		t.Errorf("PreOpenSize = %d, want 0", got.PreOpenSize)
	}
	if len(got.Backups) != 0 {
		t.Errorf("Backups = %v, want empty", got.Backups)
	}
	if got.Path != path {
		t.Errorf("Path = %q, want %q", got.Path, path)
	}
}

func TestInspectState_ZeroByteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := InspectState(path)

	if !got.Fresh {
		t.Errorf("Fresh = false, want true for zero-byte file")
	}
	if got.PreOpenSize != 0 {
		t.Errorf("PreOpenSize = %d, want 0", got.PreOpenSize)
	}
}

func TestInspectState_PopulatedDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	payload := []byte("not a real sqlite header but non-empty")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := InspectState(path)

	if got.Fresh {
		t.Errorf("Fresh = true, want false for populated file")
	}
	if got.PreOpenSize != int64(len(payload)) {
		t.Errorf("PreOpenSize = %d, want %d", got.PreOpenSize, len(payload))
	}
}

func TestInspectState_BackupsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	older := filepath.Join(dir, "state.sqlite.bak.1000000000")
	newer := filepath.Join(dir, "state.sqlite.bak.2000000000")
	if err := os.WriteFile(older, []byte("o"), 0600); err != nil {
		t.Fatalf("write older: %v", err)
	}
	if err := os.WriteFile(newer, []byte("nn"), 0600); err != nil {
		t.Fatalf("write newer: %v", err)
	}
	// Force distinct modtimes so the sort has something stable to do.
	pastTime := time.Now().Add(-2 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(older, pastTime, pastTime); err != nil {
		t.Fatalf("chtimes older: %v", err)
	}
	if err := os.Chtimes(newer, recent, recent); err != nil {
		t.Fatalf("chtimes newer: %v", err)
	}

	got := InspectState(path)

	if len(got.Backups) != 2 {
		t.Fatalf("Backups len = %d, want 2", len(got.Backups))
	}
	if got.Backups[0].Path != newer {
		t.Errorf("Backups[0] = %q, want newer %q", got.Backups[0].Path, newer)
	}
	if got.Backups[1].Path != older {
		t.Errorf("Backups[1] = %q, want older %q", got.Backups[1].Path, older)
	}
	if got.Backups[0].Size != 2 {
		t.Errorf("Backups[0].Size = %d, want 2", got.Backups[0].Size)
	}
}

func TestInspectState_DiscoversUserNamedBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")

	manual := filepath.Join(dir, "state.sqlite.bak.manual")
	if err := os.WriteFile(manual, []byte("m"), 0600); err != nil {
		t.Fatalf("write manual: %v", err)
	}

	got := InspectState(path)

	if len(got.Backups) != 1 {
		t.Fatalf("Backups len = %d, want 1 (user-named .bak.manual)", len(got.Backups))
	}
	if got.Backups[0].Path != manual {
		t.Errorf("Backups[0] = %q, want %q", got.Backups[0].Path, manual)
	}
}

func TestInspectState_IgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	// Sidecars and unrelated files in the same dir must not be reported as
	// backups.
	for _, name := range []string{
		"state.sqlite-wal",
		"state.sqlite-shm",
		"state.sqlite.notes",
		"config.yaml",
		"server.sock",
		"daemon.pid",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("."), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got := InspectState(path)

	if len(got.Backups) != 0 {
		t.Errorf("Backups = %v, want empty (none of the siblings match .bak.*)", got.Backups)
	}
}

func TestInspectState_IgnoresBackupDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "state.sqlite.bak.dir"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := InspectState(path)

	if len(got.Backups) != 0 {
		t.Errorf("Backups = %v, want empty (directories are not backups)", got.Backups)
	}
}
