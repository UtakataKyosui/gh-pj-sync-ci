package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/syncer"
)

type fake struct{ added []string }

func (f *fake) GetItem(_ context.Context, _ string, n int) (syncer.Item, error) {
	return syncer.Item{Number: n, ContentID: "C", Item: filter.Item{Type: "issue", State: "open", Author: "a"}}, nil
}
func (f *fake) ListItemNumbers(context.Context, string, []string) ([]int, error) {
	return []int{1, 2}, nil
}
func (f *fake) GetProject(context.Context, string, int) (syncer.Project, error) {
	return syncer.Project{ID: "P"}, nil
}
func (f *fake) AddItem(_ context.Context, _, c string) (string, error) {
	f.added = append(f.added, c)
	return "ITEM", nil
}

func exec(t *testing.T, f *fake, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	for _, k := range []string{"GH_REPO", "GITHUB_REPOSITORY", "GITHUB_EVENT_PATH", "GITHUB_OUTPUT"} {
		t.Setenv(k, env[k])
	}
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, func() (syncer.API, error) { return f, nil })
	return code, out.String(), errb.String()
}

func TestRunSingleIssue(t *testing.T) {
	f := &fake{}
	code, out, _ := exec(t, f, map[string]string{"GH_REPO": "o/r"}, "--project-owner", "x", "--project-number", "1", "--issue", "5")
	if code != 0 || !strings.Contains(out, "synced") || !strings.Contains(out, "o/r#5") || len(f.added) != 1 {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestRunAllDryRun(t *testing.T) {
	f := &fake{}
	code, out, _ := exec(t, f, map[string]string{"GH_REPO": "o/r"}, "--project-owner", "x", "--project-number", "1", "--all", "--dry-run", "--json")
	if code != 0 || strings.Count(out, `"result": "dry-run"`) != 2 || len(f.added) != 0 {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestRunValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--project-number", "1"},
		{"--project-owner", "x"},
		{"--project-owner", "x", "--project-number", "1", "--all", "--issue", "1"},
		{"--project-owner", "x", "--project-number", "1", "--issue", "1", "--types", "bogus"},
		{"--project-owner", "x", "--project-number", "1", "--issue", "1", "--repo", "bad"},
	} {
		if code, _, _ := exec(t, &fake{}, map[string]string{"GH_REPO": "o/r"}, args...); code != 2 {
			t.Fatalf("args %v: code=%d, want 2", args, code)
		}
	}
}

func TestRunFromActionsEvent(t *testing.T) {
	dir := t.TempDir()
	ev := filepath.Join(dir, "event.json")
	outFile := filepath.Join(dir, "output")
	os.WriteFile(ev, []byte(`{"pull_request":{"number":9}}`), 0o600)
	f := &fake{}
	env := map[string]string{"GITHUB_REPOSITORY": "o/r", "GITHUB_EVENT_PATH": ev, "GITHUB_OUTPUT": outFile}
	code, out, _ := exec(t, f, env, "--project-owner", "x", "--project-number", "1")
	if code != 0 || !strings.Contains(out, "o/r#9") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	data, _ := os.ReadFile(outFile)
	if !strings.Contains(string(data), "result=synced") || !strings.Contains(string(data), "item-id=ITEM") {
		t.Fatalf("outputs=%q", data)
	}

	os.WriteFile(ev, []byte(`{"schedule":"x"}`), 0o600)
	code, out, _ = exec(t, f, env, "--project-owner", "x", "--project-number", "1")
	if code != 0 || !strings.Contains(out, "skipped") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
