// Package tasktemplates loads, merges, and plans "task templates" — declarative
// descriptions of a kind of work that decomposes into worker-assignable Aspects.
//
// A template is YAML + markdown, no Go code. Each template ships:
//
//   - template.yaml    — id, title, inputs declaration, ordered aspects.
//   - routing.yaml     — rules mapping classified inputs to aspects.
//   - classifiers.yaml — file-glob-to-tag rules consumed by routing.
//   - checklists/<aspect>.md — markdown content embedded into the
//     PlannedAspect.Description that task_plan returns.
//
// Sources own discovery (filesystem dir, embedded FS, future flavours).
// Resolve(id, sources) merges the named template across sources by:
//
//   - Aspects: replace-by-id within an aspects list, earlier-source-wins.
//   - Routing / Classifiers: append, slice order preserved across sources.
//     Disabled rules are kept on the merged Template and filtered by Plan.
//   - Checklists: concatenate per aspect, defaults-first, unless a team
//     file declares "replace: true" YAML front-matter (full replacement).
//
// Plan(inputs, template) is pure: validate inputs against the template's
// freeform `required` list, classify inputs into tags via Classifiers,
// then evaluate Routing rules in declaration order to produce an ordered
// PlannedAspect list. Each PlannedAspect carries the composed checklist
// for its aspect so the driver can pass it straight through to
// create_task as the worker description.
//
// # Why this Resolve signature differs from constitution.Resolve
//
// constitution.Resolve(sources, scope) returns the full applicable set
// of files for a task because the worker must read every rule — the
// constitution is "everything that always applies, scoped". Templates
// are the opposite: the planner needs ONE specific template by id, then
// merges only that one across sources. Hence Resolve(id, sources) here.
// They share the layered-source idea but address different concerns —
// keep them as parallel parts of the system, not a single abstraction.
package tasktemplates

import (
	"fmt"
	"sort"
	"strings"
)

// Template is one merged kind of work after defaults + overlays are
// composed. ID and Title come from the first source declaring this id;
// later sources can override (replace-by-id) at the Aspect level only.
//
// Source is the name of the source the merged template originated in
// (used for diagnostics — "where did this aspect's title come from?").
type Template struct {
	ID          string
	Title       string
	Description string
	Source      string

	Inputs      InputDeclaration
	Aspects     []Aspect
	Routing     []RoutingRule
	Classifiers []Classifier

	// Checklists holds composed markdown bodies keyed by aspect id.
	// A missing key means no checklist content for that aspect (the
	// PlannedAspect.Description still includes background and finding
	// format, but no aspect-specific guidance).
	Checklists map[string]string
}

// InputDeclaration is the freeform input contract for a template.
// Required lists keys that MUST be present on the inputs map passed to
// Plan. Declarations is a primitive type tag per declared key —
// "string" or "list" — used by the validator. Unknown keys on the input
// map are preserved on PlannedAspect.Inputs but unused by default
// routing; teams can read them in their own routing rules.
type InputDeclaration struct {
	Required     []string
	Declarations map[string]string
}

// Aspect is one worker-assignable focus area within a template. ID is
// stable across sources (the merge key for replace-by-id). Title and
// Description are surfaced to the worker via the composed task
// description. Source is the name of the source that contributed this
// aspect to the merged template.
type Aspect struct {
	ID          string
	Title       string
	Description string
	Source      string
}

// RoutingRule maps classified inputs to one aspect to spawn. When is
// "always" or "" (the latter means "only when WhenTags fires"). WhenTags
// is the set of tags ANY of which trigger this rule (OR semantics).
// ExtraFocus is markdown appended to the spawned aspect's checklist when
// this rule fires — a place to drop "and also check X" guidance specific
// to the rule's scope without polluting the base checklist.
//
// Disabled rules are kept on the merged Template (so doctor can report
// them) but filtered out by Plan. The escape hatch lets a team disable a
// noisy default rule by id.
type RoutingRule struct {
	ID         string
	Source     string
	When       string // "always" or "" — empty means tag-driven
	WhenTags   []string
	Spawn      string // aspect id
	ExtraFocus string
	Disabled   bool
}

