package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/UtakataKyosui/gh-pj-synker/internal/config"
	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ident"
	"github.com/UtakataKyosui/gh-pj-synker/internal/syncer"
)

var addCommand = command{
	name:    "add",
	usage:   binName + " add [<number>]... [options]",
	summary: "Add eligible issues and pull requests to a project",
	args: []argDoc{
		{"[<number>]...", "Issue or pull request numbers, or use --all"},
	},
	options: []option{
		{name: "all", kind: boolOpt, desc: "Check every item in the repositories"},
		{name: "author", kind: listOpt, hint: "<LOGINS>", desc: "Only add items by these authors", def: "any"},
		{name: "bots", kind: boolOpt, desc: "Include items by bot accounts", def: "false"},
		configOption,
		{name: "drafts", kind: boolOpt, desc: "Include draft pull requests", def: "false"},
		{name: "dry-run", kind: boolOpt, desc: "Show what would be added, without adding"},
		{name: "exclude-label", kind: listOpt, hint: "<LABELS>", desc: "Skip items that have any of these labels"},
		{name: "json", kind: boolOpt, desc: "Print the result as JSON on stdout"},
		{name: "label", kind: listOpt, hint: "<LABELS>", desc: "Only add items that have all of these labels"},
		{name: "no-config", kind: boolOpt, desc: "Don't read a config file"},
		{name: "owner", kind: stringOpt, hint: "<LOGIN>", desc: "Project owner: user or organization"},
		{name: "project", kind: intOpt, hint: "<NUMBER>", desc: "Project number from its URL"},
		{name: "repo", short: "R", kind: listOpt, hint: "<REPOS>", desc: "OWNER/REPO list", def: "current repository"},
		{name: "state", kind: listOpt, hint: "<STATES>", desc: "Item states: open, closed", def: "open"},
		{name: "type", kind: listOpt, hint: "<TYPES>", desc: "Item types: issue, pull-request", def: "both"},
		{name: "yes", short: "y", kind: boolOpt, desc: "Confirm adding the items found with --all"},
	},
	examples: []example{
		{"Add an issue to the project set in the config file", []string{"gh", "pj-synker", "add", "42"}},
		{"Add an issue to a project without a config file", []string{"gh", "pj-synker", "add", "42", "--owner", "acme", "--project", "1"}},
		{"Preview which triaged items would be added", []string{"gh", "pj-synker", "add", "--all", "--label", "triaged", "--dry-run"}},
		{"Add every eligible item in CI and read the result", []string{"gh", "pj-synker", "add", "--all", "--yes", "--json"}},
	},
	notes: []string{
		"--owner and --project are required unless the config file sets them.",
		"Options override the config file. See gh pj-synker config check --help.",
		"Lists are comma-separated, such as --label triaged,accepted.",
		"Matching ignores case. An excluded label wins over a required label.",
		"Items are only added. Nothing is removed, archived, or written back.",
		"Items already in the project are reported as unchanged, never duplicated.",
		"The repository comes from --repo, GH_REPO, the config file, the current",
		"git checkout, then GITHUB_REPOSITORY.",
		"Authentication uses gh auth login, or GH_TOKEN in CI.",
		"The token needs the project scope.",
	},
}

type addOptions struct {
	p          parsed // what was given on the command line
	conf       *loadedConfig
	owner      string
	project    int
	repos      []string
	repoSource string // "" when the repositories came from --repo
	numbers    []int
	all        bool
	cfg        filter.Config
	dryRun     bool
	yes        bool
	json       bool
	g          globalOptions
}

