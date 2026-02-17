package collab

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// registerResources adds MCP Resources and Resource Templates so that agents
// can read their instructions, project config, and workflow guides directly
// from the server — no per-project AGENTS.md/CLAUDE.md/CODEX.md files needed.
func registerResources(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	agentTypes := app.OrchestrationAgentTypes(svc.Policy().Orchestration())

	// ── Static resources ──────────────────────────────────────────────

	// Per-agent instruction resources
	for _, agent := range agentTypes {
		agentName := agent // capture loop variable
		s.AddResource(
			mcp.NewResource(
				fmt.Sprintf("stringwork://instructions/%s", agentName),
				fmt.Sprintf("Instructions for %s", agentName),
				mcp.WithResourceDescription(fmt.Sprintf("Pair programming instructions and workflow guide for the %s agent. Read this at session start.", agentName)),
				mcp.WithMIMEType("text/markdown"),
			),
			func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
				logger.Printf("Resource read: instructions/%s", agentName)
				var driverID string
				_ = svc.Query(func(state *domain.CollabState) error {
					driverID = state.DriverID
					return nil
				})
				var text string
				if driverID != "" {
					text = InstructionsForRole(agentName, driverID)
				} else {
					text = agentInstructions(agentName, agentTypes)
				}
				return []mcp.ResourceContents{
					mcp.TextResourceContents{
						URI:      req.Params.URI,
						MIMEType: "text/markdown",
						Text:     text,
					},
				}, nil
			},
		)
	}

	// Workflow guide
	s.AddResource(
		mcp.NewResource(
			"stringwork://guides/workflow",
			"Collaboration Workflow Guide",
			mcp.WithResourceDescription("Patterns for pair programming: code+review, divide-and-conquer, shared planning, autonomous loop."),
			mcp.WithMIMEType("text/markdown"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			logger.Println("Resource read: guides/workflow")
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     workflowGuide(),
				},
			}, nil
		},
	)

	// Quick reference
	s.AddResource(
		mcp.NewResource(
			"stringwork://guides/quick-reference",
			"Quick Reference",
			mcp.WithResourceDescription("Concise command reference for all 21 MCP tools."),
			mcp.WithMIMEType("text/markdown"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			logger.Println("Resource read: guides/quick-reference")
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     quickReference(),
				},
			}, nil
		},
	)

	// ── Resource templates (dynamic) ──────────────────────────────────

	// Agent instructions template: stringwork://instructions/{agent}
	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"stringwork://instructions/{agent}",
			"Agent Instructions",
			mcp.WithTemplateDescription("Read instructions for a specific agent (cursor, claude-code, codex, or any registered agent)."),
			mcp.WithTemplateMIMEType("text/markdown"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			// Extract agent from URI: stringwork://instructions/{agent}
			agent := strings.TrimPrefix(req.Params.URI, "stringwork://instructions/")
			logger.Printf("Resource template read: instructions/%s", agent)
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     agentInstructions(agent, agentTypes),
				},
			}, nil
		},
	)
}

// agentInstructions returns the full markdown instructions for a given agent.
func agentInstructions(agent string, agentTypes []string) string {
	pair := pairForAgent(agent)
	others := otherAgents(agent, agentTypes)

	return fmt.Sprintf(`# %s — Pair Programming Instructions

## Identity

You are **%s** in the pair programming system.

## Agents

Three agents collaborate in this system:
- **cursor** — Cursor IDE agent
- **claude-code** — Claude Code CLI agent
- **codex** — OpenAI Codex CLI agent

All agents share state via a global SQLite database.

## Before Starting Any Task

1. **Check session context:**
`+"`"+`get_session_context for '%s'`+"`"+`

2. **Set your presence:**
`+"`"+`set_presence agent='%s' status='working' workspace='/path/to/project'`+"`"+`

3. **Read unread messages:**
`+"`"+`read_messages for '%s'`+"`"+`

4. **Check assigned tasks:**
`+"`"+`list_tasks assigned_to='%s'`+"`"+`

5. **Check for updates from your pairs:**
- Run `+"`"+`list_tasks assigned_to='%s'`+"`"+` to see their progress.
- React to completions and reply to messages so the loop continues.

## Working on Tasks

### Receiving a Task

1. Claim: `+"`"+`update_task id=X status='in_progress' updated_by='%s'`+"`"+`
2. Do the work using your native tools.
3. Report: `+"`"+`send_message from='%s' to='%s' content='summary'`+"`"+`
4. Complete: `+"`"+`update_task id=X status='completed' updated_by='%s'`+"`"+`

### Delegating Work

`+"`"+`create_task title='...' assigned_to='%s' created_by='%s'`+"`"+`
`+"`"+`send_message from='%s' to='%s' content='context'`+"`"+`

### Requesting Code Review

`+"`"+`request_review from='%s' to='%s' description='what to review'`+"`"+`

## Collaboration Patterns

### Pattern 1: Code + Review
One agent writes code, another reviews it.

### Pattern 2: Divide and Conquer
Split work across agents via tasks, integrate when done.

### Pattern 3: Shared Planning
`+"`"+`create_plan id='feature-x' title='...' goal='...'`+"`"+`
`+"`"+`update_plan action='add_item' id='1' title='...' owner='%s'`+"`"+`
All agents work from the same plan.

### Pattern 4: Autonomous Loop
1. `+"`"+`claim_next agent='%s' dry_run=true`+"`"+` — peek at next action
2. `+"`"+`claim_next agent='%s'`+"`"+` — claim it
3. Do the work
4. `+"`"+`update_task id=X status='completed'`+"`"+` or `+"`"+`handoff`+"`"+`
5. Repeat

## Available MCP Tools (21)

| Category | Tools |
|----------|-------|
| Messaging | send_message, read_messages |
| Tasks | create_task, list_tasks, update_task |
| Planning | create_plan, get_plan, update_plan |
| Session | get_session_context, set_presence, append_session_note |
| Workflow | handoff, claim_next, request_review |
| Files | lock_file |
| Agents | register_agent, list_agents |
| Workers | worker_status, heartbeat |
| Work Context | get_work_context, update_work_context |

## Important Rules

- ALWAYS check `+"`"+`get_session_context`+"`"+` at session start
- ALWAYS communicate via `+"`"+`send_message`+"`"+` — your pairs cannot see your work otherwise
- ALWAYS update task status so your pairs know progress
- State is global at ~/.config/stringwork/state.sqlite
`,
		titleCase(agent), agent,
		agent, agent, agent, agent,
		others,
		agent, agent, pair, agent,
		pair, agent, agent, pair,
		agent, pair,
		agent,
		agent, agent,
	)
}

