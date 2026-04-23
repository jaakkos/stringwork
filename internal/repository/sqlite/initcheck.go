package sqlite

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// InitState describes the on-disk state of a SQLite database file as observed
// before it was opened. Callers use this to distinguish a first-time install
// (no DB present, no backups) from an unintended wipe (no DB present, but
// backups nearby that could be restored).
type InitState struct {
	// Path is the absolute or relative path that was inspected.
	Path string
	// Fresh is true when the file does not exist or is zero bytes. After
	// sqlite.New runs the file always exists, so callers MUST capture this
	// before opening.
	Fresh bool
	// PreOpenSize is the file size in bytes before opening (0 when Fresh).
	PreOpenSize int64
	// Backups lists state.sqlite.bak.* siblings discovered in the same
	// directory at inspection time, sorted newest-first by modtime.
	Backups []BackupRef
}

// BackupRef describes a single backup file discovered next to the state DB.
type BackupRef struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// InspectState performs a pre-open check on the DB path. It MUST be called
// before sqlite.New() — sql.Open creates the file on first connection, after
// which the "did this exist?" signal is permanently lost.
//
// The function never returns an error; missing files, unreadable directories,
// and other I/O hiccups all collapse into a "Fresh, no backups" reading.
// Callers can act on InitState without defensive error handling.
func InspectState(path string) InitState {
	state := InitState{Path: path}

	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		state.Fresh = true
	case err != nil:
		// Stat failed for a reason other than ENOENT (permission, etc.).
		// Treat as fresh so the caller surfaces the warning, but leave
		// PreOpenSize at 0.
		state.Fresh = true
	default:
		state.PreOpenSize = info.Size()
		state.Fresh = info.Size() == 0
	}

	state.Backups = discoverBackups(path)
	return state
}

// discoverBackups returns sibling files matching <basename>.bak.* in path's
// directory, sorted newest-first by modtime. Best-effort — directory read or
// stat failures yield an empty slice.
func discoverBackups(path string) []BackupRef {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	matches, err := filepath.Glob(filepath.Join(dir, base+".bak.*"))
	if err != nil || len(matches) == 0 {
		return nil
	}

	refs := make([]BackupRef, 0, len(matches))
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			continue
		}
		refs = append(refs, BackupRef{
			Path:    m,
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].ModTime.After(refs[j].ModTime)
	})
	return refs
}
