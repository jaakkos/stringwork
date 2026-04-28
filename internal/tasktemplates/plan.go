package tasktemplates

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Classify returns the union of tags fired by classifiers against the
// "files" input. Disabled classifiers are skipped. Tags are returned in
// classifier declaration order with duplicates dropped (defaults first,
// then sources in declaration order — same as the merged Classifier
// slice itself).
//
// Pattern matching uses filepath.Match semantics:
//
//   - "*" matches any sequence of non-separator characters.
//   - "**" is NOT supported by filepath.Match, but we accept it as
//     "match anywhere": when a pattern contains "**", the engine
//     strips the "**/" prefix / the "/**" suffix and matches the
//     remainder against any path component. This covers the common
//     cases ("**/migration/**", "**/foo.go") without reaching for
//     a third-party glob library; it is documented in the authoring
//     guide as "shell-glob style with one extension".
//
// A pattern that fails to compile (filepath.Match returns ErrBadPattern)
// produces no matches — the rule is a no-op rather than a hard error
// so one bad classifier doesn't prevent the rest from firing. Doctor
// surfaces bad patterns at validation time.
func Classify(files []string, classifiers []Classifier) []string {
	if len(files) == 0 || len(classifiers) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var tags []string
	for _, c := range classifiers {
		if c.Disabled || c.Pattern == "" || c.Tag == "" {
			continue
		}
		for _, f := range files {
			if !matchPattern(c.Pattern, f) {
				continue
			}
			tag := strings.TrimSpace(c.Tag)
			if tag == "" {
				continue
			}
			if _, dup := seen[tag]; dup {
				continue
			}
			seen[tag] = struct{}{}
			tags = append(tags, tag)
			break
		}
	}
	return tags
}

// BuildPlan applies the template's routing rules to the inputs and
// produces the ordered list of PlannedAspects. The driver passes each
// PlannedAspect to create_task as-is.
//
// Named "BuildPlan" rather than "Plan" because the result type is also
// named Plan — Go disallows colliding identifiers in a single package
// and the type name carries more semantic weight (every caller types
// `Plan{}` literals or `*Plan` returns; the function is referenced
// once per call site).
//
// Steps:
//
//  1. Validate inputs against the template's InputDeclaration.
//  2. Classify the "files" input via the template's Classifiers to
//     produce the firing tag set.
//  3. Walk Routing in declaration order. Each rule that fires
//     contributes one slot for its target aspect; duplicates collapse
//     (the second rule firing the same aspect appends its ExtraFocus
//     to the first PlannedAspect's description and records its id in
//     SpawnedBy). Order is preserved by first-fire.
//  4. For each spawned aspect, compose the description:
//     background → aspect description → checklist → rule extras → finding format.
//
// Rules that reference a Spawn id with no matching Aspect are skipped
// (and surfaced by doctor). Disabled rules are skipped silently.
func BuildPlan(inputs map[string]any, template Template) (Plan, error) {
	if template.ID == "" {
		return Plan{}, fmt.Errorf("template is empty")
	}

	if err := ValidateInputs(inputs, template.Inputs); err != nil {
		return Plan{}, err
	}

	files := CoerceStringList(inputs["files"])
	tags := Classify(files, template.Classifiers)

	aspectByID := make(map[string]Aspect, len(template.Aspects))
	for _, a := range template.Aspects {
		aspectByID[a.ID] = a
	}

	tagSet := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		tagSet[t] = struct{}{}
	}

	summary := stringFromAny(inputs["summary"])
	background := composeBackground(template, files, tags, summary)
	findingFormat := defaultFindingFormat()

	type planSlot struct {
		aspect       Aspect
		spawnedBy    []string
		extraFocuses []string
	}
	slotByAspect := map[string]*planSlot{}
	var order []string

	for _, rule := range template.Routing {
		if rule.Disabled {
			continue
		}
		if !ruleFires(rule, tagSet) {
			continue
		}
		aspect, ok := aspectByID[rule.Spawn]
		if !ok {
			// Orphan reference — doctor surfaces; runtime silently skips.
			continue
		}
		slot, ok := slotByAspect[rule.Spawn]
		if !ok {
			slot = &planSlot{aspect: aspect}
			slotByAspect[rule.Spawn] = slot
			order = append(order, rule.Spawn)
		}
		slot.spawnedBy = append(slot.spawnedBy, rule.ID)
		if focus := strings.TrimSpace(rule.ExtraFocus); focus != "" {
			slot.extraFocuses = append(slot.extraFocuses, focus)
		}
	}

	planned := make([]PlannedAspect, 0, len(order))
	for _, aspectID := range order {
		slot := slotByAspect[aspectID]
		desc := composeDescription(
			template,
			slot.aspect,
			background,
			slot.extraFocuses,
			findingFormat,
		)
		planned = append(planned, PlannedAspect{
			Template:      template.ID,
			Aspect:        slot.aspect.ID,
			Title:         slot.aspect.Title,
			Description:   desc,
			RelevantFiles: append([]string(nil), files...),
			Constraints:   nil,
			FindingFormat: findingFormat,
			SpawnedBy:     append([]string(nil), slot.spawnedBy...),
		})
	}

	return Plan{
		Template: template.ID,
		Tags:     tags,
		Aspects:  planned,
	}, nil
}