// argv reconstructs the command for recovery and next steps. It repeats only what was
// given on the command line, with canonical long options; the config file fills in the rest.
func (o addOptions) argv(dryRun, yes bool) []string {
	a := []string{"gh", "pj-synker", "add"}
	for _, n := range o.numbers {
		a = append(a, strconv.Itoa(n))
	}
	if o.all {
		a = append(a, "--all")
	}
	for _, name := range []string{"owner", "project", "config"} {
		if o.p.has(name) {
			a = append(a, "--"+name, o.p.strs[name])
		}
	}
	for _, name := range []string{"repo", "type", "state", "label", "exclude-label", "author"} {
		if o.p.has(name) {
			a = append(a, "--"+name, strings.Join(filter.SplitList(o.p.lists[name]...), ","))
		}
	}
	for _, name := range []string{"no-config", "drafts", "bots"} {
		if o.p.has(name) {
			if o.p.bools[name] {
				a = append(a, "--"+name)
			} else if name != "no-config" {
				a = append(a, "--no-"+name)
			}
		}
	}
	if dryRun {
		a = append(a, "--dry-run")
	}
	if yes {
		a = append(a, "--yes")
	}
	if o.json {
		a = append(a, "--json")
	}
	return a
}

type inputIssue struct {
	field, reason, message string
	missing                bool
}

// listSetting is a list option, else the config value, else the default. An option that
// is given replaces the config value; it never merges with it.
func listSetting(p parsed, name string, fromConfig, def []string) []string {
	if p.has(name) {
		return filter.SplitList(p.lists[name]...)
	}
	if fromConfig != nil {
		return fromConfig
	}
	return def
}

func boolSetting(p parsed, name string, fromConfig *bool) bool {
	if p.has(name) {
		return p.bools[name]
	}
	return fromConfig != nil && *fromConfig
}

