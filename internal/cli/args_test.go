package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseForms(t *testing.T) {
	opts := addCommand.allOptions()
	p, err := parse([]string{
		"7", "--owner=acme", "--project", "1", "-R", "a/b", "--repo=c/d", "-y",
		"--drafts=false", "--no-bots", "--label", "x,y", "--label", "z", "--", "--not-an-option",
	}, opts, "gh pj-synker add")
	if err != nil {
		t.Fatal(err)
	}
	if p.strs["owner"] != "acme" || p.strs["project"] != "1" || !p.bools["yes"] {
		t.Fatalf("%+v", p)
	}
	if !reflect.DeepEqual(p.lists["repo"], []string{"a/b", "c/d"}) || !reflect.DeepEqual(p.lists["label"], []string{"x,y", "z"}) {
		t.Fatalf("lists %v", p.lists)
	}
	if p.bools["drafts"] || !p.set["drafts"] || p.bools["bots"] || !p.set["bots"] {
		t.Fatalf("bools %v", p.bools)
	}
	if !reflect.DeepEqual(p.positionals, []string{"7", "--not-an-option"}) {
		t.Fatalf("positionals %v", p.positionals)
	}
}

func TestParseErrors(t *testing.T) {
	opts := addCommand.allOptions()
	tests := []struct {
		args    []string
		reason  string
		message string
	}{
		{[]string{"--lable", "x"}, "unknown_option", "Unknown option --lable for gh pj-synker add. Did you mean --label?"},
		{[]string{"--frobnicate"}, "unknown_option", "Unknown option --frobnicate for gh pj-synker add."},
		{[]string{"-z"}, "unknown_option", "Unknown option -z for gh pj-synker add."},
		{[]string{"--own", "acme"}, "unknown_option", "Unknown option --own for gh pj-synker add. Did you mean --owner?"},
		{[]string{"--owner"}, "invalid_arguments", "--owner needs a value, such as --owner <LOGIN>."},
		{[]string{"--owner", "a", "--owner", "b"}, "invalid_arguments", "Can't use --owner more than once."},
		{[]string{"--dry-run=yes"}, "invalid_arguments", `--dry-run takes true or false, not "yes".`},
		{[]string{"--no-owner"}, "unknown_option", "Unknown option --no-owner for gh pj-synker add."},
	}
	for _, tt := range tests {
		_, err := parse(tt.args, opts, "gh pj-synker add")
		if err == nil || err.Reason != tt.reason || err.Message != tt.message || err.Exit != exitUsage {
			t.Errorf("%v: got %+v", tt.args, err)
		}
	}
}

func TestNoPrefixInference(t *testing.T) {
	// "--dry" must not resolve to --dry-run: adding an option later would break scripts.
	if _, err := parse([]string{"--dry"}, addCommand.allOptions(), "x"); err == nil {
		t.Fatal("prefix accepted")
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"acme": "acme", "a/b,c/d": "a/b,c/d", "": "''", "two words": "'two words'",
		"it's": `'it'\''s'`, "<NUMBER>": "<NUMBER>", "$(rm)": "'$(rm)'", "#1": "'#1'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuggest(t *testing.T) {
	if got := suggest("--lable", []string{"--label", "--all"}); got != "--label" {
		t.Fatal(got)
	}
	if got := suggest("--zzzzzz", []string{"--label"}); got != "" {
		t.Fatal(got)
	}
}

func hasOwner(argv []string) bool {
	for _, a := range argv {
		if a == "--owner" {
			return true
		}
	}
	return false
}

func allCommands(cs []command) []command {
	var out []command
	for _, c := range cs {
		out = append(out, c)
		out = append(out, allCommands(c.subs)...)
	}
	return out
}

// Every example must parse against the current option metadata. Examples that rely on the
// config file for --owner and --project get them added, as the config file would.
func TestHelpExamplesParse(t *testing.T) {
	for _, c := range allCommands(commands()) {
		for _, ex := range c.examples {
			if strings.Join(ex.argv[:2+len(strings.Fields(c.name))], " ") != "gh pj-synker "+c.name {
				t.Errorf("example %v doesn't start with its command", ex.argv)
			}
			if c.name != "add" {
				continue
			}
			argv := append([]string(nil), ex.argv[3:]...)
			if !hasOwner(argv) {
				argv = append(argv, "--owner", "acme", "--project", "1")
			}
			if _, err := parseAddOptions(argv, func(string) string { return "" },
				func() (string, error) { return "acme/web", nil }, func() (string, error) { return "", nil }); err != nil {
				t.Errorf("example %v: %v", ex.argv, err.Message)
			}
		}
	}
}

func TestHelpFitsIn80Columns(t *testing.T) {
	for _, c := range append(allCommands(commands()), rootCommand) {
		r := run(t, nil, env{noAPI: true}, append(strings.Fields(c.name), "--help")...)
		for _, line := range strings.Split(r.stdout, "\n") {
			if n := len([]rune(line)); n > 80 {
				t.Errorf("%s help line is %d columns: %q", c.name, n, line)
			}
		}
	}
}

func TestOptionsAreSortedInHelp(t *testing.T) {
	r := run(t, nil, env{noAPI: true}, "add", "--help")
	section := r.stdout[strings.Index(r.stdout, "Options:"):strings.Index(r.stdout, "Global Options:")]
	var names []string
	for _, l := range strings.Split(section, "\n")[1:] {
		if f := strings.Fields(strings.TrimSpace(l)); len(f) > 0 && strings.HasPrefix(l, "  -") {
			n := f[0]
			if strings.HasSuffix(n, ",") {
				n = f[1]
			}
			names = append(names, n)
		}
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("options out of order: %v", names)
		}
	}
}