// ruleFires reports whether rule applies given the firing tag set.
//
//   - When=="always" → always fires (regardless of tags).
//   - WhenTags non-empty → OR semantics: any tag in WhenTags present
//     in the firing set triggers the rule.
//   - Both empty → rule never fires (probably a config bug; doctor
//     surfaces). We don't auto-firing here because "no condition" is
//     an ambiguous intent — explicit `when: always` opts in.
func ruleFires(rule RoutingRule, fired map[string]struct{}) bool {
	if strings.EqualFold(strings.TrimSpace(rule.When), "always") {
		return true
	}
	for _, t := range rule.WhenTags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := fired[t]; ok {
			return true
		}
	}
	return false
}

// composeBackground renders the shared header that appears at the top
// of every PlannedAspect.Description in this Plan. Includes the
// template title, a one-line summary if provided, the firing tag set,
// and the file list (truncated to 20 entries with a "(+N more)" note
// to keep the spawn prompt bounded).
func composeBackground(template Template, files, tags []string, summary string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", template.Title)
	if strings.TrimSpace(summary) != "" {
		fmt.Fprintf(&b, "**Summary**: %s\n\n", strings.TrimSpace(summary))
	}
	if len(tags) > 0 {
		sortedTags := append([]string(nil), tags...)
		sort.Strings(sortedTags)
		fmt.Fprintf(&b, "**Detected tags**: %s\n\n", strings.Join(sortedTags, ", "))
	}
	if len(files) > 0 {
		b.WriteString("**Files in scope**:\n")
		const maxFiles = 20
		for i, f := range files {
			if i >= maxFiles {
				fmt.Fprintf(&b, "- (+%d more)\n", len(files)-maxFiles)
				break
			}
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// composeDescription assembles the worker-facing description for one
// aspect: shared background, aspect description, composed checklist,
// any rule-specific extra focus blocks, then the finding format. The
// 4KB-per-aspect soft cap is enforced by doctor — Plan never silently
// truncates because the truncation point would surprise the worker.
func composeDescription(template Template, aspect Aspect, background string, extraFocuses []string, findingFormat string) string {
	var b strings.Builder
	b.WriteString(background)
	fmt.Fprintf(&b, "### Aspect: %s\n\n", aspect.Title)
	if strings.TrimSpace(aspect.Description) != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(aspect.Description))
	}
	if checklist := strings.TrimSpace(template.Checklists[aspect.ID]); checklist != "" {
		fmt.Fprintf(&b, "%s\n\n", checklist)
	}
	for _, extra := range extraFocuses {
		fmt.Fprintf(&b, "### Additional focus\n\n%s\n\n", extra)
	}
	b.WriteString(findingFormat)
	return b.String()
}

// defaultFindingFormat is the standard worker output format. Templates
// can override this in a future iteration; v1 hard-codes it because all
// shipped aspects expect the same shape.
func defaultFindingFormat() string {
	return `### Finding format

Use this exact shape for every issue you raise:

` + "```" + `
### [SEVERITY] Title (file:line)
- What: description of the issue
- Why: why it matters
- Fix: suggested fix or code snippet

Severity: MUST_FIX | SHOULD_FIX | NIT | QUESTION
` + "```" + `
`
}

// matchPattern is the glob engine: classic shell globs via
// filepath.Match, plus "**" interpreted as "match any sequence of path
// segments". Three concrete shapes are supported (the patterns we
// found teams actually write today; richer doublestar can be added if
// real overlays demand it):
//
//	"X/**"     — match any path that starts with X/.
//	"**/X"     — match any path whose basename or trailing sub-path is X.
//	"**/X/**"  — match any path that has X as a path component
//	             OR contains X as a sub-path.
//
// Plain shell globs without "**" probe both the file basename AND the
// full path, because "*.go" should fire on "foo.go" and on
// "internal/foo.go" — filepath.Match's "*" stops at the path
// separator otherwise.
//
// Patterns that fail to compile (filepath.ErrBadPattern) silently
// produce no matches at runtime. Doctor catches two failure modes
// at validation time:
//
//   - filepath.Match's own bad-pattern errors (rare; "[" without "]").
//   - Middle-"**" constructions like "a/**/b" that this engine does
//     NOT support — filepath.Match accepts them silently (treats "**"
//     as two adjacent "*"s with no error), so doctor has to detect
//     them by string inspection. See doctorTemplate's classifier loop.
func matchPattern(pattern, path string) bool {
	if pattern == "" || path == "" {
		return false
	}

	if !strings.Contains(pattern, "**") {
		base := filepath.Base(path)
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, path); ok {
			return true
		}
		return false
	}

	hasLeading := strings.HasPrefix(pattern, "**/")
	hasTrailing := strings.HasSuffix(pattern, "/**")
	core := pattern
	if hasLeading {
		core = strings.TrimPrefix(core, "**/")
	}
	if hasTrailing {
		core = strings.TrimSuffix(core, "/**")
	}
	// A "**" left in the middle (e.g. "a/**/b") falls back to "no match"
	// rather than guessing — keeps the engine predictable. Doctor
	// surfaces middle-"**" patterns explicitly via string inspection
	// (filepath.Match treats "**" as two adjacent "*"s and does NOT
	// return ErrBadPattern, so we cannot rely on the loader's pattern
	// check to catch them).
	if strings.Contains(core, "**") {
		return false
	}

	switch {
	case hasLeading && hasTrailing:
		// "**/X/**" → X appears as a path component or sub-path.
		for _, seg := range strings.Split(path, "/") {
			if ok, _ := filepath.Match(core, seg); ok {
				return true
			}
		}
		return strings.Contains("/"+path+"/", "/"+core+"/")
	case hasLeading:
		// "**/X" → basename match or trailing sub-path.
		if ok, _ := filepath.Match(core, filepath.Base(path)); ok {
			return true
		}
		return strings.HasSuffix(path, "/"+core) || path == core
	case hasTrailing:
		// "X/**" → path starts with X/ (or equals X exactly, for the
		// degenerate "X is a file too" case).
		return strings.HasPrefix(path, core+"/") || path == core
	}
	return false
}

// stringFromAny returns v as a string when it is a string, "" otherwise.
// Used for optional inputs ("summary") that drop into the background
// block but never need to be type-checked because they're free text.
func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
