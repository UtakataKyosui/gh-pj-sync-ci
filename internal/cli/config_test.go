package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoDir makes a git checkout in a temp dir with an optional config file.
func repoDir(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// slash makes paths in output read the same on every OS, for goldens.
func slash(s string) string {
	if filepath.Separator == '/' {
		return s
	}
	return strings.ReplaceAll(s, string(filepath.Separator), "/")
}

const validConfig = `version: 1
project:
  owner: acme
  number: 1
filters:
  labels: [triaged]
  exclude-labels: [blocked]
`

func inDir(e env, dir string) env {
	e.cwd = dir
	return e
}

func TestConfigSuppliesRequiredSettings(t *testing.T) {
	dir := repoDir(t, ".github/pj-synker.yml", validConfig)
	f := mixedFake()
	r := run(t, f, inDir(noColorTTY(), dir), "add", "--all", "--dry-run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stderr, "  Config          "+filepath.Join(".github", "pj-synker.yml")+"\n") {
		t.Fatalf("missing Config row:\n%s", r.stderr)
	}
	// The config file's filters were applied: 2 eligible, as with the same options.
	if !strings.Contains(r.stderr, "Would add       2 items") {
		t.Fatalf("%s", r.stderr)
	}
	// The next step repeats only what was given on the command line.
	if !strings.Contains(r.stderr, "    gh pj-synker add --all --yes\n") {
		t.Fatalf("%s", r.stderr)
	}
}

func TestOptionsOverrideTheConfig(t *testing.T) {
	dir := repoDir(t, ".pj-synker.yml", validConfig+"  drafts: true\n")
	f := mixedFake()
	r := run(t, f, inDir(ci(), dir), "add", "--all", "--dry-run", "--json", "--project", "1",
		"--label", "", "--no-drafts")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	doc := decodeJSON(t, r.stdout)
	// --label "" clears the config's labels; --no-drafts overrides drafts: true.
	// Eligible: 1, 5, 6 (no label needed); 2 is still excluded, 3 is a draft, 4 a bot, 7 unchanged.
	if doc["summary"].(map[string]any)["planned"] != float64(3) {
		t.Fatalf("%v", doc["summary"])
	}
	if doc["config"] != ".pj-synker.yml" {
		t.Fatalf("config = %v", doc["config"])
	}
}

func TestConfigRepositoriesComeAfterEnvironment(t *testing.T) {
	dir := repoDir(t, ".pj-synker.yml", validConfig+"repositories: [acme/api]\n")
	f := newFake()
	f.put("acme/api", 1, nil)
	f.put("acme/env", 1, nil)
	r := run(t, f, inDir(ci(), dir), "add", "1")
	if r.code != 0 || !strings.Contains(r.stderr, "Repository      acme/api\n  Source          .pj-synker.yml\n") {
		t.Fatalf("%+v", r)
	}
	e := inDir(ci(), dir)
	e.vars["GH_REPO"] = "acme/env"
	r = run(t, f, e, "add", "1")
	if r.code != 0 || !strings.Contains(r.stderr, "Repository      acme/env\n") {
		t.Fatalf("%+v", r)
	}
}

func TestNoConfigAndExplicitConfig(t *testing.T) {
	dir := repoDir(t, ".pj-synker.yml", validConfig)
	other := filepath.Join(dir, "ci", "other.yml")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("project:\n  owner: other\n  number: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.put("acme/web", 1, nil)

	r := run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}, repo: "acme/web"}, dir), "add", "1", "--no-config")
	if r.code != exitUsage || !strings.Contains(r.stderr, "4 inputs are invalid") && !strings.Contains(r.stderr, "2 inputs are invalid") {
		t.Fatalf("--no-config must ignore the file: %+v", r)
	}
	r = run(t, f, inDir(ci(), dir), "add", "1", "--config", filepath.Join("ci", "other.yml"))
	if r.code != 0 || !strings.Contains(r.stderr, "Project         other/9") {
		t.Fatalf("%+v", r)
	}
	e := inDir(ci(), dir)
	e.vars["GH_PJ_SYNKER_CONFIG"] = other
	r = run(t, f, e, "add", "1")
	if r.code != 0 || !strings.Contains(r.stderr, "Project         other/9") {
		t.Fatalf("%+v", r)
	}
	r = run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}}, dir), "add", "1", "--config", "missing.yml")
	if r.code != exitUsage || !strings.Contains(r.stderr, "Can't read config file missing.yml. It doesn't exist.") {
		t.Fatalf("%+v", r)
	}
	r = run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}}, dir), "add", "1", "--config", "x.yml", "--no-config")
	if r.code != exitUsage || !strings.Contains(r.stderr, "Can't use --config with --no-config.") {
		t.Fatalf("%+v", r)
	}
}

