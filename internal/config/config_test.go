package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const full = `# Settings for gh pj-synker
version: 1
project:
  owner: acme
  number: 3
repositories:
  - acme/web
  - acme/api
filters:
  types: [Issue]
  states: [open, closed]
  labels: [Triaged, accepted]
  exclude-labels: [blocked]
  authors: [Alice]
  drafts: true
  bots: false
`

func TestParseFull(t *testing.T) {
	f, err := Parse([]byte(full), "x.yml")
	if err != nil {
		t.Fatal(err)
	}
	if f.Owner != "acme" || f.Number != 3 || f.Version != 1 {
		t.Fatalf("%+v", f)
	}
	if !reflect.DeepEqual(f.Repositories, []string{"acme/web", "acme/api"}) ||
		!reflect.DeepEqual(f.Types, []string{"issue"}) ||
		!reflect.DeepEqual(f.Labels, []string{"triaged", "accepted"}) ||
		!reflect.DeepEqual(f.Authors, []string{"alice"}) {
		t.Fatalf("%+v", f)
	}
	if f.Drafts == nil || !*f.Drafts || f.Bots == nil || *f.Bots {
		t.Fatalf("bools %v %v", f.Drafts, f.Bots)
	}
}

func TestParseEmptyAndPartial(t *testing.T) {
	for _, src := range []string{"", "# nothing yet\n", "project:\n  owner: acme\n"} {
		f, err := Parse([]byte(src), "x.yml")
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if f.Types != nil || f.Drafts != nil || f.Repositories != nil {
			t.Fatalf("unset settings must stay nil: %+v", f)
		}
	}
	// An explicitly empty list is set, and means "none".
	f, err := Parse([]byte("filters:\n  labels: []\n"), "x.yml")
	if err != nil || f.Labels == nil || len(f.Labels) != 0 {
		t.Fatalf("%+v %v", f, err)
	}
}

func issues(t *testing.T, src string) []Issue {
	t.Helper()
	_, err := Parse([]byte(src), "x.yml")
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("%q: expected a config error, got %v", src, err)
	}
	return cerr.Issues
}

func TestParseIssues(t *testing.T) {
	tests := []struct {
		src  string
		want []Issue
	}{
		{"project:\n  ownr: acme\n", []Issue{{2, 3, `"project.ownr" isn't a setting. Did you mean "project.owner"?`}}},
		{"projects:\n  owner: acme\n", []Issue{{1, 1, `"projects" isn't a setting. Did you mean "project"?`}}},
		{"project:\n  owner: a b\n", []Issue{{2, 10, `"a b" isn't a valid user or organization login.`}}},
		{"project:\n  number: one\n", []Issue{{2, 11, `"project.number" must be a positive whole number.`}}},
		{"project:\n  number: 0\n", []Issue{{2, 11, `"project.number" must be a positive whole number.`}}},
		{"project: acme\n", []Issue{{1, 10, `"project" must be a mapping of settings.`}}},
		{"version: 2\n", []Issue{{1, 10, "version 2 isn't supported. This build reads version 1."}}},
		{"repositories: acme/web\n", []Issue{{1, 15, `"repositories" must be a list, such as [a, b].`}}},
		{"repositories: [acme/web, nope]\n", []Issue{{1, 26, `"nope" must look like OWNER/REPO.`}}},
		{"filters:\n  types: [discussion]\n", []Issue{{2, 11, `"discussion" isn't a type. Use issue or pull-request.`}}},
		{"filters:\n  states: [merged]\n", []Issue{{2, 12, `"merged" isn't a state. Use open or closed.`}}},
		{"filters:\n  drafts: yes\n", []Issue{{2, 11, `"filters.drafts" must be true or false.`}}},
		{"filters:\n  labels: [a, ~]\n", []Issue{{2, 15, "filters.labels[1] must be a non-empty string."}}},
		{"project:\n  owner: a\n  owner: b\n", []Issue{{3, 3, `"project.owner" is set more than once.`}}},
		{"project:\n  owner: acme\n---\nfilters:\n  drafts: true\n", []Issue{{4, 1, `Only one YAML document is allowed. Remove the extra "---" section or merge it into the first.`}}},
		{"- a\n- b\n", []Issue{{1, 1, `The file must be a mapping of settings, such as "project:".`}}},
		{"project:\n  ownr: x\nfilters:\n  drafts: 1\n", []Issue{
			{2, 3, `"project.ownr" isn't a setting. Did you mean "project.owner"?`},
			{4, 11, `"filters.drafts" must be true or false.`},
		}},
	}
	for _, tt := range tests {
		if got := issues(t, tt.src); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q:\n got  %+v\n want %+v", tt.src, got, tt.want)
		}
	}
}

