package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UtakataKyosui/gh-pj-synker/internal/config"
	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ident"
)

var initCommand = command{
	name:    "init",
	usage:   binName + " init [options]",
	summary: "Create a config file for the repository",
	options: []option{
		{name: "author", kind: listOpt, hint: "<LOGINS>", desc: "Only add items by these authors"},
		{name: "bots", kind: boolOpt, desc: "Include items by bot accounts"},
		{name: "drafts", kind: boolOpt, desc: "Include draft pull requests"},
		{name: "dry-run", kind: boolOpt, desc: "Print the file to stdout without writing it"},
		{name: "exclude-label", kind: listOpt, hint: "<LABELS>", desc: "Skip items that have any of these labels"},
		{name: "force", kind: boolOpt, desc: "Replace the config file if it exists"},
		{name: "json", kind: boolOpt, desc: "Print the result as JSON on stdout"},
		{name: "label", kind: listOpt, hint: "<LABELS>", desc: "Only add items that have all of these labels"},
		{name: "owner", kind: stringOpt, hint: "<LOGIN>", desc: "Project owner: user or organization"},
		{name: "path", kind: stringOpt, hint: "<FILE>", desc: "Where to write the file", def: ".github/pj-synker.yml"},
		{name: "project", kind: intOpt, hint: "<NUMBER>", desc: "Project number from its URL"},
		{name: "repo", short: "R", kind: listOpt, hint: "<REPOS>", desc: "OWNER/REPO list", def: "current repository"},
		{name: "state", kind: listOpt, hint: "<STATES>", desc: "Item states: open, closed"},
		{name: "type", kind: listOpt, hint: "<TYPES>", desc: "Item types: issue, pull-request"},
	},
	examples: []example{
		{"Create the config file, asking for the project", []string{"gh", "pj-synker", "init"}},
		{"Create it without prompts", []string{"gh", "pj-synker", "init", "--owner", "acme", "--project", "1", "--label", "triaged"}},
		{"Preview the file", []string{"gh", "pj-synker", "init", "--owner", "acme", "--project", "1", "--dry-run"}},
	},
	notes: []string{
		"The file is written to .github/pj-synker.yml at the root of the git checkout,",
		"or the current directory outside a checkout. Nothing is written to GitHub.",
		"Unless --dry-run is given, it also reads the project's workflows and warns",
		"about ones that add items on their own.",
		"Prompts ask only for --owner and --project, and only on a terminal.",
		"An existing config file is never replaced without --force.",
	},
}

type initRun struct {
	s      Streams
	m      Mode
	p      parsed
	in     *bufio.Reader
	asked  bool // a prompt was answered, so print the command to repeat it
	cwd    string
	ctx    context.Context
	d      Deps
	repo   string // current repository, for the default owner
	pr     printer
	stdout printer
}

func runInit(ctx context.Context, args []string, s Streams, d Deps) int {
	m := resolveMode(s, scanGlobals(args), wantsFlag(args, "json", ""))
	p, err := parse(args, initCommand.allOptions(), initCommand.path())
	if err != nil {
		return fail(s, m, err)
	}
	m = resolveMode(s, globalOptions{noColor: p.bools["no-color"], nonInteractive: p.bools["non-interactive"]}, p.bools["json"])
	if len(p.positionals) > 0 {
		return fail(s, m, usageError("invalid_arguments",
			fmt.Sprintf("%s takes no arguments, not %q.", initCommand.path(), sanitize(p.positionals[0])), initCommand.path()))
	}
	cwd, _ := d.Getwd()
	repo, _ := d.CurrentRepo()
	r := &initRun{s: s, m: m, p: p, in: bufio.NewReader(s.In), cwd: cwd, repo: repo, ctx: ctx, d: d,
		pr: printer{w: s.Err, st: m.Err, width: m.Width}, stdout: printer{w: s.Out, st: m.Out, width: m.Width}}
	return r.run()
}

