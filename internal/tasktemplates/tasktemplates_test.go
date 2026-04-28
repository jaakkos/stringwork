package tasktemplates

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestDefaultEmbeddedSource_LoadsCodeReview asserts the shipped
// default template parses cleanly and produces a usable Plan when
// fed minimal inputs. This is the fastest signal that a future
// edit to defaults/task-templates/code-review/ broke the schema.
func TestDefaultEmbeddedSource_LoadsCodeReview(t *testing.T) {
	src := DefaultEmbeddedSource()
	files, err := src.List()
	if err != nil {
		t.Fatalf("DefaultEmbeddedSource.List: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("expected at least one default template, got none")
	}

	tpl, err := Resolve("code-review", []Source{src})
	if err != nil {
		t.Fatalf("Resolve(code-review): %v", err)
	}
	wantAspects := []string{"correctness", "code-quality", "security", "observability", "data-model", "performance"}
	if got := len(tpl.Aspects); got != len(wantAspects) {
		t.Fatalf("expected %d aspects, got %d", len(wantAspects), got)
	}
	for _, want := range wantAspects {
		if _, ok := tpl.Checklists[want]; !ok {
			t.Errorf("missing checklist for aspect %q", want)
		}
	}

	plan, err := BuildPlan(map[string]any{
		"files":   []string{"internal/auth/secret_helper.go", "proto/account.proto"},
		"summary": "rotate auth secrets",
	}, tpl)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Template != "code-review" {
		t.Errorf("plan.Template = %q, want code-review", plan.Template)
	}
	gotAspects := make([]string, 0, len(plan.Aspects))
	for _, a := range plan.Aspects {
		gotAspects = append(gotAspects, a.Aspect)
	}
	// always-correctness + always-code-quality + secrets-files (SECURITY) +
	// proto-files (PROTO) — order: declaration order in routing.yaml.
	wantOrder := []string{"correctness", "code-quality", "security", "data-model"}
	if !equalSlices(gotAspects, wantOrder) {
		t.Errorf("plan aspect order = %v, want %v", gotAspects, wantOrder)
	}
	for _, a := range plan.Aspects {
		if !strings.Contains(a.Description, "Severity: MUST_FIX") {
			t.Errorf("aspect %q description missing finding format block", a.Aspect)
		}
		if !strings.Contains(a.Description, "**Files in scope**") {
			t.Errorf("aspect %q description missing files-in-scope block", a.Aspect)
		}
	}
}

// TestResolve_MergesAcrossSources covers the override semantics: a
// later source can append routing rules and concatenate checklists,
// but cannot replace an aspect that was first declared earlier
// (replace-by-id, earlier-source-wins). Doctor catches the disabled
// path; this test focuses on the merge alone.
func TestResolve_MergesAcrossSources(t *testing.T) {
	defaults := newFakeSource("stringwork-defaults", map[string]string{
		"code-review/template.yaml": `
id: code-review
title: Code Review
inputs:
  required: [files]
aspects:
  - id: correctness
    title: Correctness
`,
		"code-review/routing.yaml": `
routing:
  - id: always-correctness
    when: always
    spawn: correctness
`,
		"code-review/checklists/correctness.md": "default body\n",
	})

	team := newFakeSource("team", map[string]string{
		"code-review/template.yaml": `
id: code-review
title: Team Code Review
aspects:
  - id: correctness
    title: Team Correctness Override
  - id: regfin-domain
    title: RegFin Domain
`,
		"code-review/routing.yaml": `
routing:
  - id: always-regfin
    when: always
    spawn: regfin-domain
`,
		"code-review/checklists/correctness.md":   "team addendum\n",
		"code-review/checklists/regfin-domain.md": "regfin body\n",
	})

	merged, err := Resolve("code-review", []Source{defaults, team})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if merged.Title != "Code Review" {
		t.Errorf("title = %q, want %q (defaults wins title)", merged.Title, "Code Review")
	}
	if got := merged.Aspects[0].Title; got != "Correctness" {
		t.Errorf("aspect[0].title = %q, want Correctness (replace-by-id, earlier-source-wins)", got)
	}
	if len(merged.Aspects) != 2 || merged.Aspects[1].ID != "regfin-domain" {
		t.Fatalf("expected appended regfin-domain aspect, got %+v", merged.Aspects)
	}
	if len(merged.Routing) != 2 {
		t.Errorf("routing count = %d, want 2 (defaults + team)", len(merged.Routing))
	}
	want := "default body\n\nteam addendum"
	if got := merged.Checklists["correctness"]; got != want {
		t.Errorf("checklist[correctness] = %q, want %q (concat with blank line)", got, want)
	}
}

// TestResolve_ReplaceFrontMatter exercises the YAML front-matter
// `replace: true` escape hatch on the team checklist body. The team
// body must fully replace the default body, NOT concatenate.
func TestResolve_ReplaceFrontMatter(t *testing.T) {
	defaults := newFakeSource("stringwork-defaults", map[string]string{
		"code-review/template.yaml": `
id: code-review
title: Code Review
aspects:
  - id: correctness
    title: Correctness
`,
		"code-review/checklists/correctness.md": "default body\n",
	})
	team := newFakeSource("team", map[string]string{
		"code-review/template.yaml": `
id: code-review
title: Code Review
aspects:
  - id: correctness
    title: Correctness
`,
		"code-review/checklists/correctness.md": "---\nreplace: true\n---\nteam body\n",
	})
	merged, err := Resolve("code-review", []Source{defaults, team})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := merged.Checklists["correctness"]; got != "team body" {
		t.Errorf("checklist[correctness] = %q, want %q", got, "team body")
	}
}

// TestParseChecklistBody_HorizontalRuleIsContent confirms the v3 plan's
// implementation note: a body that legitimately starts with `---`
// (markdown horizontal rule) MUST be preserved as content, NOT silently
// stripped as front-matter. Front-matter recognition requires a
// trailing `---` line on its own.
func TestParseChecklistBody_HorizontalRuleIsContent(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantBody string
		wantRepl bool
	}{
		{
			name:     "front-matter with replace true",
			body:     "---\nreplace: true\n---\nbody\n",
			wantBody: "body\n",
			wantRepl: true,
		},
		{
			name:     "horizontal rule at top",
			body:     "---\nthis is a horizontal rule\nfollowed by content\n",
			wantBody: "---\nthis is a horizontal rule\nfollowed by content\n",
			wantRepl: false,
		},
		{
			name:     "no front-matter at all",
			body:     "# Title\n\nbody\n",
			wantBody: "# Title\n\nbody\n",
			wantRepl: false,
		},
		{
			name:     "front-matter terminator missing",
			body:     "---\nreplace: true\nbody continues\n",
			wantBody: "---\nreplace: true\nbody continues\n",
			wantRepl: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cb, err := parseChecklistBody([]byte(tc.body), "test.md")
			if err != nil {
				t.Fatalf("parseChecklistBody: %v", err)
			}
			if cb.Body != tc.wantBody {
				t.Errorf("body = %q, want %q", cb.Body, tc.wantBody)
			}
			if cb.Replace != tc.wantRepl {
				t.Errorf("replace = %v, want %v", cb.Replace, tc.wantRepl)
			}
		})
	}
}

