package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
)

type ghapiItem = ghapi.Item

type httpErrForTest struct{ status int }

func (h *httpErrForTest) Error() string { return "boom" }
func (h *httpErrForTest) Unwrap() error {
	return &api.HTTPError{StatusCode: h.status, Message: "Bad gateway", Headers: http.Header{"X-Github-Request-Id": {"REQ1"}}}
}

var base = []string{"--owner", "acme", "--project", "1"}

func args(extra ...string) []string { return append(append([]string{"add"}, extra...), base...) }

func TestHelpAndVersionNeverTouchTheAPI(t *testing.T) {
	for _, a := range [][]string{
		{}, {"--help"}, {"-h"}, {"help"}, {"help", "add"}, {"add", "--help"}, {"add", "-h"},
		{"add", "--bogus", "--help"},        // help wins over invalid input
		{"add", "--owner", "x y", "--help"}, // and over validation
		{"--version"}, {"-v"}, {"add", "--version"},
	} {
		r := run(t, nil, env{noAPI: true}, a...)
		if r.code != 0 || r.stdout == "" || r.stderr != "" {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", a, r.code, r.stdout, r.stderr)
		}
	}
	if r := run(t, nil, env{noAPI: true}, "--version"); r.stdout != "v1.2.3\n" {
		t.Fatalf("%q", r.stdout)
	}
	if r := run(t, nil, env{noAPI: true}, "--version", "--json"); r.stdout != "{\"version\":\"v1.2.3\"}\n" {
		t.Fatalf("%q", r.stdout)
	}
}

func TestUnknownCommandAndHelpPath(t *testing.T) {
	r := run(t, nil, env{noAPI: true}, "ad")
	if r.code != 2 || !strings.Contains(r.stderr, "Unknown command ad for gh pj-synker. Did you mean add?") || r.stdout != "" {
		t.Fatalf("%+v", r)
	}
	r = run(t, nil, env{noAPI: true}, "help", "nope")
	if r.code != 2 || !strings.Contains(r.stderr, "Unknown command nope") {
		t.Fatalf("%+v", r)
	}
	r = run(t, nil, env{noAPI: true}, "--bogus")
	if r.code != 2 || !strings.Contains(r.stderr, "Unknown option --bogus for gh pj-synker.") {
		t.Fatalf("%+v", r)
	}
}

func TestValidationHappensBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"missing owner":       {"add", "1", "--project", "1"},
		"missing project":     {"add", "1", "--owner", "acme"},
		"bad project":         {"add", "1", "--owner", "acme", "--project", "0"},
		"bad state":           args("1", "--state", "merged"),
		"bad repo":            args("1", "--repo", "nope"),
		"all with numbers":    args("1", "--all"),
		"numbers, many repos": args("1", "--repo", "a/b,c/d"),
		"no number":           args(),
		"bad number":          args("abc"),
	}
	for name, a := range cases {
		r := run(t, nil, env{noAPI: true, repo: "acme/web", vars: map[string]string{}}, a...)
		if r.code != exitUsage || r.stdout != "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestInvalidRepoFromEnvironmentFailsInsteadOfFallingBack(t *testing.T) {
	e := env{noAPI: true, repo: "acme/web", vars: map[string]string{"GH_REPO": "not a repo"}}
	r := run(t, nil, e, args("1")...)
	if r.code != exitUsage || !strings.Contains(r.stderr, `"not a repo" must look like OWNER/REPO.`) {
		t.Fatalf("%+v", r)
	}
}

