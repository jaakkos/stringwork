// Admin CLI subcommands. Operate directly on the SQLite store so they work
// even when no daemon is running. Intended as the supported alternative to
// hand-written `sqlite3` DELETE statements when the worker pool is in a bad
// state.
package main

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
	"github.com/jaakkos/stringwork/internal/repository"
)

func adminLogger() *log.Logger { return log.New(os.Stderr, "[admin] ", 0) }

// runAdminCommand dispatches `mcp-stringwork admin <subcommand>`.
func runAdminCommand(args []string) {
	if len(args) == 0 {
		printAdminUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "prune":
		runAdminPrune(args[1:])
	case "pool-status":
		runAdminPoolStatus(args[1:])
	case "-h", "--help", "help":
		printAdminUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown admin subcommand: %s\n\n", args[0])
		printAdminUsage()
		os.Exit(1)
	}
}

func printAdminUsage() {
	fmt.Fprint(os.Stderr, `usage: mcp-stringwork admin <subcommand> [flags]

Subcommands:
  prune        Garbage-collect Presence and AgentInstance rows.
                 --presence            Prune stale Presence rows.
                 --instances           Prune offline AgentInstance rows.
                 --older-than DURATION Override retention (e.g. 7d, 24h, 30m).
                                       Applies to whichever category is selected.
                                       Without this flag, policy defaults from
                                       config.yaml are used.
                 --task-bound-older-than DURATION
                                       Separate retention for task-bound
                                       worker rows (default: policy value).
                 --dry-run             Show what would be removed without
                                       writing to the store.

  pool-status  Print a summary of the worker pool: active, offline, oldest
               stale row per category. Read-only.

Notes:
  - These commands talk to the SQLite store directly, so they work whether
    the daemon is running or stopped. If the daemon is running it will not
    see the changes until its next reload (typically the next watchdog tick
    will re-read affected fields).
  - Prefer this over hand-written sqlite3 DELETE statements: it understands
    the same task-bound vs static-pool distinction the runtime uses, so
    you won't accidentally wipe live workers.

`)
}

// adminLoadStore opens the on-disk SQLite store and returns the loaded state.
// The caller is responsible for invoking the returned close function.
func adminLoadStore() (app.StateRepository, *domain.CollabState, func()) {
	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)

	repo, err := repository.NewStateRepository(pol.StateFile())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open state store at %s: %v\n", pol.StateFile(), err)
		os.Exit(1)
	}
	close := func() {
		if c, ok := repo.(interface{ Close() error }); ok {
			_ = c.Close()
		}
	}
	state, err := repo.Load()
	if err != nil {
		close()
		fmt.Fprintf(os.Stderr, "error: load state: %v\n", err)
		os.Exit(1)
	}
	if state == nil {
		state = domain.NewCollabState()
	}
	app.EnsureStateMaps(state)
	return repo, state, close
}

// runAdminPrune wraps app.PrunePresence + app.PruneInstances behind a CLI.
func runAdminPrune(args []string) {
	dryRun := flagPresent(args, "--dry-run")
	prunePresenceFlag := flagPresent(args, "--presence")
	pruneInstancesFlag := flagPresent(args, "--instances")
	if !prunePresenceFlag && !pruneInstancesFlag {
		// Default: do both, matching the runtime watchdog behaviour.
		prunePresenceFlag = true
		pruneInstancesFlag = true
	}

	repo, state, closeFn := adminLoadStore()
	defer closeFn()

	cfg := loadConfig(adminLogger())
	pol := policy.New(cfg)

	presenceDays := pol.PresenceRetentionDays()
	instanceDays := pol.InstanceRetentionDays()
	taskBoundHours := pol.TaskBoundInstanceRetentionHours()

	if v := flagValue(args, "--older-than"); v != "" {
		days, hours, err := parseRetentionDuration(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --older-than: %v\n", err)
			os.Exit(1)
		}
		if days > 0 {
			presenceDays = days
			instanceDays = days
		} else if hours > 0 {
			// Sub-day overrides — treat as hours for instance pruning,
			// keep presence at the (smaller of) explicit hours fallback.
			presenceDays = 0
			instanceDays = 0
			taskBoundHours = hours
		}
	}
	if v := flagValue(args, "--task-bound-older-than"); v != "" {
		_, hours, err := parseRetentionDuration(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --task-bound-older-than: %v\n", err)
			os.Exit(1)
		}
		if hours > 0 {
			taskBoundHours = hours
		}
	}

	var presencePruned, instancesPruned int
	if dryRun {
		// On dry-run, report counts against a temporary clone so we don't
		// mutate the real state. Cheaper than a full deepcopy for this use
		// case: presence/instances are bounded by config.
		clone := cloneStateForPrune(state)
		if prunePresenceFlag {
			presencePruned = app.PrunePresence(clone, presenceDays)
		}
		if pruneInstancesFlag {
			instancesPruned = app.PruneInstances(clone, instanceDays, taskBoundHours)
		}
	} else {
		if prunePresenceFlag {
			presencePruned = app.PrunePresence(state, presenceDays)
		}
		if pruneInstancesFlag {
			instancesPruned = app.PruneInstances(state, instanceDays, taskBoundHours)
		}
		if presencePruned+instancesPruned > 0 {
			if err := repo.Save(state); err != nil {
				fmt.Fprintf(os.Stderr, "error: save state: %v\n", err)
				os.Exit(1)
			}
		}
	}

	verb := "pruned"
	if dryRun {
		verb = "would prune"
	}
	if prunePresenceFlag {
		fmt.Printf("presence: %s %d row(s) (retention: %dd)\n", verb, presencePruned, presenceDays)
	}
	if pruneInstancesFlag {
		fmt.Printf("instances: %s %d row(s) (retention: %dd / %dh task-bound)\n",
			verb, instancesPruned, instanceDays, taskBoundHours)
	}
	if dryRun {
		fmt.Println("(dry-run — no changes written)")
	}
}

