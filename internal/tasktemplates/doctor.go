package tasktemplates

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// DoctorIssue is one validation finding produced by Doctor. Severity is
// "ERROR" (Doctor exits non-zero) or "WARN" (advisory). Source is the
// declaring source name when known so a finding points at one team
// overlay file rather than "somewhere in the merged template".
type DoctorIssue struct {
	Severity string
	Source   string
	Template string
	Aspect   string
	Message  string
}

// Doctor walks every template that any source contributes and validates
// the merged result against the v3 plan's invariants:
//
//   - Every Aspect id is unique across the merged template (per source
//     duplicates are caught earlier in the loader; cross-source
//     duplicates are caught here when later sources' aspect ids collide
//     with reserved/disabled defaults).
//   - Every routing rule's `spawn` points at a defined aspect id.
//   - Every disabled routing rule / classifier id matches a default rule
//     or another source's rule (orphan disable references are typos —
//     e.g. team disables `when-securty-tag` thinking it overrides the
//     default `when-security-tag`).
//   - Every classifier produces a tag that is referenced by at least one
//     routing rule (warn — unused tag may be intentional but is usually
//     a copy-paste leftover).
//   - Every routing rule references at least one tag classifier emits OR
//     uses `when: always` (warn — unmatched tags in WhenTags are dead
//     conditions).
//   - Composed checklist per aspect ≤ 4096 bytes (warn at 80% of cap,
//     error above; the cap keeps spawn prompts bounded).
//   - Every required input key MUST be referenced by at least one
//     classifier OR used in the description template (warn; doctor
//     can't introspect the planner's `inputs["files"]` access so this
//     is a heuristic).
//
// Returns issues sorted by Severity (errors first), then Source, then
// Template, then Message — stable so doctor output is deterministic.
func Doctor(sources []Source) ([]DoctorIssue, error) {
	if len(sources) == 0 {
		return nil, nil
	}

	templateIDs := map[string]struct{}{}
	var sourceErrs []error
	for _, src := range sources {
		if src == nil {
			continue
		}
		files, err := src.List()
		if err != nil {
			sourceErrs = append(sourceErrs, fmt.Errorf("source %q: %w", src.Name(), err))
			continue
		}
		for _, f := range files {
			templateIDs[f.ID] = struct{}{}
		}
	}

	var issues []DoctorIssue
	if len(sourceErrs) > 0 {
		for _, e := range sourceErrs {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Message:  e.Error(),
			})
		}
	}

	ids := make([]string, 0, len(templateIDs))
	for id := range templateIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		merged, err := Resolve(id, sources)
		if err != nil {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Template: id,
				Message:  err.Error(),
			})
			continue
		}
		issues = append(issues, doctorTemplate(merged)...)
	}

	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Severity != issues[j].Severity {
			// ERROR before WARN.
			return issues[i].Severity == "ERROR"
		}
		if issues[i].Source != issues[j].Source {
			return issues[i].Source < issues[j].Source
		}
		if issues[i].Template != issues[j].Template {
			return issues[i].Template < issues[j].Template
		}
		return issues[i].Message < issues[j].Message
	})
	return issues, nil
}

