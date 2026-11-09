// Package cmd implements the `gh pj-synker` command line.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
	"github.com/UtakataKyosui/gh-pj-synker/internal/syncer"
)

const usage = `Add eligible issues and pull requests to a GitHub Project (v2), one way only.

USAGE
  gh pj-synker --project-owner <login> --project-number <n> [--issue <n> | --all] [flags]

Items are judged on fresh API data. Nothing is ever removed, archived or written back;
existing project items are never duplicated.

FLAGS
`

// stringList is a repeatable, comma/newline separated flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type options struct {
	projectOwner  string
	projectNumber int
	repos         stringList
	issue         int
	all           bool
	types         stringList
	states        stringList
	required      stringList
	excluded      stringList
	authors       stringList
	includeDrafts bool
	includeBots   bool
	dryRun        bool
	jsonOut       bool
}

// Execute runs the CLI and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, func() (syncer.API, error) { return ghapi.New() })
}

func run(args []string, stdout, stderr io.Writer, newAPI func() (syncer.API, error)) int {
	var o options
	fs := flag.NewFlagSet("gh pj-synker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage); fs.PrintDefaults() }
	fs.StringVar(&o.projectOwner, "project-owner", "", "user or organization login that owns the project (required)")
	fs.IntVar(&o.projectNumber, "project-number", 0, "project number from its URL (required)")
	fs.Var(&o.repos, "repo", "owner/repo to sync, repeatable (default: $GH_REPO, $GITHUB_REPOSITORY or the current git repository)")
	fs.IntVar(&o.issue, "issue", 0, "issue/PR number to sync (default in GitHub Actions: the event's item)")
	fs.BoolVar(&o.all, "all", false, "check every issue and PR of the repositories whose state is allowed")
	fs.Var(&o.types, "types", "allowed item types: issue, pull_request (default: both)")
	fs.Var(&o.states, "states", "allowed states: open, closed (default: open)")
	fs.Var(&o.required, "required-labels", "labels that must ALL be present")
	fs.Var(&o.excluded, "excluded-labels", "labels where ANY match excludes the item (wins over required)")
	fs.Var(&o.authors, "allowed-authors", "allowed author logins (default: any)")
	fs.BoolVar(&o.includeDrafts, "include-drafts", false, "also add draft pull requests")
	fs.BoolVar(&o.includeBots, "include-bots", false, "also add items authored by bot accounts")
	fs.BoolVar(&o.dryRun, "dry-run", false, "check eligibility and project access without adding anything")
	fs.BoolVar(&o.jsonOut, "json", false, "print results as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if err := o.validate(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}

	cfg := filter.Config{
		Types:          defaulted(filter.ParseList(o.types...), filter.TypeIssue, filter.TypePullRequest),
		States:         defaulted(filter.ParseList(o.states...), filter.StateOpen),
		RequiredLabels: filter.ParseList(o.required...),
		ExcludedLabels: filter.ParseList(o.excluded...),
		Authors:        filter.ParseList(o.authors...),
		ExcludeDrafts:  !o.includeDrafts,
		ExcludeBots:    !o.includeBots,
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}

	repos := filter.ParseList(o.repos...)
	if len(repos) == 0 {
		r, err := defaultRepo()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		repos = []string{r}
	}
	for _, r := range repos {
		if parts := strings.Split(r, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			fmt.Fprintf(stderr, "error: --repo %q must use owner/repo format\n", r)
			return 2
		}
	}

	number := o.issue
	if !o.all && number == 0 {
		n, err := eventNumber()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		if n == 0 {
			return emit(o, stdout, stderr, []syncer.Result{{Result: syncer.ResultSkipped,
				Reason: "No issue or pull request in this event; use --issue or --all."}}, nil)
		}
		number = n
	}
	if !o.all && len(repos) != 1 {
		fmt.Fprintln(stderr, "error: --issue works with exactly one --repo; use --all for several repositories")
		return 2
	}

	api, err := newAPI()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	s := &syncer.Syncer{API: api, Opts: syncer.Options{
		ProjectOwner: o.projectOwner, ProjectNumber: o.projectNumber, Filter: cfg, DryRun: o.dryRun,
	}}
	ctx := context.Background()

	var results []syncer.Result
	var errs []error
	if o.all {
		for _, repo := range repos {
			r, e := s.SyncAll(ctx, repo)
			results, errs = append(results, r...), append(errs, e...)
		}
	} else {
		r, err := s.SyncOne(ctx, repos[0], number)
		if err != nil {
			errs = append(errs, err)
		} else {
			results = append(results, r)
		}
	}
	return emit(o, stdout, stderr, results, errs)
}

func (o options) validate() error {
	if o.projectOwner == "" {
		return errors.New("--project-owner is required")
	}
	if o.projectNumber < 1 {
		return errors.New("--project-number must be a positive integer")
	}
	if o.all && o.issue != 0 {
		return errors.New("--all and --issue cannot be combined")
	}
	if o.issue < 0 {
		return errors.New("--issue must be a positive integer")
	}
	return nil
}

func defaulted(v []string, def ...string) []string {
	if len(v) == 0 {
		return def
	}
	return v
}

func defaultRepo() (string, error) {
	for _, env := range []string{"GH_REPO", "GITHUB_REPOSITORY"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, nil
		}
	}
	return currentGitRepo()
}

// eventNumber reads the issue/PR number from the GitHub Actions event payload, or 0 if there is none.
func eventNumber() (int, error) {
	path := os.Getenv("GITHUB_EVENT_PATH")
	if path == "" {
		return 0, errors.New("--issue or --all is required outside GitHub Actions")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading event payload: %w", err)
	}
	var ev struct {
		Issue       *struct{ Number int } `json:"issue"`
		PullRequest *struct{ Number int } `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return 0, fmt.Errorf("parsing event payload: %w", err)
	}
	switch {
	case ev.Issue != nil:
		return ev.Issue.Number, nil
	case ev.PullRequest != nil:
		return ev.PullRequest.Number, nil
	}
	return 0, nil
}

func emit(o options, stdout, stderr io.Writer, results []syncer.Result, errs []error) int {
	if o.jsonOut {
		if results == nil {
			results = []syncer.Result{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
	} else {
		for _, r := range results {
			ref := "-"
			if r.Repo != "" {
				ref = r.Repo + "#" + strconv.Itoa(r.Number)
			}
			line := fmt.Sprintf("%-8s %s: %s", r.Result, ref, r.Reason)
			if r.ItemID != "" {
				line += " (" + r.ItemID + ")"
			}
			fmt.Fprintln(stdout, line)
		}
	}
	writeActionsOutputs(results)
	for _, e := range errs {
		fmt.Fprintln(stderr, "error:", e)
	}
	if len(errs) > 0 {
		return 1
	}
	return 0
}

// writeActionsOutputs exposes the last result as step outputs when running in GitHub Actions.
func writeActionsOutputs(results []syncer.Result) {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" || len(results) == 0 {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	last := results[len(results)-1]
	clean := func(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }
	fmt.Fprintf(f, "result=%s\nitem-id=%s\nreason=%s\n", last.Result, last.ItemID, clean(last.Reason))
}
