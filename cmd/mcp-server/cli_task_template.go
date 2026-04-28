// Task-template CLI subcommands. Like the constitution subcommands,
// these do NOT talk to the daemon: they resolve sources from the loaded
// policy directly so users can inspect / debug overlays before any
// worker is spawned. The MCP tool task_plan covers the runtime path;
// these commands cover authoring and diagnostics.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaakkos/stringwork/internal/policy"
	"github.com/jaakkos/stringwork/internal/tasktemplates"
)

// runTaskTemplateCommand dispatches `mcp-stringwork task-template <subcommand>`.
// Exits the process on terminal errors so the caller (main) can return early.
func runTaskTemplateCommand(args []string) {
	if len(args) == 0 {
		printTaskTemplateUsage(os.Stderr)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runTaskTemplateList(args[1:])
	case "show":
		runTaskTemplateShow(args[1:])
	case "plan":
		runTaskTemplatePlan(args[1:])
	case "doctor":
		runTaskTemplateDoctor(args[1:])
	case "-h", "--help", "help":
		printTaskTemplateUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown task-template subcommand: %s\n\n", args[0])
		printTaskTemplateUsage(os.Stderr)
		os.Exit(1)
	}
}

func printTaskTemplateUsage(w io.Writer) {
	fmt.Fprint(w, `usage: mcp-stringwork task-template <subcommand> [flags]

Subcommands:
  list       List every template id contributed by any configured source.
             Useful as a quick sanity check that an overlay loaded.

  show <id>  Print the merged Template after defaults + overlays. Shows
             aspects (with provenance), routing rules, classifiers, and
             a one-line per-aspect checklist preview.

               --inputs PATH      Optional YAML file with the input map.
                                  When provided, runs Plan() against it
                                  and prints the planned aspects so
                                  authors can preview what task_plan
                                  would emit for their own overlay.

  plan       Run Plan() with --template <id> --inputs <yaml-file> and
             print the result. Equivalent to calling the task_plan MCP
             tool but works without a daemon, so authors can drive it
             from a make target.

               --template ID
               --inputs PATH

  doctor     Validate every source. Reports overlay errors (orphan
             disable references, duplicate ids, invalid patterns,
             oversize composed checklists) and warnings (unused tags,
             dead routing conditions). Exits non-zero on any ERROR;
             warnings are advisory.

This subcommand reads task-template sources resolved by the loaded
policy: the built-in defaults baked into the binary, the optional
team profile referenced by 'task_templates.profile', and any
user-declared 'task_templates.sources' entries.

`)
}

// runTaskTemplateList prints every template id across all sources, with
// the contributing source labels in alphabetical order. Templates that
// resolve from multiple sources show a comma-joined source list so the
// merge is visible at a glance.
func runTaskTemplateList(args []string) {
	_ = args
	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)
	srcs := pol.TaskTemplateSources()

	type entry struct {
		id      string
		sources []string
	}
	byID := map[string]*entry{}
	for _, src := range srcs {
		if src == nil {
			continue
		}
		files, err := src.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: source %q: %v\n", src.Name(), err)
			continue
		}
		for _, f := range files {
			e, ok := byID[f.ID]
			if !ok {
				e = &entry{id: f.ID}
				byID[f.ID] = e
			}
			e.sources = append(e.sources, src.Name())
		}
	}

	if len(byID) == 0 {
		fmt.Println("No task templates resolved.")
		return
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Printf("%-20s  %s\n", "TEMPLATE", "SOURCES")
	fmt.Println(strings.Repeat("-", 60))
	for _, id := range ids {
		fmt.Printf("%-20s  %s\n", id, strings.Join(byID[id].sources, ", "))
	}
}

// runTaskTemplateShow prints the merged Template structure for one id.
// When --inputs is provided, also runs BuildPlan() and prints the
// resulting PlannedAspects — useful for "what would task_plan emit
// against my own input?" debug.
func runTaskTemplateShow(args []string) {
	if len(args) == 0 {
		cliDie("usage: mcp-stringwork task-template show <id> [--inputs path]")
	}
	id := strings.TrimSpace(args[0])
	if id == "" {
		cliDie("template id is required")
	}
	inputsPath := flagValue(args, "--inputs")

	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)
	srcs := pol.TaskTemplateSources()

	tpl, err := tasktemplates.Resolve(id, srcs)
	if err != nil {
		cliDie(err.Error())
	}

	fmt.Printf("Template: %s\n", tpl.ID)
	fmt.Printf("Title:    %s\n", tpl.Title)
	fmt.Printf("First source: %s\n", tpl.Source)
	if tpl.Description != "" {
		fmt.Printf("\nDescription:\n%s\n", strings.TrimSpace(tpl.Description))
	}

	fmt.Println("\nInputs:")
	if len(tpl.Inputs.Required) > 0 {
		fmt.Printf("  required: %s\n", strings.Join(tpl.Inputs.Required, ", "))
	}
	if len(tpl.Inputs.Declarations) > 0 {
		keys := make([]string, 0, len(tpl.Inputs.Declarations))
		for k := range tpl.Inputs.Declarations {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %s: %s\n", k, tpl.Inputs.Declarations[k])
		}
	}

	fmt.Println("\nAspects:")
	for _, a := range tpl.Aspects {
		fmt.Printf("  - %-15s  %s  (source: %s)\n", a.ID, a.Title, a.Source)
	}

	fmt.Println("\nClassifiers:")
	if len(tpl.Classifiers) == 0 {
		fmt.Println("  (none)")
	}
	for _, c := range tpl.Classifiers {
		state := "enabled"
		if c.Disabled {
			state = "DISABLED"
		}
		fmt.Printf("  - [%s] %s pattern=%q tag=%s (source: %s)\n",
			state, c.ID, c.Pattern, c.Tag, c.Source)
	}

	fmt.Println("\nRouting:")
	if len(tpl.Routing) == 0 {
		fmt.Println("  (none)")
	}
	for _, r := range tpl.Routing {
		state := "enabled"
		if r.Disabled {
			state = "DISABLED"
		}
		when := r.When
		if when == "" {
			when = "tags=" + strings.Join(r.WhenTags, ",")
		}
		fmt.Printf("  - [%s] %s when=%s spawn=%s (source: %s)\n",
			state, r.ID, when, r.Spawn, r.Source)
	}

	fmt.Println("\nChecklists (size in bytes):")
	keys := make([]string, 0, len(tpl.Checklists))
	for k := range tpl.Checklists {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-15s  %d bytes\n", k, len(tpl.Checklists[k]))
	}

	if inputsPath != "" {
		inputs, err := readInputsYAML(inputsPath)
		if err != nil {
			cliDie(err.Error())
		}
		plan, err := tasktemplates.BuildPlan(inputs, tpl)
		if err != nil {
			cliDie(err.Error())
		}
		printPlan(os.Stdout, plan)
	}
}