func TestRepositoryResolutionOrder(t *testing.T) {
	f := newFake()
	for _, repo := range []string{"flag/repo", "env/repo", "git/repo", "actions/repo"} {
		f.put(repo, 1, nil)
	}
	tests := []struct {
		name   string
		vars   map[string]string
		git    string
		extra  []string
		want   string
		source string
	}{
		{"flag wins", map[string]string{"GH_REPO": "env/repo"}, "git/repo", []string{"--repo", "flag/repo"}, "flag/repo", ""},
		{"GH_REPO", map[string]string{"GH_REPO": "github.com/env/repo", "GITHUB_REPOSITORY": "actions/repo"}, "git/repo", nil, "env/repo", "GH_REPO"},
		{"git remote", map[string]string{"GITHUB_REPOSITORY": "actions/repo"}, "git/repo", nil, "git/repo", "git remote"},
		{"GITHUB_REPOSITORY", map[string]string{"GITHUB_REPOSITORY": "actions/repo"}, "", nil, "actions/repo", "GITHUB_REPOSITORY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := env{vars: tt.vars, repo: tt.git}
			r := run(t, f, e, args(append([]string{"1"}, tt.extra...)...)...)
			if r.code != 0 || !strings.Contains(r.stderr, "Repository      "+tt.want+"\n") {
				t.Fatalf("%+v", r)
			}
			hasSource := strings.Contains(r.stderr, "Source          "+tt.source+"\n")
			if (tt.source != "") != hasSource || (tt.source == "" && strings.Contains(r.stderr, "Source")) {
				t.Fatalf("source row: %s", r.stderr)
			}
		})
	}
	r := run(t, nil, env{noAPI: true, vars: map[string]string{}}, args("1")...)
	if r.code != exitUsage || !strings.Contains(r.stderr, "Can't add items without --repo.") {
		t.Fatalf("%+v", r)
	}
}

func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("invalid JSON %q: %v", s, err)
	}
	if dec.More() {
		t.Fatalf("more than one JSON document in %q", s)
	}
	return doc
}

func TestJSONContract(t *testing.T) {
	f := mixedFake()
	r := run(t, f, ci(), args("--all", "--yes", "--json", "--label", "triaged", "--exclude-label", "blocked")...)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	if strings.Count(r.stdout, "\n") != 1 {
		t.Fatalf("expected one compact line on a pipe: %q", r.stdout)
	}
	doc := decodeJSON(t, r.stdout)
	summary := doc["summary"].(map[string]any)
	want := map[string]float64{"added": 2, "unchanged": 1, "planned": 0, "skipped": 4, "failed": 0}
	for k, v := range want {
		if summary[k] != v {
			t.Fatalf("summary %v", summary)
		}
	}
	results := doc["results"].([]any)
	first := results[0].(map[string]any)
	for _, key := range []string{"repository", "number", "type", "title", "url", "outcome", "projectItemId", "reason", "message"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("result is missing %q: %v", key, first)
		}
	}
	if first["reason"] != nil || first["outcome"] != "added" {
		t.Fatalf("%v", first)
	}
	if doc["dryRun"] != false || doc["project"].(map[string]any)["id"] != "PVT_1" {
		t.Fatalf("%v", doc)
	}

	// On a TTY the document is indented.
	e := tty()
	r = run(t, mixedFake(), e, args("1", "--json")...)
	if !strings.Contains(r.stdout, "\n  \"dryRun\"") {
		t.Fatalf("expected indentation on a TTY: %q", r.stdout)
	}
}

func TestJSONErrorIsOneLineOnStderr(t *testing.T) {
	r := run(t, newFake(), ci(), args("404", "--json")...)
	if r.code != exitFailure || r.stdout != "" || strings.Count(r.stderr, "\n") != 1 {
		t.Fatalf("%+v", r)
	}
	doc := decodeJSON(t, r.stderr)
	if doc["status"] != "error" || doc["reason"] != "not_found" {
		t.Fatalf("%v", doc)
	}
	next := doc["next"].([]any)[0].(map[string]any)
	if next["command"] != "gh issue list --repo acme/web" {
		t.Fatalf("%v", next)
	}
}