func parseAddOptions(args []string, getenv func(string) string, currentRepo func() (string, error), getwd func() (string, error)) (addOptions, *Error) {
	path := addCommand.path()
	p, perr := parse(args, addCommand.allOptions(), path)
	if perr != nil {
		return addOptions{}, perr
	}
	if p.has("config") && p.bools["no-config"] {
		return addOptions{}, usageError("invalid_arguments", "Can't use --config with --no-config.", path)
	}
	conf, cerr := loadConfig(p, p.bools["no-config"], getenv, getwd)
	if cerr != nil {
		return addOptions{}, cerr
	}
	file := &config.File{}
	where := "a config file"
	if conf != nil {
		file, where = conf.file, conf.display
	}

	o := addOptions{
		p: p, conf: conf, all: p.bools["all"], dryRun: p.bools["dry-run"], yes: p.bools["yes"], json: p.bools["json"],
		g: globalOptions{noColor: p.bools["no-color"], nonInteractive: p.bools["non-interactive"]},
	}
	o.cfg = filter.Config{
		Types:          listSetting(p, "type", file.Types, []string{filter.TypeIssue, filter.TypePullRequest}),
		States:         listSetting(p, "state", file.States, []string{filter.StateOpen}),
		RequiredLabels: listSetting(p, "label", file.Labels, nil),
		ExcludedLabels: listSetting(p, "exclude-label", file.ExcludeLabels, nil),
		Authors:        listSetting(p, "author", file.Authors, nil),
		IncludeDrafts:  boolSetting(p, "drafts", file.Drafts),
		IncludeBots:    boolSetting(p, "bots", file.Bots),
	}

	var issues []inputIssue
	add := func(field, reason, msg string, missing bool) {
		issues = append(issues, inputIssue{field, reason, msg, missing})
	}
	switch {
	case p.has("owner"):
		o.owner = p.strs["owner"]
		if !ident.Login(o.owner) {
			add("--owner", "invalid_arguments", fmt.Sprintf("%q isn't a valid user or organization login.", o.owner), false)
		}
	case file.Owner != "":
		o.owner = file.Owner
	default:
		add("--owner", "missing_owner", fmt.Sprintf("Pass the project owner, or set project.owner in %s.", where), true)
	}
	switch {
	case p.has("project"):
		if n, err := strconv.Atoi(p.strs["project"]); err != nil || n < 1 {
			add("--project", "invalid_arguments", fmt.Sprintf("%q isn't a project number. Use the number from the project URL.", p.strs["project"]), false)
		} else {
			o.project = n
		}
	case file.Number != 0:
		o.project = file.Number
	default:
		add("--project", "missing_project", fmt.Sprintf("Pass the project number, or set project.number in %s.", where), true)
	}
	if len(o.cfg.Types) == 0 {
		add("--type", "invalid_arguments", "Pass at least one type: issue or pull-request.", false)
	}
	for _, t := range o.cfg.Types {
		if t != filter.TypeIssue && t != filter.TypePullRequest {
			add("--type", "invalid_arguments", fmt.Sprintf("%q isn't a type. Use issue or pull-request.", t), false)
		}
	}
	if len(o.cfg.States) == 0 {
		add("--state", "invalid_arguments", "Pass at least one state: open or closed.", false)
	}
	for _, st := range o.cfg.States {
		if st != filter.StateOpen && st != filter.StateClosed {
			add("--state", "invalid_arguments", fmt.Sprintf("%q isn't a state. Use open or closed.", st), false)
		}
	}
	for _, a := range p.positionals {
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 {
			add("<number>", "invalid_arguments", fmt.Sprintf("%q isn't an issue or pull request number.", sanitize(a)), false)
			continue
		}
		o.numbers = append(o.numbers, n)
	}

	// Resolve the repositories: option, environment, config file, then the checkout.
	// An explicit invalid value fails; it never falls back.
	repoField := "--repo"
	if p.has("repo") {
		o.repos = filter.SplitList(p.lists["repo"]...)
	} else if v := strings.TrimSpace(getenv("GH_REPO")); v != "" {
		o.repos, o.repoSource, repoField = []string{stripHost(v)}, "GH_REPO", "GH_REPO"
	} else if len(file.Repositories) > 0 {
		o.repos, o.repoSource = file.Repositories, conf.display
	} else if r, err := currentRepo(); err == nil && r != "" {
		o.repos, o.repoSource = []string{r}, "git remote"
	} else if v := strings.TrimSpace(getenv("GITHUB_REPOSITORY")); v != "" {
		o.repos, o.repoSource, repoField = []string{v}, "GITHUB_REPOSITORY", "GITHUB_REPOSITORY"
	}
	for _, r := range o.repos {
		if !ident.Repo(r) {
			add(repoField, "invalid_arguments", fmt.Sprintf("%q must look like OWNER/REPO.", sanitize(r)), false)
		}
	}
	if len(o.repos) == 0 {
		add("--repo", "missing_repository", "Run this in a clone of the repository, or pass --repo OWNER/REPO.", true)
	}

	switch {
	case o.all && len(o.numbers) > 0:
		add("--all", "invalid_arguments", "Can't use --all with item numbers.", false)
	case !o.all && len(o.numbers) == 0 && len(issues) == 0:
		e := &Error{Exit: exitUsage, Status: statusActionRequired, Reason: "missing_item",
			Message: "Can't add items without an issue or pull request number."}
		tmpl := o
		tmpl.numbers = []int{0}
		argv := tmpl.argv(false, false)
		argv[3] = "<NUMBER>"
		all := o
		all.all = true
		e.Recovery = []Recovery{
			{Instruction: "Pass a number", Argv: argv},
			{Instruction: "Or preview every eligible item", Argv: all.argv(true, false)},
		}
		return o, e
	case len(o.numbers) > 0 && len(o.repos) > 1:
		add("--repo", "invalid_arguments", "Can't use item numbers with more than one repository. Use --all instead.", false)
	}
	if len(issues) == 0 {
		return o, nil
	}
	return o, inputError(issues)
}

func stripHost(repo string) string {
	if parts := strings.Split(repo, "/"); len(parts) == 3 {
		return parts[1] + "/" + parts[2]
	}
	return repo
}