func (r *initRun) run() int {
	f, issues := r.settings()
	if len(issues) > 0 {
		return fail(r.s, r.m, initInputError(issues))
	}
	if code, ok := r.promptMissing(&f); !ok {
		return code
	}

	path, display, perr := r.target()
	if perr != nil {
		return fail(r.s, r.m, perr)
	}
	data := config.Render(f)
	// Never write a file this build couldn't read back.
	if _, err := config.Parse(data, display); err != nil {
		return fail(r.s, r.m, &Error{Exit: exitFailure, Status: statusError, Reason: "internal_error",
			Message: "Unexpected error in gh pj-synker init. This is a bug in gh-pj-synker.",
			Details: []detail{{"Error", sanitize(err.Error())}}})
	}

	existing := r.existingConfig(path)
	replacing := existing == path
	if existing != "" && (!replacing || !r.p.bools["force"]) {
		return fail(r.s, r.m, r.conflict(existing, display, replacing))
	}

	outcome := "created"
	switch {
	case r.p.bools["dry-run"]:
		outcome = "planned"
	case replacing:
		outcome = "replaced"
	}
	if outcome != "planned" {
		if err := config.WriteFile(path, data, replacing); err != nil {
			if errors.Is(err, config.ErrExists) {
				return fail(r.s, r.m, r.conflict(path, display, true))
			}
			return fail(r.s, r.m, &Error{Exit: exitFailure, Status: statusError, Reason: "write_failed",
				Message: fmt.Sprintf("Couldn't write %s.", display), Details: []detail{{"Error", sanitize(err.Error())}}})
		}
	}
	var warn *overlapWarning
	if outcome != "planned" {
		warn = checkOverlappingWorkflows(r.ctx, r.d, f.Owner, f.Number)
	}
	return r.report(f, display, outcome, data, warn)
}

// settings reads the options. Values are validated now; missing ones are prompted for later.
func (r *initRun) settings() (config.File, []inputIssue) {
	p := r.p
	var issues []inputIssue
	add := func(field, msg string) { issues = append(issues, inputIssue{field, "invalid_arguments", msg, false}) }
	f := config.File{Version: config.CurrentVersion}
	if p.has("owner") {
		f.Owner = p.strs["owner"]
		if !ident.Login(f.Owner) {
			add("--owner", fmt.Sprintf("%q isn't a valid user or organization login.", f.Owner))
		}
	}
	if p.has("project") {
		n, err := strconv.Atoi(p.strs["project"])
		if err != nil || n < 1 {
			add("--project", fmt.Sprintf("%q isn't a project number. Use the number from the project URL.", p.strs["project"]))
		}
		f.Number = n
	}
	list := func(name string) []string {
		if !p.has(name) {
			return nil
		}
		v := filter.SplitList(p.lists[name]...)
		if v == nil {
			v = []string{}
		}
		return v
	}
	if p.has("repo") {
		f.Repositories = list("repo")
		for _, repo := range f.Repositories {
			if !ident.Repo(repo) {
				add("--repo", fmt.Sprintf("%q must look like OWNER/REPO.", sanitize(repo)))
			}
		}
	}
	f.Types, f.States = list("type"), list("state")
	for _, t := range f.Types {
		if t != filter.TypeIssue && t != filter.TypePullRequest {
			add("--type", fmt.Sprintf("%q isn't a type. Use issue or pull-request.", t))
		}
	}
	for _, st := range f.States {
		if st != filter.StateOpen && st != filter.StateClosed {
			add("--state", fmt.Sprintf("%q isn't a state. Use open or closed.", st))
		}
	}
	f.Labels, f.ExcludeLabels, f.Authors = list("label"), list("exclude-label"), list("author")
	for _, name := range []string{"drafts", "bots"} {
		if p.has(name) {
			v := p.bools[name]
			if name == "drafts" {
				f.Drafts = &v
			} else {
				f.Bots = &v
			}
		}
	}
	return f, issues
}

func initInputError(issues []inputIssue) *Error {
	e := inputError(issues)
	e.Message = strings.Replace(e.Message, "Can't add items", "Can't create the config file", 1)
	for i := range e.Recovery {
		e.Recovery[i].Argv = []string{"gh", "pj-synker", "init", "--help"}
	}
	return e
}

// defaultOwner is the owner of the repository the file is for.
func (r *initRun) defaultOwner(f config.File) string {
	if len(f.Repositories) > 0 {
		return strings.SplitN(f.Repositories[0], "/", 2)[0]
	}
	if v := strings.TrimSpace(r.s.Getenv("GH_REPO")); v != "" {
		return strings.SplitN(stripHost(v), "/", 2)[0]
	}
	if r.repo != "" {
		return strings.SplitN(r.repo, "/", 2)[0]
	}
	return ""
}

