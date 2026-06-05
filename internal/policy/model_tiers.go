package policy

import (
	"fmt"
	"sort"
	"strings"
)

// SupportedWorkerTypes are the built-in spawnable worker CLI types that
// model_tiers keys should use (must match orchestration.workers[].type).
var SupportedWorkerTypes = []string{"claude-code", "codex", "gemini"}

// ExampleModelTiers is a reference mapping for all built-in providers. Copy
// into orchestration.model_tiers in live config and adjust model names to
// match what each CLI accepts.
var ExampleModelTiers = map[string]map[string]string{
	"fast": {
		"claude-code": "haiku",
		"codex":       "o4-mini",
		"gemini":      "gemini-2.5-flash",
	},
	"standard": {
		"claude-code": "sonnet",
		"codex":       "gpt-5-codex",
		"gemini":      "gemini-2.5-pro",
	},
	"capable": {
		"claude-code": "opus",
		"codex":       "gpt-5-codex",
		"gemini":      "gemini-2.5-pro",
	},
}

// ConfiguredWorkerTypes returns unique orchestration.workers[].type values.
func (o *OrchestrationConfig) ConfiguredWorkerTypes() []string {
	if o == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, w := range o.Workers {
		if w.Type == "" {
			continue
		}
		if _, dup := seen[w.Type]; dup {
			continue
		}
		seen[w.Type] = struct{}{}
		out = append(out, w.Type)
	}
	sort.Strings(out)
	return out
}

// ModelTierCoverageWarnings reports configured worker types missing from
// each model_tiers entry. Empty when model_tiers is unset.
func (o *OrchestrationConfig) ModelTierCoverageWarnings() []string {
	if o == nil || len(o.ModelTiers) == 0 {
		return nil
	}
	workers := o.ConfiguredWorkerTypes()
	if len(workers) == 0 {
		return nil
	}
	tierNames := make([]string, 0, len(o.ModelTiers))
	for name := range o.ModelTiers {
		tierNames = append(tierNames, name)
	}
	sort.Strings(tierNames)

	var warnings []string
	for _, tier := range tierNames {
		mapping := o.ModelTiers[tier]
		if mapping == nil {
			mapping = map[string]string{}
		}
		var missing []string
		for _, wt := range workers {
			if strings.TrimSpace(mapping[wt]) == "" {
				missing = append(missing, wt)
			}
		}
		if len(missing) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"model_tiers.%s has no model for worker type(s): %s",
				tier, strings.Join(missing, ", "),
			))
		}
	}
	return warnings
}

// ResolveModelTier looks up a concrete CLI model for workerType in tiers.
// Returns fallback when tier or worker type is missing.
func ResolveModelTier(tiers map[string]map[string]string, tier, workerType, fallback string) string {
	if tier == "" || tiers == nil {
		return fallback
	}
	mapping, ok := tiers[tier]
	if !ok || mapping == nil {
		return fallback
	}
	if m := strings.TrimSpace(mapping[workerType]); m != "" {
		return m
	}
	return fallback
}

// ModelTierSelectionHints describes when the driver should pick each tier.
var ModelTierSelectionHints = map[string]string{
	"fast":     "Docs, style, simple reads, typos, grep-only investigation",
	"standard": "Default implementation, most review aspects, clear-scope fixes",
	"capable":  "Security, auth, PII, migrations, large diffs (>5 files), unclear root cause",
}