// runAdminPoolStatus prints a read-only summary of the worker pool.
func runAdminPoolStatus(_ []string) {
	_, state, closeFn := adminLoadStore()
	defer closeFn()

	now := time.Now()
	driver := app.ConfiguredDriver(state)

	// Bucket instances.
	var (
		activeCount   int
		offlineCount  int
		taskBoundIdle int
		oldestActive  *domain.AgentInstance
		oldestOffline *domain.AgentInstance
	)
	for id, inst := range state.AgentInstances {
		if inst == nil {
			continue
		}
		if inst.Role == domain.RoleDriver {
			continue
		}
		if app.IsTaskBoundInstance(state, id) && len(inst.CurrentTasks) == 0 {
			taskBoundIdle++
		}
		if inst.Status == "offline" {
			offlineCount++
			if oldestOffline == nil || inst.LastHeartbeat.Before(oldestOffline.LastHeartbeat) {
				oldestOffline = inst
			}
		} else {
			activeCount++
			if oldestActive == nil || inst.LastHeartbeat.Before(oldestActive.LastHeartbeat) {
				oldestActive = inst
			}
		}
	}

	// Presence summary.
	var (
		stalePresence    int
		oldestPresence   *domain.Presence
		fmtAge           = func(t time.Time) string { return now.Sub(t).Round(time.Second).String() }
		stalePresenceCut = 24 * time.Hour
	)
	for agent, p := range state.Presence {
		if p == nil || agent == driver {
			continue
		}
		if now.Sub(p.LastSeen) > stalePresenceCut {
			stalePresence++
		}
		if oldestPresence == nil || p.LastSeen.Before(oldestPresence.LastSeen) {
			oldestPresence = p
		}
	}

	fmt.Printf("Stringwork pool — %s\n", now.Format(time.RFC3339))
	fmt.Printf("  driver:               %s\n", driver)
	fmt.Printf("  agent instances:      %d total (%d active, %d offline)\n",
		len(state.AgentInstances), activeCount, offlineCount)
	fmt.Printf("  task-bound idle rows: %d\n", taskBoundIdle)
	if oldestActive != nil {
		fmt.Printf("  oldest active:        %s (heartbeat %s ago)\n",
			oldestActive.InstanceID, fmtAge(oldestActive.LastHeartbeat))
	}
	if oldestOffline != nil {
		fmt.Printf("  oldest offline:       %s (heartbeat %s ago)\n",
			oldestOffline.InstanceID, fmtAge(oldestOffline.LastHeartbeat))
	}
	fmt.Printf("  presence rows:        %d total (%d stale > %s)\n",
		len(state.Presence), stalePresence, stalePresenceCut)
	if oldestPresence != nil {
		fmt.Printf("  oldest presence:      %s (last_seen %s ago)\n",
			oldestPresence.Agent, fmtAge(oldestPresence.LastSeen))
	}

	// In-flight tasks per worker — quick visual cue for "do I have stuck work?".
	type taskRow struct {
		id     int
		title  string
		owner  string
		status string
		age    time.Duration
	}
	var inFlight []taskRow
	for _, t := range state.Tasks {
		if t.Status == "in_progress" {
			inFlight = append(inFlight, taskRow{
				id: t.ID, title: t.Title, owner: t.AssignedTo,
				status: t.Status, age: now.Sub(t.UpdatedAt),
			})
		}
	}
	sort.Slice(inFlight, func(i, j int) bool { return inFlight[i].age > inFlight[j].age })
	if len(inFlight) > 0 {
		fmt.Printf("  in-flight tasks:      %d\n", len(inFlight))
		for _, r := range inFlight {
			fmt.Printf("    #%d %-30s owner=%s age=%s\n",
				r.id, truncForCLI(r.title, 30), r.owner, r.age.Round(time.Second))
		}
	} else {
		fmt.Printf("  in-flight tasks:      0\n")
	}
}

// flagPresent returns true if name appears in args (no value required).
func flagPresent(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

// parseRetentionDuration accepts shorthand like "7d", "24h", "30m". When a
// duration is expressible in whole days, it returns days; otherwise it returns
// the equivalent hours (rounded up to 1).
func parseRetentionDuration(s string) (days, hours int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		n, perr := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if perr != nil || n < 0 {
			return 0, 0, fmt.Errorf("invalid days: %q", s)
		}
		return n, 0, nil
	}
	d, perr := time.ParseDuration(s)
	if perr != nil {
		return 0, 0, fmt.Errorf("invalid duration: %q (use 7d, 24h, 30m, ...)", s)
	}
	if d < time.Hour {
		return 0, 1, nil
	}
	return 0, int(d / time.Hour), nil
}

// cloneStateForPrune produces a shallow copy of the maps the prune helpers
// touch so a dry-run does not mutate the original state. We don't deepcopy
// the Presence/AgentInstance values themselves because the prune helpers
// only delete map keys.
func cloneStateForPrune(state *domain.CollabState) *domain.CollabState {
	if state == nil {
		return domain.NewCollabState()
	}
	clone := *state
	clone.Presence = make(map[string]*domain.Presence, len(state.Presence))
	for k, v := range state.Presence {
		clone.Presence[k] = v
	}
	clone.AgentInstances = make(map[string]*domain.AgentInstance, len(state.AgentInstances))
	for k, v := range state.AgentInstances {
		clone.AgentInstances[k] = v
	}
	return &clone
}

func truncForCLI(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