// promptMissing asks for --owner and --project when they are missing, one at a time.
// Without a terminal it fails, naming the options.
func (r *initRun) promptMissing(f *config.File) (int, bool) {
	var missing []inputIssue
	if f.Owner == "" {
		missing = append(missing, inputIssue{"--owner", "missing_owner", "Pass the user or organization that owns the project.", true})
	}
	if f.Number == 0 {
		missing = append(missing, inputIssue{"--project", "missing_project", "Pass the project number from its URL.", true})
	}
	if len(missing) == 0 {
		return 0, true
	}
	if !r.m.Interactive {
		e := initInputError(missing)
		if len(missing) == 2 {
			e.Status, e.Reason = statusActionRequired, "missing_input"
			e.Message = "Can't create the config file without --owner and --project in non-interactive mode."
			e.Details = nil
		} else {
			e.Message = fmt.Sprintf("Can't create the config file without %s in non-interactive mode.", missing[0].field)
		}
		e.Recovery = []Recovery{{Instruction: "Pass the project", Argv: r.argv(withOptions{owner: "<LOGIN>", project: "<NUMBER>"})}}
		return fail(r.s, r.m, e), false
	}
	if f.Owner == "" {
		v, ok := r.ask("Project owner?", r.defaultOwner(*f), "user or organization login", func(s string) string {
			if !ident.Login(s) {
				return fmt.Sprintf("%q isn't a valid user or organization login.", s)
			}
			return ""
		})
		if !ok {
			r.canceled()
			return exitCanceled, false
		}
		f.Owner = v
	}
	if f.Number == 0 {
		v, ok := r.ask("Project number?", "", "the number at the end of the project URL", func(s string) string {
			if n, err := strconv.Atoi(s); err != nil || n < 1 {
				return fmt.Sprintf("%q isn't a project number. Use the number from the project URL.", s)
			}
			return ""
		})
		if !ok {
			r.canceled()
			return exitCanceled, false
		}
		f.Number, _ = strconv.Atoi(v)
	}
	r.pr.blank()
	return 0, true
}

// ask prompts on stderr until the answer is valid. Enter keeps a default. EOF cancels.
func (r *initRun) ask(question, def, hint string, validate func(string) string) (string, bool) {
	r.asked = true
	for {
		q := question
		if def != "" {
			q += " (default: " + def + ")"
		} else if hint != "" {
			q += " (" + hint + ")"
		}
		if r.m.Err.Rich {
			q = r.m.Err.paint(ansiGreen, "?") + " " + q
		}
		fmt.Fprint(r.s.Err, q+" ")
		line, err := r.in.ReadString('\n')
		answer := strings.TrimSpace(line)
		if err != nil && (answer == "" || !errors.Is(err, io.EOF)) {
			fmt.Fprintln(r.s.Err)
			return "", false
		}
		if answer == "" {
			answer = def
		}
		if answer == "" {
			continue
		}
		if msg := validate(answer); msg != "" {
			r.pr.text(msg)
			continue
		}
		return answer, true
	}
}

func (r *initRun) canceled() {
	if r.m.JSON {
		fmt.Fprintln(r.s.Err, (&Error{Status: statusError, Reason: "canceled", Message: "Canceled."}).renderJSON())
		return
	}
	r.pr.line("Canceled. Nothing was written.")
}

// target is where the file goes: --path, or .github/pj-synker.yml at the checkout root.
func (r *initRun) target() (string, string, *Error) {
	if r.p.has("path") {
		path := r.p.strs["path"]
		if strings.TrimSpace(path) == "" {
			return "", "", usageError("invalid_arguments", "--path needs a file name, such as --path .github/pj-synker.yml.", initCommand.path())
		}
		if !filepath.IsAbs(path) && r.cwd != "" {
			path = filepath.Join(r.cwd, path)
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return "", "", usageError("invalid_arguments", fmt.Sprintf("Can't write to %s. It's a directory.", displayPath(path, r.cwd)), initCommand.path())
		}
		return path, displayPath(path, r.cwd), nil
	}
	root := config.CheckoutRoot(r.cwd)
	if root == "" {
		root = r.cwd
	}
	path := filepath.Join(root, config.Names[0])
	return path, displayPath(path, r.cwd), nil
}

// existingConfig is a config file that a run in the target's directory would already read:
// the target itself, or another candidate the new file would conflict with.
func (r *initRun) existingConfig(target string) string {
	if _, err := os.Stat(target); err == nil {
		return target
	}
	dir := filepath.Dir(filepath.Dir(target))
	if r.p.has("path") {
		// A custom path is never discovered, but one of the default names competes with other candidates.
		var ok bool
		if dir, ok = discoveryDir(target); !ok {
			return ""
		}
	}
	found, err := config.Discover(dir)
	var amb *config.ErrAmbiguous
	if errors.As(err, &amb) {
		return amb.Paths[0]
	}
	return found
}

