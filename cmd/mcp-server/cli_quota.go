// Quota CLI subcommands — no daemon required (direct HTTP against stored OAuth tokens).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jaakkos/stringwork/internal/quota"
)

func runQuotaCommand(args []string) {
	if len(args) == 0 || args[0] != "check" {
		printQuotaUsage(os.Stderr)
		os.Exit(1)
	}
	runQuotaCheck(args[1:])
}

func printQuotaUsage(w io.Writer) {
	fmt.Fprint(w, `usage: mcp-stringwork quota check [--json] [--agent TYPE]

Check worker CLI quota via zero-token HTTP APIs (no daemon required).

  --json           Machine-readable JSON; exit 1 if any type is blocked
  --agent TYPE     Check one agent type (claude-code, codex, gemini)

OAuth/API-key note: API-key auth has no per-user usage endpoint — those types
report as no-credentials and are treated as OK (fail-open).
`)
}

func runQuotaCheck(args []string) {
	jsonOut := hasCLIArg(args, "--json")
	agentFilter := flagValue(args, "--agent")

	types := []string{"claude-code", "codex", "gemini"}
	if agentFilter != "" {
		types = []string{agentFilter}
	}

	checkers := quota.BuildCheckers(types)
	if len(checkers) == 0 {
		cliDie("unknown --agent type (want claude-code, codex, or gemini)")
	}

	mon := quota.NewMonitor(checkers, quota.MonitorConfig{CacheTTL: 2 * time.Minute, FailOpen: true}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	type row struct {
		AgentType string `json:"agent_type"`
		State     string `json:"state"`
		Blocked   bool   `json:"blocked"`
		Summary   string `json:"summary"`
		Reason    string `json:"reason,omitempty"`
	}
	var rows []row
	anyBlocked := false

	for _, agentType := range types {
		status := mon.CheckDirect(ctx, agentType)
		entry := row{AgentType: agentType}
		switch status.Kind {
		case quota.KindBlocked:
			entry.State = "BLOCKED"
			entry.Blocked = true
			entry.Summary = status.Summary
			entry.Reason = status.Reason
			anyBlocked = true
		case quota.KindNoCredentials:
			entry.State = "NO_CREDENTIALS"
			entry.Summary = "no OAuth credentials (fail-open)"
		case quota.KindCheckFailed:
			entry.State = "CHECK_FAILED"
			if status.Err != nil {
				entry.Summary = status.Err.Error()
			} else {
				entry.Summary = "check failed (fail-open)"
			}
		default:
			entry.State = "OK"
			entry.Summary = status.Summary
			if entry.Summary == "" {
				entry.Summary = "OK"
			}
		}
		rows = append(rows, entry)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].AgentType < rows[j].AgentType })

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"agents": rows}); err != nil {
			cliDie(err.Error())
		}
		if anyBlocked {
			os.Exit(1)
		}
		return
	}

	for _, r := range rows {
		switch r.State {
		case "BLOCKED":
			fmt.Printf("%s: BLOCKED — %s\n", r.AgentType, r.Summary)
		case "NO_CREDENTIALS":
			fmt.Printf("%s: skip — %s\n", r.AgentType, r.Summary)
		case "CHECK_FAILED":
			fmt.Printf("%s: degraded — %s\n", r.AgentType, r.Summary)
		default:
			fmt.Printf("%s: OK — %s\n", r.AgentType, r.Summary)
		}
	}
	if anyBlocked {
		os.Exit(1)
	}
}

func hasCLIArg(args []string, key string) bool {
	for _, a := range args {
		if a == key {
			return true
		}
		if strings.HasPrefix(a, key+"=") {
			return true
		}
	}
	return false
}