// WorkerModelDefaults returns workers[].model fallbacks keyed by worker type.
func (o *OrchestrationConfig) WorkerModelDefaults() map[string]string {
	if o == nil {
		return nil
	}
	out := map[string]string{}
	for _, w := range o.Workers {
		if w.Type != "" && strings.TrimSpace(w.Model) != "" {
			out[w.Type] = strings.TrimSpace(w.Model)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EffectiveModelTierMap returns configured model_tiers, or nil when unset.
func (o *OrchestrationConfig) EffectiveModelTierMap() map[string]map[string]string {
	if o == nil || len(o.ModelTiers) == 0 {
		return nil
	}
	return o.ModelTiers
}

// FormatModelSelectionGuide renders available models and driver guidance for AI
// agents. Uses live orchestration.model_tiers when configured; always lists
// worker default models from workers[].model.
func FormatModelSelectionGuide(orch *OrchestrationConfig) string {
	if orch == nil {
		return ""
	}
	workers := orch.ConfiguredWorkerTypes()
	if len(workers) == 0 {
		workers = append([]string(nil), SupportedWorkerTypes...)
	}
	defaults := orch.WorkerModelDefaults()
	tiers := orch.EffectiveModelTierMap()

	var b strings.Builder
	b.WriteString("Worker model selection (driver sets model_tier or model on create_task):\n\n")

	b.WriteString("Tier guidance:\n")
	tierOrder := []string{"fast", "standard", "capable"}
	seenTiers := map[string]struct{}{}
	for _, tier := range tierOrder {
		if hint, ok := ModelTierSelectionHints[tier]; ok {
			fmt.Fprintf(&b, "  %s — %s\n", tier, hint)
			seenTiers[tier] = struct{}{}
		}
	}
	if tiers != nil {
		for name := range tiers {
			if _, dup := seenTiers[name]; dup {
				continue
			}
			if hint, ok := ModelTierSelectionHints[name]; ok {
				fmt.Fprintf(&b, "  %s — %s\n", name, hint)
			} else {
				fmt.Fprintf(&b, "  %s — (custom tier)\n", name)
			}
		}
	}
	b.WriteByte('\n')

	if tiers != nil {
		tierNames := make([]string, 0, len(tiers))
		for name := range tiers {
			tierNames = append(tierNames, name)
		}
		sort.Strings(tierNames)
		b.WriteString("Configured model_tiers (tier → worker → CLI model):\n")
		for _, tier := range tierNames {
			mapping := tiers[tier]
			parts := make([]string, 0, len(workers))
			for _, wt := range workers {
				model := strings.TrimSpace(mapping[wt])
				if model == "" {
					model = "(unset — falls back to worker default)"
				}
				parts = append(parts, fmt.Sprintf("%s=%s", wt, model))
			}
			fmt.Fprintf(&b, "  %s: %s\n", tier, strings.Join(parts, ", "))
		}
		b.WriteByte('\n')
	} else {
		b.WriteString("model_tiers: not configured — set orchestration.model_tiers in config.yaml ")
		b.WriteString("to enable tier-based selection. Until then, use workers[].model defaults ")
		b.WriteString("or task.model for an explicit override.\n\n")
		b.WriteString("Example tier map (all providers):\n")
		for _, tier := range tierOrder {
			mapping := ExampleModelTiers[tier]
			parts := make([]string, 0, len(SupportedWorkerTypes))
			for _, wt := range SupportedWorkerTypes {
				parts = append(parts, fmt.Sprintf("%s=%s", wt, mapping[wt]))
			}
			fmt.Fprintf(&b, "  %s: %s\n", tier, strings.Join(parts, ", "))
		}
		b.WriteByte('\n')
	}

	if len(defaults) > 0 {
		b.WriteString("Worker default models (when model_tier/model omitted):\n")
		for _, wt := range workers {
			if m, ok := defaults[wt]; ok {
				fmt.Fprintf(&b, "  %s: %s\n", wt, m)
			}
		}
		b.WriteByte('\n')
	}

	b.WriteString("create_task params:\n")
	b.WriteString("  model_tier — fast | standard | capable (recommended; resolves per assigned worker)\n")
	b.WriteString("  model — explicit CLI model for the assigned worker (overrides model_tier)\n")
	b.WriteString("  assigned_to — claude-code | codex | gemini | any\n")
	b.WriteString("Resolution at spawn: task.model → model_tiers[tier][workerType] → workers[].model\n")

	return strings.TrimRight(b.String(), "\n")
}