func inputError(issues []inputIssue) *Error {
	var data []map[string]string
	for _, is := range issues {
		data = append(data, map[string]string{"field": is.field, "reason": is.reason, "message": is.message})
	}
	if len(issues) == 1 {
		is := issues[0]
		e := &Error{Exit: exitUsage, Status: statusError, Reason: is.reason, Recovery: []Recovery{{Instruction: "See all options", Argv: []string{"gh", "pj-synker", "add", "--help"}}}}
		if is.missing {
			e.Status = statusActionRequired
			e.Message = fmt.Sprintf("Can't add items without %s. %s", is.field, is.message)
		} else {
			e.Message = "Can't add items. " + is.message
		}
		e.setData("issues", data)
		return e
	}
	e := &Error{Exit: exitUsage, Status: statusError, Reason: "invalid_arguments",
		Message:  fmt.Sprintf("Can't add items. %d inputs are invalid.", len(issues)),
		Recovery: []Recovery{{Instruction: "See all options", Argv: []string{"gh", "pj-synker", "add", "--help"}}}}
	for _, is := range issues {
		e.addDetail(is.field, is.message)
	}
	e.setData("issues", data)
	return e
}

func ref(it ghapi.Item) string { return fmt.Sprintf("%s#%d", it.Repository, it.Number) }

func itemObject(it ghapi.Item) string {
	return fmt.Sprintf("issue or pull request %d in %s", it.Number, it.Repository)
}

type addRun struct {
	ctx     context.Context
	s       Streams
	m       Mode
	o       addOptions
	api     API
	p       printer
	project ghapi.Project
	inline  bool // a progress line is on screen and must be cleared
}

func runAdd(ctx context.Context, args []string, s Streams, d Deps) int {
	o, err := parseAddOptions(args, s.Getenv, d.CurrentRepo, d.Getwd)
	m := resolveMode(s, scanGlobals(args), wantsFlag(args, "json", ""))
	if err != nil {
		return fail(s, m, err)
	}
	m = resolveMode(s, o.g, o.json)
	r := &addRun{ctx: ctx, s: s, m: m, o: o, p: printer{w: s.Err, st: m.Err, width: m.Width}}
	return r.run(d)
}

func (r *addRun) projectRef() string { return fmt.Sprintf("%s/%d", r.o.owner, r.o.project) }

func (r *addRun) human() bool { return !r.m.JSON }

// progress shows the current phase. Rich interactive output redraws one line;
// otherwise each phase is printed once.
func (r *addRun) progress(msg string) {
	if !r.human() {
		return
	}
	if r.m.Err.Rich && r.m.Interactive {
		r.clearProgress()
		fmt.Fprint(r.s.Err, "  "+msg)
		r.inline = true
		return
	}
	if r.o.all {
		r.p.text(msg)
	}
}

func (r *addRun) clearProgress() {
	if r.inline {
		fmt.Fprint(r.s.Err, "\r\x1b[K")
		r.inline = false
	}
}

func (r *addRun) fail(e *Error) int {
	r.clearProgress()
	if r.human() {
		r.p.blank()
	}
	return fail(r.s, r.m, e)
}