func TestParseAllowsADocumentMarkerWithoutASecondDocument(t *testing.T) {
	for _, src := range []string{"---\nproject:\n  owner: acme\n", "project:\n  owner: acme\n---\n", "project:\n  owner: acme\n...\n"} {
		if _, err := Parse([]byte(src), "x.yml"); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestParseSyntaxError(t *testing.T) {
	_, err := Parse([]byte("project:\n  owner: [acme\n"), "x.yml")
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Syntax == "" || cerr.Line == 0 {
		t.Fatalf("%#v", err)
	}
}

func TestAliasesResolve(t *testing.T) {
	f, err := Parse([]byte("x: &labels [triaged]\n"), "x.yml")
	if err == nil {
		t.Fatalf("unknown key accepted: %+v", f)
	}
	f, err = Parse([]byte("filters:\n  labels: &l [triaged]\n  exclude-labels: *l\n"), "x.yml")
	if err != nil || !reflect.DeepEqual(f.ExcludeLabels, []string{"triaged"}) {
		t.Fatalf("%+v %v", f, err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: x")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if p, err := Discover(sub); err != nil || p != "" {
		t.Fatalf("no config: %q %v", p, err)
	}
	cfg := filepath.Join(root, ".github", "pj-synker.yml")
	write(t, cfg, "")
	if p, err := Discover(sub); err != nil || p != cfg {
		t.Fatalf("found %q %v, want %q", p, err, cfg)
	}
	// A nearer file wins.
	near := filepath.Join(root, "a", ".pj-synker.yml")
	write(t, near, "")
	if p, _ := Discover(sub); p != near {
		t.Fatalf("found %q, want %q", p, near)
	}
	// Two files in one directory are never guessed between.
	write(t, filepath.Join(root, "a", ".pj-synker.yaml"), "")
	var amb *ErrAmbiguous
	if _, err := Discover(sub); !errors.As(err, &amb) || len(amb.Paths) != 2 {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverStopsAtTheCheckout(t *testing.T) {
	outer := t.TempDir()
	write(t, filepath.Join(outer, ".pj-synker.yml"), "")
	repo := filepath.Join(outer, "repo")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: x")
	if p, err := Discover(repo); err != nil || p != "" {
		t.Fatalf("a file outside the checkout was used: %q %v", p, err)
	}
	// Outside any checkout only the directory itself is searched.
	plain := filepath.Join(t.TempDir(), "x")
	write(t, filepath.Join(filepath.Dir(plain), ".pj-synker.yml"), "")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, _ := Discover(plain); p != "" {
		t.Fatalf("searched above a non-git directory: %q", p)
	}
}

func TestErrorMessage(t *testing.T) {
	_, err := Parse([]byte("x: 1\n"), "cfg.yml")
	if !strings.Contains(err.Error(), "cfg.yml") {
		t.Fatal(err)
	}
}

func TestDiscoverReportsUnreadableCandidates(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink that points at itself can't be stat'ed. It must not read as "no config".
	if err := os.Symlink(".pj-synker.yml", filepath.Join(dir, ".pj-synker.yml")); err != nil {
		t.Skipf("symlinks aren't available: %v", err)
	}
	if found, err := Discover(dir); err == nil || found != "" {
		t.Fatalf("found %q, err %v", found, err)
	}
}

func TestDiscoverIgnoresMissingCandidates(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if found, err := Discover(dir); err != nil || found != "" {
		t.Fatalf("found %q, err %v", found, err)
	}
}
