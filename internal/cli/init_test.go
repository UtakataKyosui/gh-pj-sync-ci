package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UtakataKyosui/gh-pj-synker/internal/config"
)

func readConfig(t *testing.T, path string) *config.File {
	t.Helper()
	f, err := config.Load(path, path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestInitWritesAtTheCheckoutRoot(t *testing.T) {
	dir := repoDir(t, "", "")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}}, sub),
		"init", "--owner", "acme", "--project", "3", "--label", "triaged,good first issue", "--no-drafts")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	path := filepath.Join(dir, ".github", "pj-synker.yml")
	f := readConfig(t, path)
	if f.Owner != "acme" || f.Number != 3 || strings.Join(f.Labels, "|") != "triaged|good first issue" || f.Drafts == nil || *f.Drafts {
		t.Fatalf("%+v", f)
	}
	// stdout carries the path for scripts; stderr has the receipt.
	// A path above the working directory is shown in full.
	if r.stdout != path+"\n" || !strings.Contains(r.stderr, "Created") {
		t.Fatalf("%+v", r)
	}
	// add then reads it from the subdirectory.
	f2 := newFake()
	f2.put("acme/web", 1, func(i *ghapiItem) { i.Labels = []string{"triaged", "good first issue"} })
	r = run(t, f2, inDir(ci(), sub), "add", "1")
	if r.code != 0 || !strings.Contains(r.stderr, "Project         acme/3") {
		t.Fatalf("%+v", r)
	}
}

func TestInitNeverReplacesWithoutForce(t *testing.T) {
	dir := repoDir(t, ".github/pj-synker.yml", "# mine\nproject:\n  owner: old\n  number: 1\n")
	e := inDir(env{noAPI: true, vars: map[string]string{}}, dir)
	r := run(t, nil, e, "init", "--owner", "acme", "--project", "2")
	if r.code != exitFailure || !strings.Contains(r.stderr, "The file already exists.") || r.stdout != "" {
		t.Fatalf("%+v", r)
	}
	if f := readConfig(t, filepath.Join(dir, ".github", "pj-synker.yml")); f.Owner != "old" {
		t.Fatal("the existing file was changed")
	}
	r = run(t, nil, e, "init", "--owner", "acme", "--project", "2", "--force")
	if r.code != 0 || !strings.Contains(r.stderr, "Replaced") {
		t.Fatalf("%+v", r)
	}
	if f := readConfig(t, filepath.Join(dir, ".github", "pj-synker.yml")); f.Owner != "acme" {
		t.Fatal("--force didn't replace the file")
	}
}

func TestInitRefusesASecondConfigFile(t *testing.T) {
	dir := repoDir(t, ".pj-synker.yml", "project:\n  owner: old\n  number: 1\n")
	r := run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}}, dir), "init", "--owner", "acme", "--project", "2", "--force")
	if r.code != exitFailure || !strings.Contains(r.stderr, ".pj-synker.yml is already a config file") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "pj-synker.yml")); err == nil {
		t.Fatal("a second config file was written")
	}
}

func TestInitExplicitDefaultPathStillChecksForCompetingConfigs(t *testing.T) {
	dir := repoDir(t, ".pj-synker.yml", "project:\n  owner: old\n  number: 1\n")
	e := inDir(env{noAPI: true, vars: map[string]string{}}, dir)
	r := run(t, nil, e, "init", "--owner", "acme", "--project", "2", "--path", ".github/pj-synker.yml")
	if r.code != exitFailure || !strings.Contains(r.stderr, ".pj-synker.yml is already a config file") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "pj-synker.yml")); err == nil {
		t.Fatal("a second config file was written")
	}
	// A custom name is never discovered, so it can sit beside an existing config.
	if r := run(t, nil, e, "init", "--owner", "acme", "--project", "2", "--path", "ci/pj.yml"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestInitWithANumericLookingOwner(t *testing.T) {
	dir := repoDir(t, "", "")
	r := run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}}, dir), "init", "--owner", "1e3", "--project", "1")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if f := readConfig(t, filepath.Join(dir, ".github", "pj-synker.yml")); f.Owner != "1e3" {
		t.Fatalf("%+v", f)
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	dir := repoDir(t, "", "")
	r := run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}}, dir), "init", "--owner", "acme", "--project", "2", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "  owner: acme\n") || !strings.Contains(r.stderr, "Dry run.") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github")); err == nil {
		t.Fatal("dry run wrote files")
	}
	r = run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}}, dir), "init", "--owner", "acme", "--project", "2", "--dry-run", "--json")
	doc := decodeJSON(t, r.stdout)
	if doc["outcome"] != "planned" || !strings.Contains(doc["content"].(string), "number: 2") {
		t.Fatalf("%v", doc)
	}
}