func (r *addRun) run(d Deps) int {
	api, err := d.NewAPI()
	if err != nil {
		return r.fail(mapAPIError(err, apiContext{verb: "connect to", object: "GitHub"}, r.s.Getenv))
	}
	r.api = api

	if r.human() {
		r.p.row("Project", r.projectRef())
		if r.o.conf != nil {
			r.p.row("Config", r.o.conf.display)
		}
		label := "Repository"
		if len(r.o.repos) > 1 {
			label = "Repositories"
		}
		r.p.row(label, strings.Join(r.o.repos, ", "))
		if r.o.repoSource != "" {
			r.p.row("Source", r.o.repoSource)
		}
	}

	r.progress(fmt.Sprintf("Finding project %s…", r.projectRef()))
	project, err := api.GetProject(r.ctx, r.o.owner, r.o.project)
	if err != nil {
		if r.ctx.Err() != nil {
			return r.canceled(nil)
		}
		if errors.Is(err, ghapi.ErrProjectNotFound) {
			err = r.projectNotFound()
		}
		return r.fail(mapAPIError(err, apiContext{verb: "read", object: "project " + r.projectRef(), notFound: r.projectNotFound()}, r.s.Getenv))
	}
	r.project = project
	if project.Closed {
		return r.fail(&Error{Exit: exitFailure, Status: statusError, Reason: "project_closed",
			Message:  fmt.Sprintf("Can't add items to project %s. The project is closed.", r.projectRef()),
			Recovery: []Recovery{{Instruction: "Reopen it in the project settings, then run this again."}}})
	}

	var refs []syncer.Ref
	if r.o.all {
		for _, repo := range r.o.repos {
			r.progress(fmt.Sprintf("Listing items in %s…", repo))
			numbers, err := api.ListItemNumbers(r.ctx, repo, r.o.cfg.States)
			if err != nil {
				if r.ctx.Err() != nil {
					return r.canceled(nil)
				}
				return r.fail(mapAPIError(err, apiContext{verb: "list", object: "issues and pull requests in " + repo,
					notFound: repoNotFound(repo)}, r.s.Getenv))
			}
			for _, n := range numbers {
				refs = append(refs, syncer.Ref{Repository: repo, Number: n})
			}
		}
		if len(refs) > 1 {
			r.progress(fmt.Sprintf("Checking %s…", plural(len(refs), "item", "items")))
		}
	} else {
		for _, n := range r.o.numbers {
			refs = append(refs, syncer.Ref{Repository: r.o.repos[0], Number: n})
		}
	}

	results := syncer.Plan(r.ctx, api, project, r.o.cfg, refs)
	if r.ctx.Err() != nil {
		return r.canceled(results)
	}

	pending := pendingItems(results)
	if r.o.all && !r.o.dryRun && len(pending) > 0 && !r.o.yes {
		if code, ok := r.confirm(pending); !ok {
			return code
		}
	}
	if len(pending) > 1 && !r.o.dryRun {
		r.progress(fmt.Sprintf("Adding %s…", plural(len(pending), "item", "items")))
	}
	syncer.Apply(r.ctx, api, project, results, r.o.dryRun)
	r.clearProgress()
	if r.ctx.Err() != nil {
		return r.canceled(results)
	}
	return r.report(results, len(refs) == 0)
}

func (r *addRun) projectNotFound() *Error {
	return &Error{Exit: exitFailure, Status: statusError, Reason: "project_not_found",
		Message: fmt.Sprintf("Couldn't find project %d owned by %s.", r.o.project, r.o.owner),
		Details: []detail{{"Token", "The token needs the project scope to see projects."}},
		Recovery: []Recovery{
			{Instruction: "List the owner's projects", Argv: []string{"gh", "project", "list", "--owner", r.o.owner}},
			{Instruction: "Add the project scope to your gh login", Argv: []string{"gh", "auth", "refresh", "--scopes", "project"}},
		}}
}

func repoNotFound(repo string) *Error {
	return &Error{Exit: exitFailure, Status: statusError, Reason: "not_found",
		Message:  fmt.Sprintf("Couldn't find repository %s.", repo),
		Recovery: []Recovery{{Instruction: "Check the repository name", Argv: []string{"gh", "repo", "view", repo}}}}
}

func itemNotFound(it ghapi.Item) *Error {
	return &Error{Exit: exitFailure, Status: statusError, Reason: "not_found",
		Message: fmt.Sprintf("Couldn't find issue or pull request %d in %s.", it.Number, it.Repository),
		Recovery: []Recovery{
			{Instruction: "List open issues", Argv: []string{"gh", "issue", "list", "--repo", it.Repository}},
			{Instruction: "List open pull requests", Argv: []string{"gh", "pr", "list", "--repo", it.Repository}},
		}}
}