func TestConsentForAll(t *testing.T) {
	filters := []string{"--all", "--label", "triaged", "--exclude-label", "blocked"}

	t.Run("non-interactive needs --yes and changes nothing", func(t *testing.T) {
		f := mixedFake()
		r := run(t, f, ci(), args(append(filters, "--json")...)...)
		if r.code != exitUsage || r.stdout != "" || len(f.added) != 0 {
			t.Fatalf("%+v added=%v", r, f.added)
		}
		doc := decodeJSON(t, r.stderr)
		if doc["reason"] != "confirmation_required" || doc["status"] != "action_required" || doc["userActionRequired"] != true {
			t.Fatalf("%v", doc)
		}
		if n := len(doc["data"].(map[string]any)["effects"].([]any)); n != 2 {
			t.Fatalf("effects %d", n)
		}
		next := doc["next"].([]any)[0].(map[string]any)
		if !strings.Contains(next["command"].(string), "--yes") || !strings.HasPrefix(next["when"].(string), "Only after a human approves") {
			t.Fatalf("%v", next)
		}
	})

	t.Run("interactive yes adds", func(t *testing.T) {
		f := mixedFake()
		e := noColorTTY()
		e.stdin = "y\n"
		r := run(t, f, e, args(filters...)...)
		if r.code != 0 || len(f.added) != 2 || !strings.Contains(r.stderr, "? Add 2 items to project acme/1? (y/N) ") {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("interactive no cancels", func(t *testing.T) {
		f := mixedFake()
		e := noColorTTY()
		e.stdin = "\n"
		r := run(t, f, e, args(filters...)...)
		if r.code != exitCanceled || len(f.added) != 0 || !strings.Contains(r.stderr, "Canceled. Nothing was changed.") {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("explicit numbers need no confirmation", func(t *testing.T) {
		f := mixedFake()
		r := run(t, f, ci(), args("1", "6")...)
		if r.code != 0 || len(f.added) != 2 {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("dry run never asks or adds", func(t *testing.T) {
		f := mixedFake()
		r := run(t, f, ci(), args(append(filters, "--dry-run")...)...)
		if r.code != 0 || len(f.added) != 0 {
			t.Fatalf("%+v", r)
		}
		for _, c := range f.calls {
			if c == "AddItem" {
				t.Fatal("dry run called AddItem")
			}
		}
	})
}

func TestStdoutIdentifiers(t *testing.T) {
	f := mixedFake()
	r := run(t, f, ci(), args("1", "7")...)
	if r.stdout != "PVTI_I_acme_web_1\nPVTI_existing\n" {
		t.Fatalf("%q", r.stdout)
	}
	// On an interactive TTY the receipt shows the ID instead.
	r = run(t, mixedFake(), noColorTTY(), args("1")...)
	if r.stdout != "" || !strings.Contains(r.stderr, "Item ID         PVTI_I_acme_web_1") {
		t.Fatalf("%+v", r)
	}
	// Nothing on stdout when the run fails.
	f = mixedFake()
	f.itemErr[1] = &httpErrForTest{status: 502}
	r = run(t, f, ci(), args("1", "7")...)
	if r.code != exitFailure || r.stdout != "PVTI_existing\n" && r.stdout != "" {
		t.Fatalf("%+v", r)
	}
}

func TestProjectErrors(t *testing.T) {
	f := newFake()
	f.put("acme/web", 1, nil)
	f.projectErr = ghapi.ErrProjectNotFound
	r := run(t, f, ci(), args("1", "--json")...)
	doc := decodeJSON(t, r.stderr)
	if r.code != exitFailure || doc["reason"] != "project_not_found" {
		t.Fatalf("%+v", r)
	}
	for _, c := range f.calls {
		if c != "GetProject" {
			t.Fatalf("items were read before the project resolved: %v", f.calls)
		}
	}

	f = newFake()
	f.put("acme/web", 1, nil)
	f.project.Closed = true
	r = run(t, f, ci(), args("1")...)
	if r.code != exitFailure || !strings.Contains(r.stderr, "Can't add items to project acme/1. The project is closed.") || len(f.added) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestEveryItemFailingTheSameWayIsOneError(t *testing.T) {
	f := newFake()
	r := run(t, f, ci(), args("8", "9")...)
	if r.code != exitFailure || !strings.Contains(r.stderr, "Error: Couldn't find issue or pull request 8 in acme/web.") {
		t.Fatalf("%+v", r)
	}
}

func TestUnknownOutcomeOnAdd(t *testing.T) {
	f := newFake()
	f.put("acme/web", 1, nil)
	f.addErr = &httpErrForTest{status: 502}
	r := run(t, f, ci(), args("1", "--json")...)
	doc := decodeJSON(t, r.stderr)
	if r.code != exitFailure || doc["reason"] != "unknown_outcome" || doc["retryable"] != true {
		t.Fatalf("%+v", r)
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := mixedFake()
	e := ci()
	e.ctx = ctx
	f.projectErr = context.Canceled
	r := run(t, f, e, args("1")...)
	if r.code != exitCanceled || !strings.Contains(r.stderr, "Canceled. Nothing was changed.") || len(f.added) != 0 {
		t.Fatalf("%+v", r)
	}
	e.ctx = ctx
	r = run(t, f, e, args("1", "--json")...)
	if r.code != exitCanceled || r.stdout != "" || decodeJSON(t, r.stderr)["reason"] != "canceled" {
		t.Fatalf("%+v", r)
	}
}

func TestRemoteTextIsSanitized(t *testing.T) {
	f := newFake()
	f.put("acme/web", 1, func(i *ghapi.Item) { i.Title = "Evil \x1b]0;pwned\x07title\u202e" })
	r := run(t, f, noColorTTY(), args("1")...)
	_, title, _ := strings.Cut(r.stderr, "  Title")
	title, _, _ = strings.Cut("Title"+title, "\n")
	if title != "Title           Evil ]0;pwnedtitle" {
		t.Fatalf("%q", title)
	}
}

func TestNarrowTerminalTruncatesOnlyDescriptions(t *testing.T) {
	f := newFake()
	f.put("acme/web", 1, func(i *ghapi.Item) { i.Title = strings.Repeat("long ", 30) })
	e := noColorTTY()
	e.width = 40
	r := run(t, f, e, args("1")...)
	for _, line := range strings.Split(r.stderr, "\n") {
		if strings.HasPrefix(line, "  Title") && len([]rune(line)) > 40 {
			t.Fatalf("title not truncated: %q", line)
		}
	}
	if !strings.Contains(r.stderr, "PVTI_I_acme_web_1") {
		t.Fatal("IDs must never be truncated")
	}
}

func TestAPIErrorMapping(t *testing.T) {
	getenv := func(string) string { return "" }
	ctx := apiContext{verb: "read", object: "issue or pull request 1 in a/b"}
	hdr := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	tests := []struct {
		name   string
		err    error
		c      apiContext
		reason string
		exit   int
		msg    string
	}{
		{"401", &api.HTTPError{StatusCode: 401, Headers: hdr()}, ctx, "token_invalid", 1, "GitHub didn't accept the token."},
		{"403", &api.HTTPError{StatusCode: 403, Message: "Resource not accessible", Headers: hdr()}, ctx, "permission_denied", 1,
			"This token can't read issue or pull request 1 in a/b."},
		{"rate limit 403", &api.HTTPError{StatusCode: 403, Headers: hdr("X-RateLimit-Remaining", "0")}, ctx, "rate_limited", 1, ""},
		{"429", &api.HTTPError{StatusCode: 429, Headers: hdr("Retry-After", "120")}, ctx, "rate_limited", 1,
			"Too many requests to the GitHub API. Try again in 2 minutes."},
		{"404", &api.HTTPError{StatusCode: 404, Headers: hdr()}, ctx, "not_found", 1, "Couldn't find issue or pull request 1 in a/b."},
		{"500 read", &api.HTTPError{StatusCode: 500, Headers: hdr()}, ctx, "api_error", 1,
			"Failed to read issue or pull request 1 in a/b. GitHub returned status 500."},
		{"500 mutation", &api.HTTPError{StatusCode: 500, Headers: hdr()}, apiContext{verb: "add", object: "a/b#1", mutation: true},
			"unknown_outcome", 1, "Can't confirm whether a/b#1 was added. The request didn't complete."},
		{"scopes", &api.GraphQLError{Errors: []api.GraphQLErrorItem{{Type: "INSUFFICIENT_SCOPES", Message: "needs read:project"}}}, ctx,
			"token_scope_required", 1, "This token can't read issue or pull request 1 in a/b. It needs the project scope."},
		{"graphql not found", &api.GraphQLError{Errors: []api.GraphQLErrorItem{{Type: "NOT_FOUND"}}}, ctx, "not_found", 1, ""},
		{"no token", errors.New("authentication token not found for host github.com"), ctx, "login_required", 1,
			"You're not logged in to GitHub."},
		{"other", errors.New("weird"), ctx, "api_error", 1, "Failed to read issue or pull request 1 in a/b."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := mapAPIError(tt.err, tt.c, getenv)
			if e.Reason != tt.reason || e.Exit != tt.exit || (tt.msg != "" && e.Message != tt.msg) {
				t.Fatalf("got %+v", e)
			}
			if strings.Contains(e.Message, "Resource not accessible") || strings.Contains(e.Message, "needs read:project") {
				t.Fatal("remote messages must stay out of the headline")
			}
		})
	}
}