// TestValidateInputs_RequiredAndTypes covers the freeform input
// declaration's two validation rules: required keys must be present
// and non-empty, declared types must match.
func TestValidateInputs_RequiredAndTypes(t *testing.T) {
	decl := InputDeclaration{
		Required: []string{"files", "summary"},
		Declarations: map[string]string{
			"files":   "list",
			"summary": "string",
		},
	}
	cases := []struct {
		name    string
		inputs  map[string]any
		wantErr string
	}{
		{
			name: "happy path",
			inputs: map[string]any{
				"files":   []string{"a.go"},
				"summary": "fix",
			},
		},
		{
			name: "missing required",
			inputs: map[string]any{
				"summary": "fix",
			},
			wantErr: "missing required input \"files\"",
		},
		{
			name: "empty required",
			inputs: map[string]any{
				"files":   []string{},
				"summary": "fix",
			},
			wantErr: "required input \"files\" is empty",
		},
		{
			name: "wrong type",
			inputs: map[string]any{
				"files":   "a.go",
				"summary": "fix",
			},
			wantErr: "input \"files\": expected list of strings, got string",
		},
		{
			name: "list of any with strings",
			inputs: map[string]any{
				"files":   []any{"a.go", "b.go"},
				"summary": "fix",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInputs(tc.inputs, decl)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// TestClassify_PatternMatching covers the documented glob extensions:
// shell-style "*" semantics, plus "**" interpreted as "match any path
// component" so common patterns like "**/migration/**" do the obvious
// thing.
func TestClassify_PatternMatching(t *testing.T) {
	classifiers := []Classifier{
		{ID: "secrets", Pattern: "*secret*", Tag: "SECURITY"},
		{ID: "proto", Pattern: "*.proto", Tag: "PROTO"},
		{ID: "migration", Pattern: "**/migration/**", Tag: "MIGRATION"},
		{ID: "disabled", Pattern: "*.go", Tag: "GO", Disabled: true},
	}
	tags := Classify([]string{
		"internal/auth/secret_helper.go",
		"proto/account.proto",
		"db/migration/202504_init.sql",
		"main.go",
	}, classifiers)
	wantSet := map[string]bool{"SECURITY": true, "PROTO": true, "MIGRATION": true}
	if len(tags) != len(wantSet) {
		t.Errorf("tags = %v, want set %v", tags, wantSet)
	}
	for _, tag := range tags {
		if !wantSet[tag] {
			t.Errorf("unexpected tag %q in %v", tag, tags)
		}
	}
}

// TestDoctor_FlagsCommonMistakes seeds a deliberately broken team
// overlay and asserts that doctor reports the expected ERROR/WARN set.
func TestDoctor_FlagsCommonMistakes(t *testing.T) {
	src := newFakeSource("broken", map[string]string{
		"buggy/template.yaml": `
id: buggy
title: Buggy
aspects:
  - id: correctness
    title: Correctness
`,
		"buggy/routing.yaml": `
routing:
  - id: orphan-spawn
    when: always
    spawn: nope
  - id: dead-rule
    when_tags: [NEVER_EMITTED]
    spawn: correctness
`,
		"buggy/classifiers.yaml": `
classifiers:
  - id: unused-tag
    pattern: "*.go"
    tag: GO
`,
	})
	issues, err := Doctor([]Source{src})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	gotMessages := make([]string, 0, len(issues))
	for _, iss := range issues {
		gotMessages = append(gotMessages, iss.Severity+": "+iss.Message)
	}
	mustContain := []string{
		"ERROR: routing rule \"orphan-spawn\" spawns unknown aspect \"nope\"",
		"WARN: routing rule \"dead-rule\" references tag \"NEVER_EMITTED\" that no classifier emits",
		"WARN: classifier \"unused-tag\" emits tag \"GO\" which no routing rule references",
	}
	for _, want := range mustContain {
		found := false
		for _, m := range gotMessages {
			if strings.Contains(m, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing doctor finding %q in %v", want, gotMessages)
		}
	}
}

// TestEmbedSource_GlobMatchesBasenameAndPath confirms matchPattern's
// dual-probe semantic — a "*.go" pattern matches both "foo.go" and
// "internal/foo.go". This is the part of the engine that diverges
// from raw filepath.Match.
func TestEmbedSource_GlobMatchesBasenameAndPath(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "internal/foo.go", true},
		{"*.go", "internal/foo.kt", false},
		{"**/migration/**", "db/migration/init.sql", true},
		{"**/migration/**", "internal/auth.go", false},
		{"*secret*", "internal/auth/secret_helper.go", true},
	}
	for _, tc := range cases {
		t.Run(filepath.Join(tc.pattern, tc.path), func(t *testing.T) {
			if got := matchPattern(tc.pattern, tc.path); got != tc.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

// fakeSource is an in-memory Source backed by testing/fstest.MapFS so
// merge tests don't need to touch disk. It mirrors EmbedSource's
// loader path (loadTemplateDir + listTemplateDirs) so we exercise the
// real loader against synthetic content.
type fakeSource struct {
	name string
	fs   fstest.MapFS
}

func newFakeSource(name string, files map[string]string) *fakeSource {
	mfs := fstest.MapFS{}
	for p, body := range files {
		mfs[p] = &fstest.MapFile{Data: []byte(strings.TrimLeft(body, "\n"))}
	}
	return &fakeSource{name: name, fs: mfs}
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) List() ([]TemplateFile, error) {
	dirs, err := listTemplateDirs(f.fs, ".")
	if err != nil {
		return nil, err
	}
	out := make([]TemplateFile, 0, len(dirs))
	for _, dir := range dirs {
		tf, err := loadTemplateDir(f.fs, dir, f.name)
		if err != nil {
			return nil, err
		}
		out = append(out, tf)
	}
	return out, nil
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