func TestInitNonInteractiveNeedsTheProject(t *testing.T) {
	dir := repoDir(t, "", "")
	r := run(t, nil, inDir(ci(), dir), "init", "--json")
	if r.code != exitUsage || r.stdout != "" {
		t.Fatalf("%+v", r)
	}
	doc := decodeJSON(t, r.stderr)
	if doc["reason"] != "missing_input" || !strings.Contains(doc["message"].(string), "--owner and --project") {
		t.Fatalf("%v", doc)
	}
	r = run(t, nil, inDir(ci(), dir), "init", "--owner", "acme")
	if r.code != exitUsage || !strings.Contains(r.stderr, "without --project in non-interactive mode") {
		t.Fatalf("%+v", r)
	}
	r = run(t, nil, inDir(ci(), dir), "init", "--owner", "a b", "--project", "0", "--type", "x")
	if r.code != exitUsage || !strings.Contains(r.stderr, "Can't create the config file. 3 inputs are invalid.") {
		t.Fatalf("%+v", r)
	}
}

func TestInitPrompts(t *testing.T) {
	dir := repoDir(t, "", "")
	e := inDir(noColorTTY(), dir)
	e.noAPI = true
	e.repo = "acme/web"
	// Enter keeps the default owner; an invalid number is asked again.
	e.stdin = "\nzero\n4\n"
	r := run(t, nil, e, "init", "--label", "triaged")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stderr, "? Project owner? (default: acme) ") ||
		!strings.Contains(r.stderr, `"zero" isn't a project number.`) ||
		!strings.Contains(r.stderr, "    gh pj-synker init --owner acme --project 4 --label triaged\n") {
		t.Fatalf("%s", r.stderr)
	}
	if f := readConfig(t, filepath.Join(dir, ".github", "pj-synker.yml")); f.Owner != "acme" || f.Number != 4 {
		t.Fatalf("%+v", f)
	}

	// EOF cancels and writes nothing.
	dir = repoDir(t, "", "")
	e = inDir(noColorTTY(), dir)
	e.noAPI, e.stdin = true, ""
	r = run(t, nil, e, "init")
	if r.code != exitCanceled || !strings.Contains(r.stderr, "Canceled. Nothing was written.") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github")); err == nil {
		t.Fatal("a canceled init wrote files")
	}
}

func TestInitPath(t *testing.T) {
	dir := repoDir(t, "", "")
	e := inDir(env{noAPI: true, vars: map[string]string{}}, dir)
	r := run(t, nil, e, "init", "--owner", "acme", "--project", "1", "--path", "ci/pj.yml")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if f := readConfig(t, filepath.Join(dir, "ci", "pj.yml")); f.Owner != "acme" {
		t.Fatalf("%+v", f)
	}
	r = run(t, nil, e, "init", "--owner", "acme", "--project", "1", "--path", "ci")
	if r.code != exitUsage || !strings.Contains(r.stderr, "It's a directory.") {
		t.Fatalf("%+v", r)
	}
}

func TestInitGoldens(t *testing.T) {
	dir := repoDir(t, "", "")
	e := inDir(noColorTTY(), dir)
	e.noAPI = true
	golden(t, "init_created", slash(transcript(run(t, nil, e, "init", "--owner", "acme", "--project", "1", "--label", "triaged"))))
	golden(t, "init_exists", slash(transcript(run(t, nil, e, "init", "--owner", "acme", "--project", "1"))))
	e.cwd = repoDir(t, "", "")
	golden(t, "init_dry_run", slash(transcript(run(t, nil, e, "init", "--owner", "acme", "--project", "1", "--repo", "acme/web",
		"--exclude-label", "blocked", "--dry-run"))))
}
