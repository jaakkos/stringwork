# Team Rules

Stringwork's [constitution](CONSTITUTION.md) is per-user by default. This
page is for teams that already maintain a shared devtools repo
(`team-rules.git`, `regfin-devtools`, `infra-style-guide`, ...) and
want every developer's Stringwork to pick those rules up automatically.

## The problem

Each developer running Stringwork has a personal constitution at
`~/.config/stringwork/constitution/`. That works fine for solo style
preferences ("I always want my agent to wrap commit messages at 72
characters"). It does **not** scale to:

- Team-wide review checklists shipped with a stack-specific
  language style guide.
- Cross-cutting safety rules (no secrets in screenshots, never push to
  `main`, never use the wrong Atlassian instance, ...).
- Repo-shape conventions (commit format, PR template, branch naming).

These already live in the team's devtools repo. Stringwork should read
them straight from there — no copy-paste, no per-user setup.

## The solution: profiles

A **constitution profile** is a YAML file that lists the sources a team
wants every developer to load. The team commits the profile alongside
their devtools repo; each developer points their personal Stringwork
config at it with one line:

```yaml
# ~/.config/stringwork/config.yaml
constitution:
  profile: ~/Development/regfin-devtools/stringwork.profile.yaml
```

That's it. Pull the devtools repo, the rules update on the next
`claim_next`. No custom CLI workflow.

> **Tip — team-shipped installers.** If your team's devtools repo ships
> an idempotent installer for this wiring, prefer it over hand-editing
> `~/.config/stringwork/config.yaml`. RegFin teammates, for example,
> can run `make stringwork-constitution` from a `regfin-devtools`
> checkout and get backup, dry-run, and uninstall symmetry for free
> instead of editing the YAML by hand.

## Path tokens in profiles

Profiles understand `$PROFILE_DIR`, which resolves to the directory of
the profile file itself. That makes the profile portable — every
developer can clone the devtools repo to a different absolute path and
the source declarations still work.

```yaml
sources:
  - name: rules
    type: dir
    path: $PROFILE_DIR/rules
  - name: instructions
    type: dir
    path: $PROFILE_DIR/instructions
```

`~`, `$VAR`, and `${VAR}` are also expanded.

## Worked example: RegFin team

A complete profile for the RegFin devtools repo is shipped at
[docs/profiles/regfin-devtools.profile.yaml](profiles/regfin-devtools.profile.yaml).
Drop it into the root of `regfin-devtools` and reference it from your
personal config:

```yaml
constitution:
  profile: ~/Development/regfin-devtools/stringwork.profile.yaml
```

What it gives every Stringwork worker:

| Source                          | Path                                                   | Scope               |
|---------------------------------|--------------------------------------------------------|---------------------|
| `regfin-rules`                  | `$PROFILE_DIR/rules`                                   | always              |
| `regfin-instructions`           | `$PROFILE_DIR/instructions`                            | always              |
| `regfin-pr-review-checklists`   | `$PROFILE_DIR/skills/regfin-pr-review/references`      | `task_kind: review` |
| `regfin-team-context`           | `$PROFILE_DIR/context` (regfin-team.md, repos.yaml...) | always              |

Run `mcp-stringwork constitution show` to preview what a regular task
gets, or `--task-kind review` to preview what a review task gets.

## Workflow once a profile is in place

1. Team author edits a markdown file in `regfin-devtools/rules/` and
   commits to main.
2. Each developer `git pull`s the devtools repo on their normal cadence.
3. Their next worker spawn picks up the change automatically — no
   extra command, no daemon restart. (The `dir`-source case; for `git`
   sources, `mcp-stringwork constitution sync` is the equivalent.)

## Authoring guidelines

- **Keep files small.** Every file is inlined into every spawn prompt.
  A 200-line markdown file costs every worker on every task.
- **Single subject per file.** "How to write Kotlin", "How to review a
  PR", "Atlassian safety rules" — never combine.
- **Use scope filters generously.** Review checklists belong on review
  tasks, not feature work. Style guides for one language belong only
  when that language is in play (the [task_kind heuristic](CONSTITUTION.md#scope-filtering)
  takes care of the obvious cases; you can register custom kinds
  later).
- **Order matters.** Earlier files win conflicts. Put the universal
  safety rules first, language-specific style next, scoped checklists
  last.
- **Don't duplicate the project's own AGENTS.md / CLAUDE.md.** Those
  are read directly by the agents. The constitution is for rules the
  agents need *no matter which repo* they're working in.

## Migrating an existing devtools repo

If you already have a directory of rules, you don't need to rearrange
anything to publish a profile. Just:

1. Copy [docs/profiles/regfin-devtools.profile.yaml](profiles/regfin-devtools.profile.yaml)
   to the root of your devtools repo (rename if you like).
2. Adjust the `path` entries to match your repo layout.
3. Optionally add `scope:` blocks to scope individual directories to
   review-only / role-only tasks.
4. Commit. Tell teammates to add the `constitution.profile:` line to
   their personal `~/.config/stringwork/config.yaml`.
5. Run `mcp-stringwork constitution doctor` to confirm every source
   resolves.

## Troubleshooting

| Symptom                                    | Cause                                                  | Fix                                                      |
|--------------------------------------------|--------------------------------------------------------|----------------------------------------------------------|
| `constitution: read <profile>: no such file` | Profile path wrong or repo not cloned.                 | Check `constitution.profile` in your config.             |
| Source missing from `show` output           | Bad declaration in profile (logged to stderr).         | Run `mcp-stringwork constitution doctor`.                |
| `[ERROR] regfin-rules ... no files match`   | `include` glob doesn't match anything in the directory.| Adjust glob or remove `include` to use the default `*.md`. |
| Reviewers don't see review checklist        | Task title doesn't trigger `task_kind: review`.        | Use a title that contains the word `review` (e.g. `Code review of foo`, `PR review of bar`). The heuristic returns the same `task_kind: "review"` for all of these. If you want unconditional checklists, remove the scope filter. |

For the underlying mechanics, see [CONSTITUTION.md](CONSTITUTION.md).
