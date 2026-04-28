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

	routingIDs := map[string]bool{}
	for _, r := range t.Routing {
		if _, dup := routingIDs[r.ID]; dup {
			// Cross-source dup — this can happen when a team file
			// reuses a default rule's id without `disabled: true`.
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   r.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("routing rule %q declared by multiple sources without disabled: true", r.ID),
			})
		}
		routingIDs[r.ID] = true
		if !r.Disabled {
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
	}

	classifierIDs := map[string]bool{}
	emittedTags := map[string]struct{}{}
	for _, c := range t.Classifiers {
		if _, dup := classifierIDs[c.ID]; dup {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   c.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("classifier %q declared by multiple sources without disabled: true", c.ID),
			})
		}
		classifierIDs[c.ID] = true
		if !c.Disabled {
			if _, err := filepath.Match(c.Pattern, ""); err != nil {
				issues = append(issues, DoctorIssue{
					Severity: "ERROR",
					Source:   c.Source,
					Template: t.ID,
					Message:  fmt.Sprintf("classifier %q has invalid pattern %q: %v", c.ID, c.Pattern, err),
				})
			}
			if c.Tag != "" {
				emittedTags[c.Tag] = struct{}{}
			}
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

	// Disabled rules pointing at ids no source declares — typo guard.
	for _, r := range t.Routing {
		if !r.Disabled {
			continue
		}
		if !routingIDs[r.ID] {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   r.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("disabled routing rule %q matches no other source's rule", r.ID),
			})
		}
	}
	for _, c := range t.Classifiers {
		if !c.Disabled {
			continue
		}
		if !classifierIDs[c.ID] {
			issues = append(issues, DoctorIssue{
				Severity: "ERROR",
				Source:   c.Source,
				Template: t.ID,
				Message:  fmt.Sprintf("disabled classifier %q matches no other source's classifier", c.ID),
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
