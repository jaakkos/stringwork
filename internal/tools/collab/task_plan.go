package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/tasktemplates"
)

// taskPlanResponse is the JSON-shaped payload returned to the driver.
// The driver iterates Aspects and emits one create_task call per
// PlannedAspect, copying Description and RelevantFiles straight
// through. Tags and Template are included for transparency / audit
// (the driver may want to log "we planned 4 aspects against tags
// [SECURITY PROTO]").
type taskPlanResponse struct {
	Template string                        `json:"template"`
	Tags     []string                      `json:"tags"`
	Aspects  []tasktemplates.PlannedAspect `json:"aspects"`
	Notes    []string                      `json:"notes,omitempty"`
}

// registerTaskPlan registers the task_plan MCP tool.
//
// task_plan takes a template id ("code-review") and a freeform input
// map ({files: [...], summary: "..."}) and returns an ordered list of
// PlannedAspects ready to be passed to create_task. The driver is the
// orchestrator — task_plan is a pure planner that returns what to
// spawn but never spawns directly. Keeping the planner pure means
// drivers can preview a plan (CLI `task-template plan`), tweak inputs,
// and re-call without side effects.
//
// Why one tool, not three:
//
//	v1 ships only the code-review template, so a `task_templates()`
//	listing tool would return one entry and a `task_aspects()` browse
//	tool would return the same six aspect ids forever. CLI
//	`task-template list` / `show` cover the authoring-debug path. Once
//	a second template lands, revisit and add browse-style tools then.
func registerTaskPlan(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("task_plan",
			mcp.WithDescription(
				"Plan a templated multi-aspect task (e.g. code-review). "+
					"Returns an ordered list of PlannedAspects — each one is the "+
					"full payload for a single create_task call. The planner "+
					"validates inputs, classifies them into tags, and applies "+
					"the template's routing rules to decide which aspects to "+
					"spawn. The driver iterates the result and creates one task "+
					"per aspect; the description for each task is composed by "+
					"the planner (background + checklist + finding format), so "+
					"the driver passes it through unchanged.",
			),
			mcp.WithString("template", mcp.Required(),
				mcp.Description("Template id to plan (today: \"code-review\").")),
			mcp.WithObject("inputs", mcp.Required(),
				mcp.Description("Freeform input map. The shape required by the "+
					"named template; for code-review: "+
					"{files: [paths...], summary: string}. Files is required.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			templateID, _ := args["template"].(string)
			templateID = strings.TrimSpace(templateID)
			if templateID == "" {
				return nil, fmt.Errorf("template is required")
			}
			inputsAny, ok := args["inputs"]
			if !ok || inputsAny == nil {
				return nil, fmt.Errorf("inputs is required")
			}
			inputs, ok := inputsAny.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("inputs must be an object, got %T", inputsAny)
			}

			sources := svc.Policy().TaskTemplateSources()
			tpl, err := tasktemplates.Resolve(templateID, sources)
			if err != nil {
				return nil, fmt.Errorf("resolve template %q: %w", templateID, err)
			}
			plan, err := tasktemplates.BuildPlan(inputs, tpl)
			if err != nil {
				return nil, fmt.Errorf("plan template %q: %w", templateID, err)
			}

			resp := taskPlanResponse{
				Template: plan.Template,
				Tags:     plan.Tags,
				Aspects:  plan.Aspects,
			}
			if len(plan.Aspects) == 0 {
				// No aspect fired — the driver should know whether
				// this is "no work needed" (docs-only PR) or "the
				// inputs missed every classifier" (config typo).
				resp.Notes = append(resp.Notes,
					"No aspects fired. Either the change matches no classifier "+
						"(docs-only / config-only) or the input files were not "+
						"specific enough — verify with `mcp-stringwork task-template plan`.")
			}
			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("marshal plan: %w", err)
			}
			if logger != nil {
				logger.Printf("task_plan: template=%s aspects=%d tags=%v",
					plan.Template, len(plan.Aspects), plan.Tags)
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)
}