func TestBrokenConfigIsNeverIgnored(t *testing.T) {
	dir := repoDir(t, ".github/pj-synker.yml", "project:\n  ownr: acme\n")
	// Even with every required option given, a broken file fails before any request.
	r := run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}, repo: "acme/web"}, dir),
		"add", "1", "--owner", "acme", "--project", "1", "--json")
	if r.code != exitFailure || r.stdout != "" {
		t.Fatalf("%+v", r)
	}
	doc := decodeJSON(t, r.stderr)
	data := doc["data"].(map[string]any)
	issue := data["issues"].([]any)[0].(map[string]any)
	if doc["reason"] != "invalid_config" || issue["line"] != float64(2) || issue["column"] != float64(3) {
		t.Fatalf("%v", doc)
	}
	if !strings.Contains(doc["next"].([]any)[0].(map[string]any)["command"].(string), "gh pj-synker config check") {
		t.Fatalf("%v", doc["next"])
	}
}

func TestAmbiguousConfig(t *testing.T) {
	dir := repoDir(t, ".pj-synker.yml", validConfig)
	if err := os.WriteFile(filepath.Join(dir, ".pj-synker.yaml"), []byte(validConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, nil, inDir(env{noAPI: true, vars: map[string]string{}}, dir), "add", "1")
	if r.code != exitUsage || !strings.Contains(r.stderr, "Can't choose a config file. Found .pj-synker.yml and .pj-synker.yaml.") {
		t.Fatalf("%+v", r)
	}
}

func TestConfigCommandRouting(t *testing.T) {
	r := run(t, nil, env{noAPI: true}, "config")
	if r.code != 0 || !strings.Contains(r.stdout, "Commands:\n  check  Validate the config file") {
		t.Fatalf("a group without an action shows help: %+v", r)
	}
	r = run(t, nil, env{noAPI: true}, "config", "chek")
	if r.code != exitUsage || !strings.Contains(r.stderr, "Unknown command chek for gh pj-synker config. Did you mean check?") {
		t.Fatalf("%+v", r)
	}
	for _, a := range [][]string{{"help", "config", "check"}, {"config", "check", "--help"}, {"config", "--help"}} {
		r = run(t, nil, env{noAPI: true}, a...)
		if r.code != 0 || !strings.HasPrefix(r.stdout, "gh pj-synker config") {
			t.Fatalf("%v: %+v", a, r)
		}
	}
	r = run(t, nil, env{noAPI: true}, "config", "check", "extra")
	if r.code != exitUsage {
		t.Fatalf("%+v", r)
	}
}

func TestConfigCheckJSON(t *testing.T) {
	dir := repoDir(t, ".github/pj-synker.yml", validConfig)
	r := run(t, nil, inDir(env{vars: map[string]string{}}, dir), "config", "check", "--json")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	c := decodeJSON(t, r.stdout)["config"].(map[string]any)
	settings := c["settings"].(map[string]any)
	if settings["project.owner"] != "acme" || settings["project.number"] != float64(1) {
		t.Fatalf("%v", settings)
	}
	defaults := strings.Join(toStrings(c["defaults"].([]any)), ",")
	if !strings.Contains(defaults, "filters.types") || strings.Contains(defaults, "filters.labels") {
		t.Fatalf("defaults %s", defaults)
	}
}

func toStrings(v []any) []string {
	out := make([]string, len(v))
	for i, x := range v {
		out[i] = x.(string)
	}
	return out
}

func TestConfigGoldens(t *testing.T) {
	tests := []struct {
		name, file, content string
		args                []string
	}{
		{"config_check_valid", ".github/pj-synker.yml", validConfig, []string{"config", "check"}},
		{"config_check_partial", ".pj-synker.yml", "filters:\n  bots: true\n", []string{"config", "check"}},
		{"config_check_missing", "", "", []string{"config", "check"}},
		{"config_unknown_key", ".github/pj-synker.yml", "version: 1\nproject:\n  ownr: acme\n  number: 1\n", []string{"add", "1"}},
		{"config_many_issues", ".github/pj-synker.yml",
			"project:\n  owner: a b\n  number: zero\nfilters:\n  types: [discussion]\n  drafts: yes\n", []string{"config", "check"}},
		{"config_syntax_error", ".github/pj-synker.yml", "project:\n  owner: [acme\n  number: 1\n", []string{"add", "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := repoDir(t, tt.file, tt.content)
			e := inDir(noColorTTY(), dir)
			e.noAPI = tt.args[0] != "config"
			golden(t, tt.name, slash(transcript(run(t, nil, e, tt.args...))))
		})
	}
}
