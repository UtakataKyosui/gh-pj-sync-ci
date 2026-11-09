package cli

import (
	"regexp"
	"strings"
	"testing"
)

func TestResolveMode(t *testing.T) {
	ttyStreams := func(vars map[string]string) Streams {
		return Streams{InTTY: true, OutTTY: true, ErrTTY: true, Getenv: func(k string) string { return vars[k] }}
	}
	tests := []struct {
		name        string
		s           Streams
		g           globalOptions
		json        bool
		interactive bool
		errRich     bool
		errColor    bool
	}{
		{"tty", ttyStreams(nil), globalOptions{}, false, true, true, true},
		{"CI forces non-interactive", ttyStreams(map[string]string{"CI": "true"}), globalOptions{}, false, false, true, true},
		{"--json is non-interactive", ttyStreams(nil), globalOptions{}, true, false, true, true},
		{"--non-interactive", ttyStreams(nil), globalOptions{nonInteractive: true}, false, false, true, true},
		{"stdin piped", Streams{OutTTY: true, ErrTTY: true, Getenv: func(string) string { return "" }}, globalOptions{}, false, false, true, true},
		{"NO_COLOR keeps glyphs", ttyStreams(map[string]string{"NO_COLOR": "1"}), globalOptions{}, false, true, true, false},
		{"--no-color", ttyStreams(nil), globalOptions{noColor: true}, false, true, true, false},
		{"--no-color beats FORCE_COLOR", ttyStreams(map[string]string{"FORCE_COLOR": "1"}), globalOptions{noColor: true}, false, true, true, false},
		{"FORCE_COLOR=0", ttyStreams(map[string]string{"FORCE_COLOR": "0"}), globalOptions{}, false, true, true, false},
		{"TERM=dumb is plain", ttyStreams(map[string]string{"TERM": "dumb"}), globalOptions{}, false, true, false, false},
		{"pipe is plain", Streams{Getenv: func(string) string { return "" }}, globalOptions{}, false, false, false, false},
		{"pipe with FORCE_COLOR", Streams{Getenv: func(k string) string { return map[string]string{"FORCE_COLOR": "1"}[k] }}, globalOptions{}, false, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := resolveMode(tt.s, tt.g, tt.json)
			if m.Interactive != tt.interactive || m.Err.Rich != tt.errRich || m.Err.Color != tt.errColor {
				t.Fatalf("got %+v", m)
			}
			if m.Width != 80 {
				t.Fatalf("default width %d", m.Width)
			}
		})
	}
}

func TestStrippingColorRecoversText(t *testing.T) {
	f := newFake()
	f.put("acme/web", 5, nil)
	colored := tty()
	colored.vars["FORCE_COLOR"] = "1"
	plainColor := tty()
	plainColor.vars["NO_COLOR"] = "1"
	a := run(t, f, colored, "add", "5", "--owner", "acme", "--project", "1", "--dry-run")
	b := run(t, f, plainColor, "add", "5", "--owner", "acme", "--project", "1", "--dry-run")
	if !colorRe.MatchString(a.stderr) {
		t.Fatal("expected color")
	}
	if colorRe.MatchString(b.stderr) {
		t.Fatal("NO_COLOR printed color")
	}
	if colorRe.ReplaceAllString(a.stderr, "") != b.stderr {
		t.Fatalf("stripped output differs:\n%s\n---\n%s", colorRe.ReplaceAllString(a.stderr, ""), b.stderr)
	}
}

var colorRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestPlainModeUsesWords(t *testing.T) {
	f := newFake()
	f.put("acme/web", 5, nil)
	r := run(t, f, ci(), "add", "5", "--owner", "acme", "--project", "1")
	if !strings.Contains(r.stderr, "\nAdded acme/web#5\n") || strings.ContainsAny(r.stderr, "✓✗") {
		t.Fatalf("stderr:\n%s", r.stderr)
	}
	r = run(t, f, ci(), "add", "99", "--owner", "acme", "--project", "1")
	if !strings.Contains(r.stderr, "Error: Couldn't find issue or pull request 99 in acme/web.") {
		t.Fatalf("stderr:\n%s", r.stderr)
	}
}

func TestSanitize(t *testing.T) {
	in := "Fix \x1b[31mred\x1b[0m\u202e bidi\u200b zero\nline\tTab\x07"
	if got := sanitize(in); got != `Fix [31mred[0m bidi zero\nline\tTab` {
		t.Fatalf("%q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdefghij", 5); got != "abcd…" {
		t.Fatal(got)
	}
	if got := truncate("日本語のタイトル", 7); got != "日本語…" {
		t.Fatal(got)
	}
	if got := truncate("short", 10); got != "short" {
		t.Fatal(got)
	}
}

func TestPlural(t *testing.T) {
	if plural(1, "item", "items") != "1 item" || plural(0, "item", "items") != "0 items" || plural(1200, "item", "items") != "1,200 items" {
		t.Fatal("plural")
	}
}
