package tasktemplates

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// loadTemplateDir reads a single template directory from any fs.FS
// implementation (os.DirFS for DirSource, embed.FS for EmbedSource) and
// returns the raw TemplateFile. dir is the relative path inside fsys to
// the template's root (e.g. "code-review" for an embedded layout under
// "defaults/task-templates/code-review", or "regfin-domain" for a
// regfin-devtools team overlay).
//
// Three files are read when present:
//
//   - <dir>/template.yaml    — required; absent → error.
//   - <dir>/routing.yaml     — optional; absent → empty Routing slice.
//   - <dir>/classifiers.yaml — optional; absent → empty Classifier slice.
//
// Plus every <dir>/checklists/*.md file (any markdown body keyed by its
// stem). YAML decoding runs in KnownFields(true) so a typo in any
// declared field surfaces as a clear "parse" error rather than a
// silently-dropped value. Within each file, duplicate ids (aspect,
// routing rule, classifier) are surfaced as errors so a copy-paste typo
// can't shadow another rule.
//
// sourceName is recorded on the returned TemplateFile so the merger can
// label per-aspect / per-rule provenance. Callers should use
// the source's Name() (e.g. "global", "regfin-task-templates") so
// `task-template show` lists provenance the user can recognise.
func loadTemplateDir(fsys fs.FS, dir, sourceName string) (TemplateFile, error) {
	tf := TemplateFile{
		Source:     sourceName,
		Checklists: map[string]ChecklistBody{},
	}

	tplPath := path.Join(dir, "template.yaml")
	tplData, err := fs.ReadFile(fsys, tplPath)
	if err != nil {
		return tf, fmt.Errorf("read %s: %w", tplPath, err)
	}
	var raw rawTemplateYAML
	if err := decodeStrict(tplData, &raw, tplPath); err != nil {
		return tf, err
	}
	tf.ID = strings.TrimSpace(raw.ID)
	tf.Title = strings.TrimSpace(raw.Title)
	tf.Desc = strings.TrimSpace(raw.Description)
	if tf.ID == "" {
		return tf, fmt.Errorf("%s: id is required", tplPath)
	}
	tf.Inputs = InputDeclaration{
		Required:     append([]string(nil), raw.Inputs.Required...),
		Declarations: copyMap(raw.Inputs.Declarations),
	}
	seenAspects := map[string]struct{}{}
	for _, a := range raw.Aspects {
		id := strings.TrimSpace(a.ID)
		if id == "" {
			return tf, fmt.Errorf("%s: aspect missing id", tplPath)
		}
		if _, dup := seenAspects[id]; dup {
			return tf, fmt.Errorf("%s: duplicate aspect id %q", tplPath, id)
		}
		seenAspects[id] = struct{}{}
		tf.Aspects = append(tf.Aspects, Aspect{
			ID:          id,
			Title:       strings.TrimSpace(a.Title),
			Description: strings.TrimSpace(a.Description),
			Source:      sourceName,
		})
	}

	if data, err := fs.ReadFile(fsys, path.Join(dir, "routing.yaml")); err == nil {
		var r rawRoutingYAML
		if err := decodeStrict(data, &r, path.Join(dir, "routing.yaml")); err != nil {
			return tf, err
		}
		seen := map[string]struct{}{}
		for _, rule := range r.Routing {
			id := strings.TrimSpace(rule.ID)
			if id == "" {
				return tf, fmt.Errorf("%s/routing.yaml: rule missing id", dir)
			}
			if _, dup := seen[id]; dup {
				return tf, fmt.Errorf("%s/routing.yaml: duplicate rule id %q", dir, id)
			}
			seen[id] = struct{}{}
			tf.Routing = append(tf.Routing, RoutingRule{
				ID:         id,
				When:       strings.TrimSpace(rule.When),
				WhenTags:   append([]string(nil), rule.WhenTags...),
				Spawn:      strings.TrimSpace(rule.Spawn),
				ExtraFocus: strings.TrimSpace(rule.ExtraFocus),
				Disabled:   rule.Disabled,
				Source:     sourceName,
			})
		}
	}

	if data, err := fs.ReadFile(fsys, path.Join(dir, "classifiers.yaml")); err == nil {
		var c rawClassifiersYAML
		if err := decodeStrict(data, &c, path.Join(dir, "classifiers.yaml")); err != nil {
			return tf, err
		}
		seen := map[string]struct{}{}
		for _, item := range c.Classifiers {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				return tf, fmt.Errorf("%s/classifiers.yaml: classifier missing id", dir)
			}
			if _, dup := seen[id]; dup {
				return tf, fmt.Errorf("%s/classifiers.yaml: duplicate classifier id %q", dir, id)
			}
			seen[id] = struct{}{}
			tf.Classifier = append(tf.Classifier, Classifier{
				ID:       id,
				Pattern:  strings.TrimSpace(item.Pattern),
				Tag:      strings.TrimSpace(item.Tag),
				Disabled: item.Disabled,
				Source:   sourceName,
			})
		}
	}

	checklistDir := path.Join(dir, "checklists")
	entries, err := fs.ReadDir(fsys, checklistDir)
	if err == nil {
		// Walk in sorted order so dir-source / embed-source produce the
		// same TemplateFile shape regardless of underlying FS iteration.
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			aspect := strings.TrimSuffix(name, ".md")
			body, err := fs.ReadFile(fsys, path.Join(checklistDir, name))
			if err != nil {
				return tf, fmt.Errorf("read %s/%s: %w", checklistDir, name, err)
			}
			cb, err := parseChecklistBody(body, path.Join(checklistDir, name))
			if err != nil {
				return tf, err
			}
			tf.Checklists[aspect] = cb
		}
	}

	return tf, nil
}

