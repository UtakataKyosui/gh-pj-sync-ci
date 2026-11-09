// Package e2e runs the built gh-pj-synker binary against a fake GitHub API.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gh-pj-synker-e2e")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "gh-pj-synker")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "..")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("build failed: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type out struct {
	code           int
	stdout, stderr string
}

// command prepares the binary with a clean environment: no inherited tokens or config.
func command(apiURL string, args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Dir = os.TempDir() // not a git checkout
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"GH_CONFIG_DIR=" + filepath.Join(os.TempDir(), "gh-pj-synker-e2e-no-config"),
		"CI=true",
		"GITHUB_REPOSITORY=acme/web",
	}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"), "USERPROFILE="+os.TempDir())
	}
	if apiURL != "" {
		cmd.Env = append(cmd.Env, "__GH_PJ_SYNKER_API_URL="+apiURL)
	}
	return cmd
}

func runCmd(t *testing.T, cmd *exec.Cmd) out {
	t.Helper()
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out{code, so.String(), se.String()}
}

func run(t *testing.T, apiURL string, args ...string) out {
	t.Helper()
	return runCmd(t, command(apiURL, args...))
}

var project = []string{"--owner", "acme", "--project", "1"}

func add(extra ...string) []string { return append(append([]string{"add"}, extra...), project...) }

func TestHelpWorksOfflineWithoutCredentials(t *testing.T) {
	for _, a := range [][]string{{"--help"}, {"add", "--help"}, {"help", "add"}, {"--version"}} {
		r := run(t, "", a...)
		if r.code != 0 || r.stdout == "" || r.stderr != "" {
			t.Errorf("%v: %+v", a, r)
		}
	}
}

func TestNoCredentials(t *testing.T) {
	r := run(t, "", add("1", "--json")...)
	if r.code != 1 || r.stdout != "" {
		t.Fatalf("%+v", r)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.stderr), &doc); err != nil || doc["reason"] != "login_required" {
		t.Fatalf("%q %v", r.stderr, err)
	}
}

func TestUsageErrorExitsTwo(t *testing.T) {
	r := run(t, "", "add", "--lable", "x")
	if r.code != 2 || !strings.Contains(r.stderr, "Did you mean --label?") {
		t.Fatalf("%+v", r)
	}
}