// runTaskTemplatePlan accepts --template <id> --inputs <yaml-path> and
// prints the planned aspects. Same path as the task_plan MCP tool but
// usable without a running daemon. Authors typically pipe the output
// through a YAML pretty-printer; we emit one human-readable summary
// followed by a structured YAML dump.
func runTaskTemplatePlan(args []string) {
	templateID := strings.TrimSpace(flagValue(args, "--template"))
	inputsPath := flagValue(args, "--inputs")
	if templateID == "" || inputsPath == "" {
		cliDie("usage: mcp-stringwork task-template plan --template <id> --inputs <yaml-path>")
	}

	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)
	srcs := pol.TaskTemplateSources()

	tpl, err := tasktemplates.Resolve(templateID, srcs)
	if err != nil {
		cliDie(err.Error())
	}
	inputs, err := readInputsYAML(inputsPath)
	if err != nil {
		cliDie(err.Error())
	}
	plan, err := tasktemplates.BuildPlan(inputs, tpl)
	if err != nil {
		cliDie(err.Error())
	}
	printPlan(os.Stdout, plan)
}

// runTaskTemplateDoctor walks every configured source and reports the
// validation issues. Exit code is 0 unless at least one source produced
// an ERROR; warnings stay non-fatal so doctor is safe to wire into
// pre-flight checks.
func runTaskTemplateDoctor(args []string) {
	_ = args

	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)
	srcs := pol.TaskTemplateSources()

	if len(srcs) == 0 {
		fmt.Println("No task-template sources configured.")
		return
	}

	issues, err := tasktemplates.Doctor(srcs)
	if err != nil {
		cliDie(err.Error())
	}
	if len(issues) == 0 {
		fmt.Println("[OK]    all sources valid; no issues found")
		return
	}
	hasError := false
	for _, iss := range issues {
		if iss.Severity == "ERROR" {
			hasError = true
		}
		label := iss.Severity
		if iss.Source != "" {
			label += " " + iss.Source
		}
		if iss.Template != "" {
			label += "/" + iss.Template
		}
		if iss.Aspect != "" {
			label += "::" + iss.Aspect
		}
		fmt.Printf("[%s] %s\n", label, iss.Message)
	}
	if hasError {
		os.Exit(1)
	}
}

// printPlan emits a human-readable summary followed by a YAML dump. The
// YAML form is what the task_plan MCP tool returns (modulo YAML vs
// JSON encoding) so authors can sanity-check the planner's output
// shape against what their driver code expects.
func printPlan(w io.Writer, plan tasktemplates.Plan) {
	fmt.Fprintf(w, "\nPlan for template %q (%d aspect(s) to spawn)\n", plan.Template, len(plan.Aspects))
	if len(plan.Tags) > 0 {
		fmt.Fprintf(w, "Detected tags: %s\n", strings.Join(plan.Tags, ", "))
	}
	for i, a := range plan.Aspects {
		fmt.Fprintf(w, "\n  %d) %s — %s (spawned by: %s)\n", i+1, a.Aspect, a.Title, strings.Join(a.SpawnedBy, ", "))
		fmt.Fprintf(w, "     description: %d bytes\n", len(a.Description))
		fmt.Fprintf(w, "     relevant_files: %d\n", len(a.RelevantFiles))
	}
	if len(plan.Aspects) == 0 {
		return
	}
	dump, err := yaml.Marshal(plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: marshal plan: %v\n", err)
		return
	}
	fmt.Fprintln(w, "\n--- yaml dump ---")
	fmt.Fprint(w, string(dump))
}

// readInputsYAML loads an inputs map from a YAML file. The file shape
// is the same one the MCP `inputs` arg accepts (e.g. `files: [a.go]`,
// `summary: "fix"`). YAML is preferred over JSON for CLI use because
// authors hand-write these files and YAML's multi-line strings are
// nicer for `summary` blocks.
func readInputsYAML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return raw, nil
}