// Classifier tags a file path matching Pattern with Tag. Pattern is a
// shell-style glob evaluated against each file path passed to Classify.
// Disabled classifiers are kept on the merged Template (so doctor can
// report them) but filtered out by Classify.
type Classifier struct {
	ID       string
	Source   string
	Pattern  string
	Tag      string
	Disabled bool
}

// PlannedAspect is one worker task produced by Plan. The driver passes
// this straight to create_task: Title becomes the task title,
// Description includes the composed background + checklist + finding
// format, RelevantFiles becomes the task's relevant_files,
// Constraints become the task's constraints. Template + Aspect are the
// metadata fields persisted on domain.Task (drive list_tasks --template
// queries and the constitution alias rule).
//
// Both `json:` and `yaml:` tags are required. The MCP tool task_plan
// returns JSON; the CLI `task-template plan` subcommand prints YAML.
// yaml.v3 falls back to lowercased-no-separator field names when no
// tag is present, so missing tags emit `relevantfiles` instead of the
// snake_case the loader's input YAML uses (caught by claude-code review
// on Phases 1-3, file:line drift verified).
type PlannedAspect struct {
	Template      string   `json:"template" yaml:"template"`
	Aspect        string   `json:"aspect" yaml:"aspect"`
	Title         string   `json:"title" yaml:"title"`
	Description   string   `json:"description" yaml:"description"`
	RelevantFiles []string `json:"relevant_files" yaml:"relevant_files"`
	Constraints   []string `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	FindingFormat string   `json:"finding_format" yaml:"finding_format"`
	SpawnedBy     []string `json:"spawned_by" yaml:"spawned_by"`
}

// Plan is the result of Plan(inputs, template). Tags is the union of
// classifier tags fired against inputs (returned for transparency so
// callers can log "we classified files as [TAULU PROTO]" without
// re-deriving it). Aspects is the ordered list of PlannedAspects in the
// order the driver should spawn them — defaults first, then routing-rule
// declaration order across sources.
type Plan struct {
	Template string          `json:"template" yaml:"template"`
	Tags     []string        `json:"tags" yaml:"tags"`
	Aspects  []PlannedAspect `json:"aspects" yaml:"aspects"`
}

// Source produces the templates this source contributes. Each source
// owns its own discovery (filesystem walk, embedded FS, …) and yields
// raw, unmerged template files; Resolve does the merge.
type Source interface {
	Name() string
	List() ([]TemplateFile, error)
}

// TemplateFile is one source's contribution to a single template id —
// the raw, pre-merge view. Routing / Classifier slices preserve
// declaration order from the source's own files so the merge can
// concatenate them deterministically.
//
// Checklists is keyed by aspect id; each entry carries the file's body
// AND a Replace flag that, when true, instructs the merger to drop any
// previously-accumulated content for that aspect and use this body as
// the sole content (the YAML front-matter `replace: true` escape hatch).
type TemplateFile struct {
	Source     string
	ID         string
	Title      string
	Desc       string
	Inputs     InputDeclaration
	Aspects    []Aspect
	Routing    []RoutingRule
	Classifier []Classifier
	Checklists map[string]ChecklistBody
}

// ChecklistBody is one aspect's checklist as contributed by one source.
// Replace=true means "this body replaces any earlier-source body for
// this aspect", false means "concatenate this body after earlier-source
// bodies" (the default).
type ChecklistBody struct {
	Body    string
	Replace bool
}

// Resolve picks the named template across sources and returns the
// merged Template ready for Plan. Returns a clear error when no source
// declares the requested id so callers can distinguish "no such
// template" from "template loaded but empty".
//
// Merge semantics (mirrors the override-precedence table in the
// docs/TASK_TEMPLATES.md authoring guide):
//
//   - Aspects: replace-by-id, earlier-source-wins. The first source to
//     declare an aspect id holds the slot; later sources are ignored
//     for that id. New ids from later sources are appended.
//   - Routing / Classifiers: append, slice order preserved across
//     sources (defaults first, then sources in declaration order).
//   - Checklists: per aspect, concatenate in source declaration order
//     unless a source's body has Replace=true, in which case the
//     accumulated content is dropped and only the replacing body is kept
//     (later sources still concatenate after a Replace).
//   - Title / Description / Inputs.Required / Inputs.Declarations:
//     first source declaring a non-empty value wins; later sources can
//     fill in fields the earlier source left unset (nil slice / nil
//     map / "" string) but cannot overwrite a value already present.
//     Same fill-in rule applies uniformly to all four fields. Teams
//     that ship an `inputs:` block AFTER defaults therefore cannot
//     relax `required` if the defaults already declared it; they need
//     to fork the template id (one source, one definition) or convince
//     upstream defaults to drop the key.
//
// Duplicate ids within a SINGLE source are surfaced as an error from
// the source (DirSource.List, EmbedSource.List); Resolve trusts that
// each TemplateFile.Aspects / .Routing / .Classifier is internally
// duplicate-free.
func Resolve(id string, sources []Source) (Template, error) {
	if id == "" {
		return Template{}, fmt.Errorf("template id is required")
	}

	var (
		matched     []TemplateFile
		errs        []error
		firstSource string
	)
	for _, src := range sources {
		if src == nil {
			continue
		}
		files, err := src.List()
		if err != nil {
			errs = append(errs, fmt.Errorf("source %q: %w", src.Name(), err))
			continue
		}
		for _, f := range files {
			if f.ID != id {
				continue
			}
			if firstSource == "" {
				firstSource = f.Source
			}
			matched = append(matched, f)
		}
	}
	if len(matched) == 0 {
		if len(errs) > 0 {
			return Template{}, fmt.Errorf("template %q not found (and %d source(s) errored: %v)", id, len(errs), errs)
		}
		return Template{}, fmt.Errorf("template %q not found", id)
	}

	merged := Template{
		ID:         id,
		Source:     firstSource,
		Checklists: make(map[string]string),
	}

	aspectIdx := map[string]int{}
	for _, f := range matched {
		// Title / description / inputs: first source wins outright.
		if merged.Title == "" {
			merged.Title = f.Title
		}
		if merged.Description == "" {
			merged.Description = f.Desc
		}
		if merged.Inputs.Required == nil && len(f.Inputs.Required) > 0 {
			merged.Inputs.Required = append([]string(nil), f.Inputs.Required...)
		}
		if merged.Inputs.Declarations == nil && len(f.Inputs.Declarations) > 0 {
			merged.Inputs.Declarations = make(map[string]string, len(f.Inputs.Declarations))
			for k, v := range f.Inputs.Declarations {
				merged.Inputs.Declarations[k] = v
			}
		}

		// Aspects: replace-by-id, earlier-source-wins.
		for _, a := range f.Aspects {
			if _, dup := aspectIdx[a.ID]; dup {
				continue
			}
			aspectIdx[a.ID] = len(merged.Aspects)
			a.Source = f.Source
			merged.Aspects = append(merged.Aspects, a)
		}

		// Routing / Classifiers: append, preserve slice order.
		for _, r := range f.Routing {
			r.Source = f.Source
			merged.Routing = append(merged.Routing, r)
		}
		for _, c := range f.Classifier {
			c.Source = f.Source
			merged.Classifiers = append(merged.Classifiers, c)
		}

		// Checklists: per aspect, concatenate or replace.
		for aspect, body := range f.Checklists {
			if body.Replace {
				merged.Checklists[aspect] = strings.TrimRight(body.Body, "\n")
				continue
			}
			existing := merged.Checklists[aspect]
			if existing == "" {
				merged.Checklists[aspect] = strings.TrimRight(body.Body, "\n")
			} else {
				merged.Checklists[aspect] = existing + "\n\n" + strings.TrimRight(body.Body, "\n")
			}
		}
	}

	return merged, nil
}

// SortAspectsByID is exposed for callers (CLI doctor / show) that want
// alphabetical aspect output regardless of source order. Plan does NOT
// re-sort — spawn order follows routing-rule declaration order.
func SortAspectsByID(aspects []Aspect) []Aspect {
	out := append([]Aspect(nil), aspects...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
