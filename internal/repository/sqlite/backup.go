package sqlite

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// BackupOptions controls RotateBackups behaviour.
type BackupOptions struct {
	// Enabled gates the entire operation. When false, RotateBackups is a no-op.
	Enabled bool
	// KeepN is how many auto-generated backups to retain after writing the new
	// one. Values <= 0 fall back to a sensible default (5).
	KeepN int
}

// autoBackupSuffix matches the timestamp suffix this package writes:
// state.sqlite.bak.<unix-seconds>. We only ever prune files that match this
// pattern so user-named backups (e.g. state.sqlite.bak.before-migration) are
// preserved untouched.
var autoBackupSuffix = regexp.MustCompile(`\.bak\.(\d+)$`)

// RotateBackups copies path -> path.bak.<unix-seconds> if path exists, then
// prunes auto-generated siblings down to opts.KeepN newest. The operation is
// best-effort: every failure (source missing, copy error, prune error) is
// logged via logger but never returned, because a backup failure must NEVER
// prevent the server from starting.
//
// Naming matches the convention already on disk
// (state.sqlite.bak.1776836104) — seconds since epoch, not nanoseconds, so
// lexicographic and numeric sort order coincide for at least the next century.
func RotateBackups(path string, opts BackupOptions, logger *log.Logger) {
	if !opts.Enabled {
		return
	}
	keep := opts.KeepN
	if keep <= 0 {
		keep = 5
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		// Nothing to back up on a fresh install — the warning path in
		// initializeServer handles that case loud and clear.
		return
	}
	if err != nil {
		logf(logger, "backup: stat %s: %v (skipping)", path, err)
		return
	}
	if info.Size() == 0 {
		// Empty file is indistinguishable from a fresh DB; backing up
		// zero bytes adds nothing recoverable.
		return
	}

	dest := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := copyFile(path, dest); err != nil {
		logf(logger, "backup: copy %s -> %s: %v", path, dest, err)
		return
	}
	logf(logger, "backup: wrote %s (%d bytes)", filepath.Base(dest), info.Size())

	pruneAutoBackups(path, keep, logger)
}

// pruneAutoBackups removes auto-generated backups beyond the newest keep.
// Only files matching path + ".bak.<digits>" are eligible — user-named
// backups (path + ".bak.<anything-else>") are left alone.
func pruneAutoBackups(path string, keep int, logger *log.Logger) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	matches, err := filepath.Glob(filepath.Join(dir, base+".bak.*"))
	if err != nil {
		logf(logger, "backup: glob %s: %v", dir, err)
		return
	}

	type entry struct {
		path string
		ts   int64
	}
	var auto []entry
	for _, m := range matches {
		sub := autoBackupSuffix.FindStringSubmatch(m)
		if sub == nil {
			continue
		}
		ts, err := strconv.ParseInt(sub[1], 10, 64)
		if err != nil {
			continue
		}
		auto = append(auto, entry{path: m, ts: ts})
	}

	if len(auto) <= keep {
		return
	}

	sort.Slice(auto, func(i, j int) bool { return auto[i].ts > auto[j].ts })

	for _, e := range auto[keep:] {
		if err := os.Remove(e.path); err != nil {
			logf(logger, "backup: prune %s: %v", e.path, err)
			continue
		}
		logf(logger, "backup: pruned %s", filepath.Base(e.path))
	}
}

// copyFile copies src to dst with mode 0600 (matching the SQLite default).
// Uses a fresh O_EXCL create so we never clobber an existing backup if two
// processes ever race within the same wall-clock second.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// logf is a nil-tolerant log helper so callers can pass a nil logger in tests.
func logf(logger *log.Logger, format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Printf(format, args...)
}