func (r *addRun) resultError(res syncer.Result) *Error {
	c := apiContext{verb: "read", object: itemObject(res.Item), notFound: itemNotFound(res.Item)}
	if res.Mutating {
		c = apiContext{verb: "add", object: ref(res.Item) + " to " + r.projectRef(), mutation: true}
	}
	return mapAPIError(res.Err, c, r.s.Getenv)
}

func pendingItems(results []syncer.Result) []syncer.Result {
	var out []syncer.Result
	for _, res := range results {
		if res.Outcome == syncer.Pending {
			out = append(out, res)
		}
	}
	return out
}

const listLimit = 5

// itemRows prints up to five items, then "and N more".
func (r *addRun) itemRows(items []syncer.Result, value func(syncer.Result) string) {
	for i, it := range items {
		if i == listLimit {
			r.p.text(fmt.Sprintf("and %s more", commas(len(items)-listLimit)))
			return
		}
		r.p.descRow(ref(it.Item), sanitize(value(it)))
	}
}

func title(res syncer.Result) string { return res.Item.Title }

// confirm asks before adding items found with --all. It returns false with the exit code
// when the run must stop.
func (r *addRun) confirm(pending []syncer.Result) (int, bool) {
	r.clearProgress()
	n := plural(len(pending), "item", "items")
	if !r.m.Interactive {
		e := &Error{Exit: exitUsage, Status: statusActionRequired, Reason: "confirmation_required", UserActionRequired: true,
			Message: fmt.Sprintf("Can't add %s found with --all without confirmation in non-interactive mode.", n)}
		var effects []map[string]any
		for i, it := range pending {
			effects = append(effects, map[string]any{"repository": it.Item.Repository, "number": it.Item.Number, "title": it.Item.Title})
			if i < listLimit {
				e.addDetail(ref(it.Item), sanitize(truncate(it.Item.Title, 60)))
			}
		}
		if len(pending) > listLimit {
			e.addDetail("", fmt.Sprintf("and %s more", commas(len(pending)-listLimit)))
		}
		e.setData("effects", effects)
		e.Recovery = append(e.Recovery, Recovery{
			Instruction: "Review the items, then confirm with --yes",
			Argv:        r.o.argv(false, true),
			When:        fmt.Sprintf("Only after a human approves: adds %s to project %s", n, r.projectRef()),
		})
		return r.fail(e), false
	}
	r.p.blank()
	r.p.text(fmt.Sprintf("Adds %s to %s", n, r.projectRef()))
	r.itemRows(pending, title)
	r.p.blank()
	question := fmt.Sprintf("Add %s to project %s? (y/N) ", n, r.projectRef())
	if r.m.Err.Rich {
		question = r.m.Err.paint(ansiGreen, "?") + " " + question
	}
	fmt.Fprint(r.s.Err, question)
	// Read in the background so Ctrl+C ends the prompt without another line of input.
	answers := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r.s.In).ReadString('\n')
		answers <- line
	}()
	var answer string
	select {
	case answer = <-answers:
	case <-r.ctx.Done():
		r.p.blank()
		r.p.line("Canceled. Nothing was changed.")
		return exitCanceled, false
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		r.p.line("Canceled. Nothing was changed.")
		return exitCanceled, false
	}
	r.p.blank()
	return 0, true
}

func (r *addRun) canceled(results []syncer.Result) int {
	r.clearProgress()
	var added, unknown []string
	for _, res := range results {
		switch {
		case res.Outcome == syncer.Added:
			added = append(added, res.ProjectItemID)
		case res.Outcome == syncer.Failed && res.Mutating:
			// The request was sent; GitHub may have accepted it before the interrupt.
			unknown = append(unknown, ref(res.Item))
		}
	}
	if r.m.JSON {
		e := &Error{Exit: exitCanceled, Status: statusError, Reason: "canceled", Message: "Canceled."}
		if added == nil {
			added = []string{}
		}
		e.setData("addedProjectItemIds", added)
		if unknown == nil {
			unknown = []string{}
		}
		e.setData("unknownOutcome", unknown)
		fmt.Fprintln(r.s.Err, e.renderJSON())
		return exitCanceled
	}
	r.p.blank()
	if len(unknown) > 0 {
		r.p.line(fmt.Sprintf("Canceled while adding %s. It may or may not have been added.", strings.Join(unknown, ", ")))
		r.p.line("Run the command again to check; adding is idempotent.")
	}
	if len(added) == 0 {
		if len(unknown) == 0 {
			r.p.line("Canceled. Nothing was changed.")
		}
	} else {
		r.p.line(fmt.Sprintf("Canceled after adding %s. The rest weren't added.", plural(len(added), "item", "items")))
	}
	return exitCanceled
}