// titleCase uppercases the first letter of s.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// otherAgents returns a comma-separated list of agent types excluding the given one.
func otherAgents(agent string, agentTypes []string) string {
	var others []string
	for _, a := range agentTypes {
		if a != agent {
			others = append(others, a)
		}
	}
	return strings.Join(others, "', '")
}

// workflowGuide returns the collaboration workflow patterns guide.
func workflowGuide() string {
	return `# Collaboration Workflow Guide

## Pattern 1: Code + Review

Best for: quality-focused development

1. One agent writes code
2. Creates a review task: ` + "`" + `create_task title='Review auth module' assigned_to='claude-code' created_by='cursor'` + "`" + `
3. Reviewer analyzes and sends findings via ` + "`" + `send_message` + "`" + `
4. Author reads findings and iterates

## Pattern 2: Divide and Conquer

Best for: parallel work on independent components

1. Create tasks for different components, assigned to different agents
2. All agents work on their assigned tasks simultaneously
3. Each sends updates via messages
4. Integrate when tasks complete

## Pattern 3: Shared Planning

Best for: complex features requiring coordination

1. Create plan: ` + "`" + `create_plan id='feature-x' title='...' goal='...'` + "`" + `
2. Add items: ` + "`" + `update_plan action='add_item' id='1' title='...' owner='cursor'` + "`" + `
3. Claim work: ` + "`" + `update_plan action='update_item' item_id='1' status='in_progress'` + "`" + `
4. Report progress: ` + "`" + `update_plan action='update_item' item_id='1' status='completed' add_note='Done'` + "`" + `
5. Check status: ` + "`" + `get_plan` + "`" + `

## Pattern 4: Fully Autonomous Loop

Best for: hands-off pair programming

1. ` + "`" + `claim_next agent='<you>' dry_run=true` + "`" + ` — peek
2. ` + "`" + `claim_next agent='<you>'` + "`" + ` — claim
3. Do the work
4. ` + "`" + `update_task id=X status='completed'` + "`" + ` or ` + "`" + `handoff` + "`" + `
5. Repeat

## Pattern 5: Multi-Agent Orchestration

Best for: cursor driving both claude-code and codex

1. Cursor creates tasks for both agents
2. ` + "`" + `send_message from='cursor' to='claude-code' content='Implement data model'` + "`" + `
3. ` + "`" + `send_message from='cursor' to='codex' content='Write tests for data model'` + "`" + `
4. Auto-respond spawns both CLIs in parallel
5. Both report back via ` + "`" + `send_message` + "`" + `
6. Cursor reviews, integrates, and assigns next round

## Anti-Patterns

- Starting work without checking context
- Completing work without communicating
- Creating duplicate tasks (check ` + "`" + `list_tasks` + "`" + ` first)
- Ignoring unread messages
`
}

// quickReference returns a concise command reference.
func quickReference() string {
	return `# Quick Reference

## Session Start
` + "```" + `
get_session_context for '<agent>'
set_presence agent='<agent>' status='working' workspace='<path>'
read_messages for '<agent>'
list_tasks assigned_to='<agent>'
` + "```" + `

## Messaging
` + "```" + `
send_message from='<you>' to='<pair>' content='...'
send_message from='<you>' to='all' content='...'       # broadcast
read_messages for='<you>'
read_messages for='<you>' unread_only=true
` + "```" + `

## Tasks
` + "```" + `
create_task title='...' assigned_to='<pair>' created_by='<you>'
list_tasks                                               # all tasks
list_tasks assigned_to='<agent>' status='pending'        # filtered
update_task id=X status='in_progress' updated_by='<you>'
update_task id=X status='completed' updated_by='<you>'
` + "```" + `

## Planning
` + "```" + `
create_plan id='feature-x' title='...' goal='...' created_by='<you>'
get_plan                                                 # active plan
get_plan id='feature-x'                                  # specific plan
update_plan action='add_item' id='1' title='...' owner='<you>' updated_by='<you>'
update_plan action='update_item' item_id='1' status='completed' updated_by='<you>'
` + "```" + `

## Workflow
` + "```" + `
claim_next agent='<you>' dry_run=true                    # peek
claim_next agent='<you>'                                 # claim
handoff from='<you>' to='<pair>' summary='...' next_steps='...'
request_review from='<you>' to='<pair>' description='...'
` + "```" + `

## Session
` + "```" + `
set_presence agent='<you>' status='working' note='...'
append_session_note author='<you>' content='Decision: use JWT'
lock_file agent='<you>' path='src/auth.go' action='lock' reason='editing'
lock_file action='list'                                  # see all locks
` + "```" + `

## Agents
` + "```" + `
list_agents                                              # see all agents
register_agent name='my-bot' display_name='My Bot'       # custom agent
` + "```" + `
`
}