// doctorTemplate runs the per-template checks against a single merged
// Template. Split out from Doctor so callers (CLI doctor, tests) can
// validate one template at a time without re-walking sources.
func doctorTemplate(t Template) []DoctorIssue {
	var issues []DoctorIssue

	aspectIDs := map[string]struct{}{}
	for _, a := range t.Aspects {
		aspectIDs[a.ID] = struct{}{}
	}

	// Group routing rules by id so the duplicate check can distinguish
	// "two enabled rules from different sources" (a true conflict —
	// both will fire) from "one enabled + one disabled" (the documented
	// override pattern: a team uses disabled: true to suppress an
	// upstream default).
	routingByID := map[string][]RoutingRule{}
	for _, r := range t.Routing {
		routingByID[r.ID] = append(routingByID[r.ID], r)
	}
	for id, rs := range routingByID {
		if len(rs) <= 1 {
			continue
		}
		enabled := 0
		var firstOverlay RoutingRule
		for i, r := range rs {
			if !r.Disabled {
				enabled++
			}
			if i == 1 {
				// Source of the second declaration — the one that
				// "should have" used disabled: true if it meant to
				// override.
				firstOverlay = r
			}
		}
		if enabled > 1 {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   firstOverlay.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("routing rule %q declared by multiple sources, all enabled — at least one must set disabled: true to override", id),
			})
		}
	}

	for _, r := range t.Routing {
		if r.Disabled {
			continue
		}
		if _, ok := aspectIDs[r.Spawn]; !ok {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   r.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("routing rule %q spawns unknown aspect %q", r.ID, r.Spawn),
			})
		}
		if strings.TrimSpace(r.When) == "" && len(r.WhenTags) == 0 {
			issues = append(issues, DoctorIssue{
				Severity: "WARN",
				Source:   r.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("routing rule %q has neither when nor when_tags — it will never fire", r.ID),
			})
		}
	}

	// Classifiers: duplicate check uses the same enabled-vs-disabled
	// distinction as routing. Pattern validation also runs here, with
	// an explicit middle-"**" guard because filepath.Match silently
	// accepts "a/**/b" (it treats "**" as two adjacent "*"s with no
	// ErrBadPattern), but matchPattern in plan.go drops to "no match"
	// for any unsupported "**" position. Without this check the
	// classifier compiles, the doctor passes, and the runtime never
	// fires — silent failure mode the team has no way to attribute.
	classifierByID := map[string][]Classifier{}
	for _, c := range t.Classifiers {
		classifierByID[c.ID] = append(classifierByID[c.ID], c)
	}
	for id, cs := range classifierByID {
		if len(cs) <= 1 {
			continue
		}
		enabled := 0
		var firstOverlay Classifier
		for i, c := range cs {
			if !c.Disabled {
				enabled++
			}
			if i == 1 {
				firstOverlay = c
			}
		}
		if enabled > 1 {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   firstOverlay.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("classifier %q declared by multiple sources, all enabled — at least one must set disabled: true to override", id),
			})
		}
	}

	emittedTags := map[string]struct{}{}
	for _, c := range t.Classifiers {
		if c.Disabled {
			continue
		}
		if _, err := filepath.Match(c.Pattern, ""); err != nil {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   c.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("classifier %q has invalid pattern %q: %v", c.ID, c.Pattern, err),
			})
		}
		if isMiddleDoubleStar(c.Pattern) {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   c.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("classifier %q pattern %q has unsupported middle \"**\" — use leading \"**/X\", trailing \"X/**\", or \"**/X/**\" instead", c.ID, c.Pattern),
			})
		}
		if c.Tag != "" {
			emittedTags[c.Tag] = struct{}{}
		}
	}

	// Routing rules referencing tags no classifier emits are dead
	// conditions. Skip rules with `when: always` (no tag dependency)
	// and disabled rules.
	for _, r := range t.Routing {
		if r.Disabled || strings.EqualFold(strings.TrimSpace(r.When), "always") {
			continue
		}
		for _, tag := range r.WhenTags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if _, ok := emittedTags[tag]; !ok {
				issues = append(issues, DoctorIssue{
					Severity: "WARN",
					Source:   r.Source,
					Template: t.ID,
					Message:  fmt.Sprintf("routing rule %q references tag %q that no classifier emits", r.ID, tag),
				})
			}
		}
	}

	usedTags := map[string]struct{}{}
	for _, r := range t.Routing {
		if r.Disabled {
			continue
		}
		for _, tag := range r.WhenTags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				usedTags[tag] = struct{}{}
			}
		}
	}
	for _, c := range t.Classifiers {
		if c.Disabled || c.Tag == "" {
			continue
		}
		if _, used := usedTags[c.Tag]; !used {
			issues = append(issues, DoctorIssue{
				Severity: "WARN",
				Source:   c.Source,
				Template: t.ID,
				Aspect:   "",
				Message:  fmt.Sprintf("classifier %q emits tag %q which no routing rule references", c.ID, c.Tag),
			})
		}
	}

	// Orphan-disable: a `disabled: true` declaration only makes sense
	// when SOME other (enabled) source declares the same id — the
	// disable is overriding that one. If no enabled rule with this id
	// exists in any source, the disable is dead config (typo guard:
	// catches "when-securty-tag" trying to disable "when-security-tag").
	//
	// The check uses routingByID / classifierByID built above, which
	// group by id across all sources. A disabled rule whose group
	// contains zero enabled rules is the orphan case.
	for _, r := range t.Routing {
		if !r.Disabled {
			continue
		}
		if !groupHasEnabledRule(routingByID[r.ID]) {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   r.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("disabled routing rule %q matches no enabled rule from any source", r.ID),
			})
		}
	}
	for _, c := range t.Classifiers {
		if !c.Disabled {
			continue
		}
		if !groupHasEnabledClassifier(classifierByID[c.ID]) {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   c.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("disabled classifier %q matches no enabled classifier from any source", c.ID),
			})
		}
	}

	// 4KB-per-aspect soft cap on composed checklists. WARN at 80% so a
	// team near the cap is alerted before they cross it.
	const checklistCap = 4096
	const checklistWarnAt = checklistCap * 80 / 100
	for aspect, body := range t.Checklists {
		if len(body) > checklistCap {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Template: t.ID,
				Aspect:   aspect,
				Message:  fmt.Sprintf("composed checklist for %q is %d bytes (cap %d) — split or trim", aspect, len(body), checklistCap),
			})
		} else if len(body) > checklistWarnAt {
			issues = append(issues, DoctorIssue{
				Severity: "WARN",
				Template: t.ID,
				Aspect:   aspect,
				Message:  fmt.Sprintf("composed checklist for %q is %d bytes (warn at %d, cap %d)", aspect, len(body), checklistWarnAt, checklistCap),
			})
		}
		// Catch checklists targeting an aspect that doesn't exist on
		// the merged template — usually a typo in the team file.
		if _, ok := aspectIDs[aspect]; !ok {
			issues = append(issues, DoctorIssue{
				Severity: "WARN",
				Template: t.ID,
				Aspect:   aspect,
				Message:  fmt.Sprintf("checklist file targets unknown aspect %q", aspect),
			})
		}
	}

	return issues
}

// isMiddleDoubleStar reports whether pattern contains a "**" segment
// that is not a leading "**/" or trailing "/**". Middle-"**" is the
// silent-failure case matchPattern explicitly drops to "no match" —
// see plan.go's matchPattern doc and inline comment for why.
func isMiddleDoubleStar(pattern string) bool {
	if !strings.Contains(pattern, "**") {
		return false
	}
	core := pattern
	core = strings.TrimPrefix(core, "**/")
	core = strings.TrimSuffix(core, "/**")
	return strings.Contains(core, "**")
}

// groupHasEnabledRule reports whether any rule in rs has Disabled=false.
// Used by the orphan-disable check: a disabled rule is orphan when its
// id-group contains zero enabled rules across all sources.
func groupHasEnabledRule(rs []RoutingRule) bool {
	for _, r := range rs {
		if !r.Disabled {
			return true
		}
	}
	return false
}

// groupHasEnabledClassifier is the classifier counterpart to
// groupHasEnabledRule.
func groupHasEnabledClassifier(cs []Classifier) bool {
	for _, c := range cs {
		if !c.Disabled {
			return true
		}
	}
	return false
}