func byOutcome(results []syncer.Result, o syncer.Outcome) []syncer.Result {
	var out []syncer.Result
	for _, res := range results {
		if res.Outcome == o {
			out = append(out, res)
		}
	}
	return out
}

func (r *addRun) report(results []syncer.Result, empty bool) int {
	failed := byOutcome(results, syncer.Failed)
	// When every item failed for the same reason, that is the error.
	if len(failed) > 0 && len(failed) == len(results) {
		first := r.resultError(failed[0])
		same := true
		for _, f := range failed[1:] {
			if r.resultError(f).Reason != first.Reason {
				same = false
				break
			}
		}
		if same {
			if len(failed) > 1 {
				first.setData("failed", len(failed))
			}
			return r.fail(first)
		}
	}

	code := exitOK
	if len(failed) > 0 {
		code = exitFailure
	}
	if r.m.JSON {
		r.writeJSON(results)
		return code
	}

	r.p.blank()
	if empty {
		r.p.text(fmt.Sprintf("No %s issues or pull requests in %s.", strings.Join(r.o.cfg.States, " or "), strings.Join(r.o.repos, ", ")))
		return exitOK
	}
	single := len(results) == 1
	sections := 0
	section := func() {
		if sections > 0 {
			r.p.blank()
		}
		sections++
	}
	if added := byOutcome(results, syncer.Added); len(added) > 0 {
		section()
		if single {
			r.p.success("Added", ref(added[0].Item))
			r.p.descRow("Title", sanitize(added[0].Item.Title))
			r.p.row("Item ID", added[0].ProjectItemID)
		} else {
			r.p.success("Added", plural(len(added), "item", "items"))
			r.itemRows(added, title)
		}
	}
	planned := byOutcome(results, syncer.Planned)
	if len(planned) > 0 {
		section()
		if single {
			r.p.row("Would add", ref(planned[0].Item))
			r.p.descRow("Title", sanitize(planned[0].Item.Title))
		} else {
			r.p.row("Would add", plural(len(planned), "item", "items"))
			r.itemRows(planned, title)
		}
	}
	if unchanged := byOutcome(results, syncer.Unchanged); len(unchanged) > 0 {
		section()
		if single {
			r.p.text(fmt.Sprintf("%s is already in %s.", ref(unchanged[0].Item), r.projectRef()))
			r.p.row("Item ID", unchanged[0].ProjectItemID)
		} else {
			r.p.row("Unchanged", plural(len(unchanged), "item", "items")+" already in the project")
		}
	}
	if skipped := byOutcome(results, syncer.Skipped); len(skipped) > 0 {
		section()
		if single {
			r.p.row("Skipped", ref(skipped[0].Item))
			r.p.row("Reason", skipped[0].Message)
		} else {
			r.p.row("Skipped", plural(len(skipped), "item", "items"))
			r.itemRows(skipped, func(res syncer.Result) string { return res.Message })
		}
	}
	if len(failed) > 0 {
		section()
		r.p.errorHeadline(fmt.Sprintf("Checked %s of %s. %d failed.",
			commas(len(results)-len(failed)), plural(len(results), "item", "items"), len(failed)))
		r.itemRows(failed, func(res syncer.Result) string { return r.resultError(res).Message })
		if len(failed) > listLimit || len(results) > listLimit {
			r.p.blank()
			r.p.text("See every result:")
			r.p.command(joinArgv(r.o.argv(true, false)) + " --json")
		}
		return code
	}

	if r.o.dryRun {
		r.p.blank()
		r.p.text("Dry run. Nothing was changed.")
		if len(planned) > 0 {
			r.p.blank()
			if len(planned) == 1 {
				r.p.text("Add it:")
			} else {
				r.p.text("Add them:")
			}
			r.p.command(joinArgv(r.o.argv(false, r.o.all)))
		}
	} else if len(byOutcome(results, syncer.Added)) > 0 {
		r.p.blank()
		r.p.text("View the project:")
		r.p.command(joinArgv(r.viewProjectArgv()))
	}

	// Scripts read the project item IDs from stdout.
	if !r.s.OutTTY || !r.m.Interactive {
		for _, res := range results {
			if res.Outcome == syncer.Added || res.Outcome == syncer.Unchanged {
				fmt.Fprintln(r.s.Out, res.ProjectItemID)
			}
		}
	}
	return code
}