// discoveryDir is the directory whose discovery would read target, if target has a default name.
func discoveryDir(target string) (string, bool) {
	dir, name := filepath.Split(target)
	dir = filepath.Clean(dir)
	for _, n := range config.Names {
		if filepath.Base(n) != name {
			continue
		}
		if parent := filepath.Dir(n); parent != "." {
			if filepath.Base(dir) == parent {
				return filepath.Dir(dir), true
			}
			continue
		}
		return dir, true
	}
	return "", false
}

func (r *initRun) conflict(existing, display string, same bool) *Error {
	shown := displayPath(existing, r.cwd)
	if same {
		return &Error{Exit: exitFailure, Status: statusError, Reason: "config_exists",
			Message: fmt.Sprintf("Can't create %s. The file already exists.", display),
			Recovery: []Recovery{
				{Instruction: "Check the existing file", Argv: []string{"gh", "pj-synker", "config", "check"}},
				{Instruction: "Or replace it", Argv: append(r.argv(withOptions{}), "--force")},
			}}
	}
	return &Error{Exit: exitFailure, Status: statusError, Reason: "config_exists",
		Message: fmt.Sprintf("Can't create %s. %s is already a config file, and two would be ambiguous.", display, shown),
		Recovery: []Recovery{
			{Instruction: "Check the existing file", Argv: []string{"gh", "pj-synker", "config", "check"}},
			{Instruction: "Or remove " + shown + ", then run this again."},
		}}
}

type withOptions struct{ owner, project string }

// argv repeats the options given on the command line, plus any that were answered at a prompt.
func (r *initRun) argv(w withOptions) []string {
	a := []string{"gh", "pj-synker", "init"}
	for _, name := range []string{"owner", "project", "path"} {
		switch {
		case r.p.has(name):
			a = append(a, "--"+name, r.p.strs[name])
		case name == "owner" && w.owner != "":
			a = append(a, "--owner", w.owner)
		case name == "project" && w.project != "":
			a = append(a, "--project", w.project)
		}
	}
	for _, name := range []string{"repo", "type", "state", "label", "exclude-label", "author"} {
		if r.p.has(name) {
			a = append(a, "--"+name, strings.Join(filter.SplitList(r.p.lists[name]...), ","))
		}
	}
	for _, name := range []string{"drafts", "bots"} {
		if r.p.has(name) {
			if r.p.bools[name] {
				a = append(a, "--"+name)
			} else {
				a = append(a, "--no-"+name)
			}
		}
	}
	return a
}

func (r *initRun) report(f config.File, display, outcome string, data []byte, warn *overlapWarning) int {
	next := []Recovery{{Instruction: "Preview the items it would add", Argv: []string{"gh", "pj-synker", "add", "--all", "--dry-run"}}}
	if r.m.JSON {
		doc := map[string]any{
			"config":  map[string]any{"path": display, "owner": f.Owner, "number": f.Number},
			"outcome": outcome,
			"next":    nextList(next),
		}
		if outcome == "planned" {
			doc["content"] = string(data)
		}
		if w := warningsJSON(warn); w != nil {
			doc["warnings"] = w
		}
		writeJSONDoc(r.s, doc)
		return exitOK
	}
	if outcome == "planned" {
		_, _ = r.s.Out.Write(data)
		r.pr.text(fmt.Sprintf("Dry run. %s wasn't written.", display))
		return exitOK
	}
	verb := "Created"
	if outcome == "replaced" {
		verb = "Replaced"
	}
	r.pr.success(verb, display)
	r.pr.row("Project", fmt.Sprintf("%s/%d", f.Owner, f.Number))
	if len(f.Repositories) > 0 {
		r.pr.row("Repositories", strings.Join(f.Repositories, ", "))
	}
	if r.asked {
		r.pr.blank()
		r.pr.text("Run this again without prompts:")
		r.pr.command(joinArgv(r.argv(withOptions{owner: f.Owner, project: strconv.Itoa(f.Number)})))
	}
	r.pr.blank()
	r.pr.text(next[0].Instruction + ":")
	r.pr.command(joinArgv(next[0].Argv))
	r.pr.blank()
	r.pr.text("Commit the file so CI uses the same settings.")
	if warn != nil {
		printOverlapWarning(r.pr, warn)
	}
	// Scripts read the path from stdout.
	if !r.s.OutTTY || !r.m.Interactive {
		fmt.Fprintln(r.s.Out, display)
	}
	return exitOK
}
