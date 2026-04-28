package tasktemplates

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DirSource is a Source backed by a filesystem directory. Path is the
// directory containing template directories — each direct subdirectory
// with a template.yaml is one template. SourceName overrides the
// displayed source label; when empty the source uses the directory
// basename.
//
// A missing Path is NOT an error: a user with no overlays simply
// contributes no templates. Per-template read errors ARE returned (the
// merge surface assumes one bad team overlay should surface, not be
// silently dropped) so config typos are caught at doctor time rather
// than at task spawn time.
type DirSource struct {
	SourceName string
	Path       string
}

// Name returns the configured SourceName or the directory basename.
// Used for diagnostics and provenance — must be stable across runs so
// `task-template show` and merge order are deterministic.
func (d *DirSource) Name() string {
	if d == nil {
		return "(nil)"
	}
	if d.SourceName != "" {
		return d.SourceName
	}
	if d.Path == "" {
		return "(unnamed)"
	}
	return filepath.Base(d.Path)
}

// List walks Path and returns one TemplateFile per template directory.
// Returns nil when Path is empty or missing on disk; a present-but-not-
// a-directory path is an error so a misconfigured `path: /etc/passwd`
// produces a clear diagnostic.
func (d *DirSource) List() ([]TemplateFile, error) {
	if d == nil || d.Path == "" {
		return nil, nil
	}
	info, err := os.Stat(d.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", d.Path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", d.Path)
	}

	fsys := os.DirFS(d.Path)
	dirs, err := listTemplateDirs(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("list templates in %s: %w", d.Path, err)
	}
	out := make([]TemplateFile, 0, len(dirs))
	for _, dir := range dirs {
		tf, err := loadTemplateDir(fsys, dir, d.Name())
		if err != nil {
			return nil, fmt.Errorf("load template %s in %s: %w", dir, d.Path, err)
		}
		out = append(out, tf)
	}
	return out, nil
}