func TestAddIsIdempotent(t *testing.T) {
	gh, srv := newFakeGitHub(t)
	gh.setIssue(fakeIssue{Number: 5, Title: "Fix login", State: "open", Author: "octocat", Labels: []string{"triaged"}})

	r := run(t, srv.URL, add("5", "--json")...)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	var doc struct {
		Results []struct {
			Outcome       string `json:"outcome"`
			ProjectItemID string `json:"projectItemId"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Results[0].Outcome != "added" || doc.Results[0].ProjectItemID != "PVTI_I_5" {
		t.Fatalf("%+v", doc)
	}

	// The second run reports the existing item and adds nothing.
	r = run(t, srv.URL, add("5")...)
	if r.code != 0 || r.stdout != "PVTI_I_5\n" || !strings.Contains(r.stderr, "acme/web#5 is already in acme/1.") {
		t.Fatalf("%+v", r)
	}
	if gh.addCount() != 1 {
		t.Fatalf("adds = %d", gh.addCount())
	}
	for _, a := range gh.authHeaders() {
		if a != "token test-token" {
			t.Fatalf("Authorization header %q", a)
		}
	}
}

func TestAllInCI(t *testing.T) {
	gh, srv := newFakeGitHub(t)
	gh.setIssue(fakeIssue{Number: 1, Title: "One", State: "open", Author: "a", Labels: []string{"triaged"}})
	gh.setIssue(fakeIssue{Number: 2, Title: "Two", State: "open", Author: "a", PR: true, Draft: true, Labels: []string{"triaged"}})
	gh.setIssue(fakeIssue{Number: 3, Title: "Three", State: "open", Author: "renovate[bot]", Bot: true, Labels: []string{"triaged"}})
	gh.setIssue(fakeIssue{Number: 4, Title: "Four", State: "closed", Author: "a", Labels: []string{"triaged"}})
	gh.setIssue(fakeIssue{Number: 5, Title: "Five", State: "open", Author: "a", PR: true, Labels: []string{"Triaged"}})

	// Without --yes nothing is added.
	r := run(t, srv.URL, add("--all", "--label", "triaged")...)
	if r.code != 2 || gh.addCount() != 0 || !strings.Contains(r.stderr, "Error: Can't add 2 items found with --all without confirmation") {
		t.Fatalf("%+v", r)
	}
	// A dry run plans the same items.
	r = run(t, srv.URL, add("--all", "--label", "triaged", "--dry-run", "--json")...)
	if r.code != 0 || gh.addCount() != 0 || !strings.Contains(r.stdout, `"planned":2`) {
		t.Fatalf("%+v", r)
	}
	r = run(t, srv.URL, add("--all", "--label", "triaged", "--yes")...)
	if r.code != 0 || gh.addCount() != 2 || r.stdout != "PVTI_I_1\nPVTI_PR_5\n" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stderr, "\nAdded 2 items\n") || strings.Contains(r.stderr, "\x1b[") {
		t.Fatalf("CI output must be plain:\n%s", r.stderr)
	}
}

func TestErrors(t *testing.T) {
	gh, srv := newFakeGitHub(t)
	r := run(t, srv.URL, add("9")...)
	if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "Error: Couldn't find issue or pull request 9 in acme/web.") {
		t.Fatalf("%+v", r)
	}
	r = run(t, srv.URL, "add", "9", "--owner", "acme", "--project", "7")
	if r.code != 1 || !strings.Contains(r.stderr, "Couldn't find project 7 owned by acme.") {
		t.Fatalf("%+v", r)
	}
	gh.setClosed(true)
	r = run(t, srv.URL, add("9")...)
	if r.code != 1 || !strings.Contains(r.stderr, "The project is closed.") {
		t.Fatalf("%+v", r)
	}
}

func TestInterruptExits130(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt can't be sent to a process on Windows")
	}
	gh, srv := newFakeGitHub(t)
	gh.setBlocking()
	cmd := command(srv.URL, add("1")...)
	var se bytes.Buffer
	cmd.Stderr = &se
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gh.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the CLI never called the API")
	}
	_ = cmd.Process.Signal(os.Interrupt)
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 {
		t.Fatalf("err=%v stderr=%s", err, se.String())
	}
	if !strings.Contains(se.String(), "Canceled. Nothing was changed.") {
		t.Fatalf("stderr=%s", se.String())
	}
}

func TestConfigFile(t *testing.T) {
	gh, srv := newFakeGitHub(t)
	gh.setIssue(fakeIssue{Number: 1, Title: "One", State: "open", Author: "a", Labels: []string{"triaged"}})
	gh.setIssue(fakeIssue{Number: 2, Title: "Two", State: "open", Author: "a"})

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, ".github", "pj-synker.yml")
	write := func(content string) {
		if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("project:\n  owner: acme\n  number: 1\nrepositories: [acme/web]\nfilters:\n  labels: [triaged]\n")

	// The config file supplies --owner, --project and the filters.
	cmd := command(srv.URL, "add", "--all", "--yes")
	cmd.Dir = filepath.Join(dir, ".github") // found from a subdirectory too
	r := runCmd(t, cmd)
	if r.code != 0 || r.stdout != "PVTI_I_1\n" || gh.addCount() != 1 {
		t.Fatalf("%+v", r)
	}

	cmd = command("", "config", "check", "--json")
	cmd.Dir = dir
	r = runCmd(t, cmd)
	if r.code != 0 || !strings.Contains(r.stdout, `"project.owner":"acme"`) {
		t.Fatalf("%+v", r)
	}

	// A broken file fails with its location, before any request.
	write("project:\n  owner: acme\n  numbr: 1\n")
	before := len(gh.authHeaders())
	cmd = command(srv.URL, "add", "1", "--owner", "acme", "--project", "1")
	cmd.Dir = dir
	r = runCmd(t, cmd)
	if r.code != 1 || !strings.Contains(r.stderr, "pj-synker.yml:3:3 \"project.numbr\" isn't a setting. Did you mean \"project.number\"?") {
		t.Fatalf("%+v", r)
	}
	if len(gh.authHeaders()) != before {
		t.Fatal("a request was made with a broken config file")
	}
}
