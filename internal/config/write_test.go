package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRenderRoundTrips(t *testing.T) {
	yes, no := true, false
	in := File{
		Owner: "1e3", Number: 7, Repositories: []string{"acme/web", "acme/api"},
		Types: []string{"issue"}, States: []string{"open", "closed"},
		Labels:        []string{"triaged", "good first issue", "a, b", "yes", "1e3", "0x1f", "12", "2024-01-02", "type: bug", `say "hi"`, "#1"},
		ExcludeLabels: []string{}, Authors: []string{"alice"}, Drafts: &yes, Bots: &no,
	}
	out, err := Parse(Render(in), "x.yml")
	if err != nil {
		t.Fatalf("%v\n%s", err, Render(in))
	}
	in.Path, in.Version = "x.yml", 1
	in.Labels = []string{"triaged", "good first issue", "a, b", "yes", "1e3", "0x1f", "12", "2024-01-02", "type: bug", `say "hi"`, "#1"}
	if !reflect.DeepEqual(*out, in) {
		t.Fatalf("round trip:\n got  %+v\n want %+v\n%s", *out, in, Render(in))
	}
}

func TestRenderMinimal(t *testing.T) {
	src := string(Render(File{Owner: "acme", Number: 1}))
	if !strings.Contains(src, "project:\n  owner: acme\n  number: 1\n") || !strings.Contains(src, "# filters:") {
		t.Fatal(src)
	}
	f, err := Parse([]byte(src), "x.yml")
	if err != nil || f.Labels != nil || f.Repositories != nil {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".github", "pj-synker.yml")
	if err := WriteFile(path, []byte("a"), false); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("b"), false); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "a" {
		t.Fatalf("an existing file was changed: %q", b)
	}
	if err := WriteFile(path, []byte("c"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "c" {
		t.Fatalf("%q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files were left behind: %v", entries)
	}
}

func TestCheckoutRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CheckoutRoot(sub); got != root {
		t.Fatalf("%q", got)
	}
}
