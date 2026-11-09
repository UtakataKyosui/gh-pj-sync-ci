package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// screen applies the line redraws a terminal would, so goldens show what a person sees.
func screen(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "\r\x1b[K")
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		cur := b.String()
		b.Reset()
		b.WriteString(cur[:strings.LastIndex(cur, "\n")+1])
		s = s[i+len("\r\x1b[K"):]
	}
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/cli -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs (run go test ./internal/cli -update to accept)\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

func transcript(r result) string {
	return "$ exit " + itoa(r.code) + "\n--- stdout\n" + r.stdout + "--- stderr\n" + screen(r.stderr)
}

// noColorTTY is an interactive terminal with glyphs but no color, for readable goldens.
func noColorTTY() env {
	e := tty()
	e.vars["NO_COLOR"] = "1"
	return e
}

func mixedFake() *fakeAPI {
	f := newFake()
	f.put("acme/web", 1, nil)
	f.put("acme/web", 2, func(i *ghapiItem) { i.Labels = []string{"blocked", "triaged"} })
	f.put("acme/web", 3, func(i *ghapiItem) { i.Type, i.Draft = "pull-request", true })
	f.put("acme/web", 4, func(i *ghapiItem) { i.Author, i.IsBot = "dependabot[bot]", true })
	f.put("acme/web", 5, func(i *ghapiItem) {
		i.Title = "An extremely long title that will not fit on one line of an eighty column terminal"
	})
	f.put("acme/web", 6, func(i *ghapiItem) { i.Labels = nil })
	f.put("acme/web", 7, nil)
	f.inProject["I_acme_web_7"] = "PVTI_existing"
	return f
}

func TestGoldenTranscripts(t *testing.T) {
	base := []string{"--owner", "acme", "--project", "1"}
	filters := []string{"--label", "triaged", "--exclude-label", "blocked"}
	tests := []struct {
		name string
		f    func() *fakeAPI
		env  func() env
		args []string
	}{
		{"help_root", nil, noColorTTY, []string{"--help"}},
		{"help_add", nil, noColorTTY, []string{"help", "add"}},
		{"add_one_tty", func() *fakeAPI { f := newFake(); f.put("acme/web", 5, nil); return f }, noColorTTY,
			append([]string{"add", "5"}, base...)},
		{"add_one_ci", func() *fakeAPI { f := newFake(); f.put("acme/web", 5, nil); return f }, ci,
			append([]string{"add", "5"}, base...)},
		{"add_one_unchanged", func() *fakeAPI {
			f := newFake()
			f.put("acme/web", 5, nil)
			f.inProject["I_acme_web_5"] = "PVTI_existing"
			return f
		}, noColorTTY, append([]string{"add", "5"}, base...)},
		{"add_one_skipped", func() *fakeAPI { f := newFake(); f.put("acme/web", 5, nil); return f }, noColorTTY,
			append(append([]string{"add", "5"}, base...), "--label", "accepted")},
		{"add_all_dry_run", mixedFake, noColorTTY, append(append([]string{"add", "--all", "--dry-run"}, base...), filters...)},
		{"add_all_yes_ci", mixedFake, ci, append(append([]string{"add", "--all", "--yes"}, base...), filters...)},
		{"add_all_needs_confirmation_ci", mixedFake, ci, append(append([]string{"add", "--all"}, base...), filters...)},
		{"add_all_empty", newFake, noColorTTY, append([]string{"add", "--all"}, base...)},
		{"add_not_found", newFake, noColorTTY, append([]string{"add", "404"}, base...)},
		{"add_partial_failure", func() *fakeAPI {
			f := mixedFake()
			f.itemErr[2] = &httpErrForTest{status: 502}
			return f
		}, noColorTTY, append(append([]string{"add", "--all", "--yes"}, base...), filters...)},
		{"add_invalid_inputs", nil, noColorTTY, []string{"add", "x", "--owner", "a b", "--type", "discussion"}},
		{"add_json", mixedFake, ci, append(append([]string{"add", "--all", "--yes", "--json"}, base...), filters...)},
		{"add_json_error", newFake, ci, append([]string{"add", "404", "--json"}, base...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f *fakeAPI
			if tt.f != nil {
				f = tt.f()
			}
			e := tt.env()
			if f == nil {
				e.noAPI = true
			}
			golden(t, tt.name, transcript(run(t, f, e, tt.args...)))
		})
	}
}
