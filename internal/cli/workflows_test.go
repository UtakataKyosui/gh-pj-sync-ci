package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
)

func workflowFake(ws ...ghapi.Workflow) *fakeAPI {
	f := newFake()
	f.workflows = ws
	return f
}

var autoAdd = ghapi.Workflow{Name: "Auto-add to project", Enabled: true}

func TestInitWarnsAboutAutoAdd(t *testing.T) {
	dir := repoDir(t, "", "")
	f := workflowFake(autoAdd, ghapi.Workflow{Name: "Auto-add sub-issues to project", Enabled: true},
		ghapi.Workflow{Name: "Item closed", Enabled: true})
	r := run(t, f, inDir(noColorTTY(), dir), "init", "--owner", "acme", "--project", "1")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{
		`Warning: Project acme/1 has "Auto-add to project" and "Auto-add sub-issues to project" turned on.`,
		"doesn't change the project's workflows",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("missing %q in\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stdout, "Warning") {
		t.Fatalf("warning leaked to stdout: %s", r.stdout)
	}
}

func TestInitStaysQuietWithoutOverlap(t *testing.T) {
	for name, f := range map[string]*fakeAPI{
		"disabled":      workflowFake(ghapi.Workflow{Name: "Auto-add to project"}),
		"other":         workflowFake(ghapi.Workflow{Name: "Item added to project", Enabled: true}),
		"none":          workflowFake(),
		"read fails":    {workflowsErr: errors.New("forbidden")},
		"unknown title": workflowFake(ghapi.Workflow{Name: "Auto-add to project 2", Enabled: true}),
	} {
		dir := repoDir(t, "", "")
		r := run(t, f, inDir(noColorTTY(), dir), "init", "--owner", "acme", "--project", "1")
		if r.code != 0 || strings.Contains(r.stderr, "Warning") {
			t.Fatalf("%s: %+v", name, r)
		}
	}
}

func TestInitDryRunDoesNotReadTheProject(t *testing.T) {
	dir := repoDir(t, "", "")
	f := workflowFake(autoAdd)
	r := run(t, f, inDir(noColorTTY(), dir), "init", "--owner", "acme", "--project", "1", "--dry-run")
	if r.code != 0 || len(f.calls) != 0 || strings.Contains(r.stderr, "Warning") {
		t.Fatalf("%+v %v", r, f.calls)
	}
}

func TestInitWorksWithoutAPIAccess(t *testing.T) {
	dir := repoDir(t, "", "")
	r := run(t, nil, inDir(noColorTTY(), dir), "init", "--owner", "acme", "--project", "1")
	if r.code != 0 || strings.Contains(r.stderr, "Warning") {
		t.Fatalf("%+v", r)
	}
}

func TestInitJSONWarnings(t *testing.T) {
	dir := repoDir(t, "", "")
	r := run(t, workflowFake(autoAdd), inDir(noColorTTY(), dir), "init", "--owner", "acme", "--project", "1", "--json")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	w := decodeJSON(t, r.stdout)["warnings"].([]any)[0].(map[string]any)
	if w["reason"] != "overlapping_workflow" || w["workflows"].([]any)[0] != "Auto-add to project" {
		t.Fatalf("%v", w)
	}
}

func TestConfigCheckWarnsAboutAutoAdd(t *testing.T) {
	dir := repoDir(t, ".github/pj-synker.yml", validConfig)
	r := run(t, workflowFake(autoAdd), inDir(noColorTTY(), dir), "config", "check")
	if r.code != 0 || !strings.Contains(r.stderr, `has "Auto-add to project" turned on`) || strings.Contains(r.stdout, "Warning") {
		t.Fatalf("%+v", r)
	}
	r = run(t, workflowFake(autoAdd), inDir(env{vars: map[string]string{}}, dir), "config", "check", "--json")
	if _, ok := decodeJSON(t, r.stdout)["warnings"]; !ok || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
}

func TestConfigCheckSkipsWithoutAProject(t *testing.T) {
	dir := repoDir(t, ".pj-synker.yml", "filters:\n  bots: true\n")
	f := workflowFake(autoAdd)
	r := run(t, f, inDir(noColorTTY(), dir), "config", "check")
	if r.code != 0 || len(f.calls) != 0 {
		t.Fatalf("%+v %v", r, f.calls)
	}
}