func (r *addRun) configPath() *string {
	if r.o.conf == nil {
		return nil
	}
	return &r.o.conf.display
}

func (r *addRun) viewProjectArgv() []string {
	return []string{"gh", "project", "view", strconv.Itoa(r.o.project), "--owner", r.o.owner}
}

type resultJSON struct {
	Repository    string           `json:"repository"`
	Number        int              `json:"number"`
	Type          *string          `json:"type"`
	Title         *string          `json:"title"`
	URL           *string          `json:"url"`
	Outcome       string           `json:"outcome"`
	ProjectItemID *string          `json:"projectItemId"`
	Reason        *string          `json:"reason"`
	Message       *string          `json:"message"`
	Error         *errorResultJSON `json:"error,omitempty"`
}

type errorResultJSON struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r *addRun) writeJSON(results []syncer.Result) {
	out := make([]resultJSON, 0, len(results))
	summary := map[string]int{}
	for _, o := range []syncer.Outcome{syncer.Added, syncer.Unchanged, syncer.Planned, syncer.Skipped, syncer.Failed} {
		summary[string(o)] = 0
	}
	for _, res := range results {
		j := resultJSON{
			Repository: res.Item.Repository, Number: res.Item.Number, Outcome: string(res.Outcome),
			Type: strOrNil(res.Item.Type), Title: strOrNil(res.Item.Title), URL: strOrNil(res.Item.URL),
			ProjectItemID: strOrNil(res.ProjectItemID), Reason: strOrNil(res.Reason), Message: strOrNil(res.Message),
		}
		if res.Outcome == syncer.Failed {
			e := r.resultError(res)
			j.Error = &errorResultJSON{Reason: e.Reason, Message: e.Message}
		}
		summary[string(res.Outcome)]++
		out = append(out, j)
	}
	doc := map[string]any{
		"project": map[string]any{
			"owner": r.o.owner, "number": r.o.project, "id": r.project.ID,
			"title": strOrNil(r.project.Title), "url": strOrNil(r.project.URL),
		},
		"config":  r.configPath(),
		"dryRun":  r.o.dryRun,
		"results": out,
		"summary": summary,
	}
	var next []nextJSON
	if r.o.dryRun && summary[string(syncer.Planned)] > 0 {
		next = nextList([]Recovery{{Argv: r.o.argv(false, r.o.all),
			When: "Only after a human approves the planned items"}})
	} else if summary[string(syncer.Added)] > 0 {
		next = nextList([]Recovery{{Argv: r.viewProjectArgv()}})
	}
	if len(next) > 0 {
		doc["next"] = next
	}
	writeJSONDoc(r.s, doc)
}

// writeJSONDoc writes exactly one document: indented on a TTY, one line otherwise.
func writeJSONDoc(s Streams, doc any) {
	enc := json.NewEncoder(s.Out)
	enc.SetEscapeHTML(false)
	if s.OutTTY {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(doc)
}
