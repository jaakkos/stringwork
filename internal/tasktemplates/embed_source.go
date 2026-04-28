package tasktemplates

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
)

// defaultsFS holds the shipped task templates baked into the binary.
// Path layout: defaults/task-templates/<id>/{template.yaml, routing.yaml,
// classifiers.yaml, checklists/*.md}. The "task-templates" intermediate
// directory leaves room for sibling defaults categories (e.g. agent
// prompt templates) without renaming the embed root.
//
//go:embed all:defaults
var defaultsFS embed.FS

// DefaultEmbeddedSource returns the Source that contributes Stringwork's
// shipped defaults. It is always the first source in the resolver list
// so default aspects, routing, and classifiers are present even when no
// team overlay is configured.
//
// SourceName is hard-coded to "stringwork-defaults" so messages like
// "aspect 'security' contributed by stringwork-defaults" identify the
// origin unambiguously across CLI / MCP / log output.
func DefaultEmbeddedSource() Source {
	return &EmbedSource{
		SourceName: "stringwork-defaults",
		FS:         defaultsFS,
		Root:       "defaults/task-templates",
	}
}

// EmbedSource is a Source backed by an embed.FS. FS holds the embedded
// tree and Root is the path inside FS that contains template
// directories (each direct subdirectory of Root with a template.yaml is
// one template). SourceName overrides the displayed label.
//
// Splitting EmbedSource from DirSource keeps the two underlying FS
// implementations behind a single, parallel Source contract — the
// resolver doesn't care whether a template's bytes came from disk or
// from the binary.
type EmbedSource struct {
	SourceName string
	FS         embed.FS
	Root       string
}

// Name returns SourceName or "(embed)" when unset. The default Source
// always sets SourceName so this fallback is for hand-built EmbedSource
// instances in tests.
func (e *EmbedSource) Name() string {
	if e == nil {
		return "(nil)"
	}
	if e.SourceName != "" {
		return e.SourceName
	}
	return "(embed)"
}

// List walks Root inside the embed.FS and returns one TemplateFile per
// template directory. Empty / missing Root is NOT an error (consistent
// with DirSource): a future binary without baked-in defaults still
// satisfies the Source contract.
func (e *EmbedSource) List() ([]TemplateFile, error) {
	if e == nil {
		return nil, nil
	}
	root := e.Root
	if root == "" {
		root = "."
	}
	if _, err := fs.Stat(e.FS, root); err != nil {
		// Missing embed root is a no-op. Other stat errors (corrupt
		// embed?) bubble up so a build problem is loud.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat embed root %q: %w", root, err)
	}
	dirs, err := listTemplateDirs(e.FS, root)
	if err != nil {
		return nil, fmt.Errorf("list embed templates in %q: %w", root, err)
	}
	out := make([]TemplateFile, 0, len(dirs))
	for _, dir := range dirs {
		tf, err := loadTemplateDir(e.FS, dir, e.Name())
		if err != nil {
			return nil, fmt.Errorf("load embed template %s: %w", dir, err)
		}
		out = append(out, tf)
	}
	return out, nil
}