// parseChecklistBody splits the optional YAML front-matter from the
// markdown body. Front-matter MUST start at byte 0 with a "---\n" line
// and be terminated by a "---\n" line on its own; anything else is
// treated as content (so a markdown horizontal rule that legitimately
// starts with `---` is never mis-parsed). Today the only meaningful
// front-matter key is `replace: true`; unknown keys parse without
// error so the format can extend without breaking older parsers.
//
// This strictness is part of the contract documented in
// docs/TASK_TEMPLATES.md — the v3 plan calls it out as an
// implementation note. Doctor exercises this code path on every
// checklist file so a malformed front-matter surfaces during
// validation rather than at spawn time.
func parseChecklistBody(raw []byte, sourcePath string) (ChecklistBody, error) {
	if !bytes.HasPrefix(raw, []byte("---\n")) && !bytes.HasPrefix(raw, []byte("---\r\n")) {
		return ChecklistBody{Body: string(raw)}, nil
	}
	// Strip the leading marker line and search for the closing one.
	rest := raw
	if bytes.HasPrefix(rest, []byte("---\r\n")) {
		rest = rest[5:]
	} else {
		rest = rest[4:]
	}
	end := bytes.Index(rest, []byte("\n---\n"))
	endLen := 5
	if end < 0 {
		end = bytes.Index(rest, []byte("\n---\r\n"))
		endLen = 6
	}
	if end < 0 {
		return ChecklistBody{Body: string(raw)}, nil
	}
	frontMatter := rest[:end]
	body := rest[end+endLen:]
	var fm struct {
		Replace bool `yaml:"replace"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(frontMatter))
	dec.KnownFields(false)
	if err := dec.Decode(&fm); err != nil {
		return ChecklistBody{}, fmt.Errorf("parse %s front-matter: %w", sourcePath, err)
	}
	return ChecklistBody{
		Body:    strings.TrimLeft(string(body), "\n"),
		Replace: fm.Replace,
	}, nil
}

func decodeStrict(data []byte, into any, sourcePath string) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("parse %s: %w", sourcePath, err)
	}
	return nil
}

func copyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// listTemplateDirs returns the relative paths of all template
// directories one level under root. A template directory is any direct
// child that contains a template.yaml. Sub-sub-directories are not
// scanned because templates are intentionally flat — keeps the embed
// layout and the team overlay layout aligned.
func listTemplateDirs(fsys fs.FS, root string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := path.Join(root, e.Name())
		if _, err := fs.Stat(fsys, path.Join(candidate, "template.yaml")); err == nil {
			out = append(out, candidate)
		}
	}
	sort.Strings(out)
	return out, nil
}

// rawTemplateYAML is the wire shape for template.yaml. Kept private so
// the public Template type stays focused on the merged result and the
// raw shape can change without breaking downstream consumers.
type rawTemplateYAML struct {
	ID          string          `yaml:"id"`
	Title       string          `yaml:"title"`
	Description string          `yaml:"description,omitempty"`
	Inputs      rawInputsYAML   `yaml:"inputs"`
	Aspects     []rawAspectYAML `yaml:"aspects"`
}

type rawInputsYAML struct {
	Required     []string          `yaml:"required,omitempty"`
	Declarations map[string]string `yaml:"declarations,omitempty"`
}

type rawAspectYAML struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Description string `yaml:"description,omitempty"`
}

type rawRoutingYAML struct {
	Routing []rawRoutingRuleYAML `yaml:"routing"`
}

type rawRoutingRuleYAML struct {
	ID         string   `yaml:"id"`
	When       string   `yaml:"when,omitempty"`
	WhenTags   []string `yaml:"when_tags,omitempty"`
	Spawn      string   `yaml:"spawn"`
	ExtraFocus string   `yaml:"extra_focus,omitempty"`
	Disabled   bool     `yaml:"disabled,omitempty"`
}

type rawClassifiersYAML struct {
	Classifiers []rawClassifierYAML `yaml:"classifiers"`
}

type rawClassifierYAML struct {
	ID       string `yaml:"id"`
	Pattern  string `yaml:"pattern"`
	Tag      string `yaml:"tag"`
	Disabled bool   `yaml:"disabled,omitempty"`
}
