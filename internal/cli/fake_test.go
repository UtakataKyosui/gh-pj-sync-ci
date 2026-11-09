package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
)

// fakeAPI is an in-memory GitHub used by the CLI tests.
type fakeAPI struct {
	mu         sync.Mutex
	project    ghapi.Project
	projectErr error
	items      map[string]map[int]ghapi.Item // repo -> number -> item
	itemErr    map[int]error
	inProject  map[string]string // content ID -> project item ID
	addErr     error
	listErr    error
	added      []string
	calls      []string
}

func newFake() *fakeAPI {
	return &fakeAPI{
		project:   ghapi.Project{ID: "PVT_1", Title: "Roadmap", URL: "https://github.com/orgs/acme/projects/1"},
		items:     map[string]map[int]ghapi.Item{"acme/web": {}},
		itemErr:   map[int]error{},
		inProject: map[string]string{},
	}
}

func (f *fakeAPI) put(repo string, n int, mod func(*ghapi.Item)) {
	it := ghapi.Item{
		Repository: repo, Number: n, ContentID: "I_" + strings.ReplaceAll(repo, "/", "_") + "_" + itoa(n),
		Title: "Item " + itoa(n), URL: "https://github.com/" + repo + "/issues/" + itoa(n),
		Item: filter.Item{Type: filter.TypeIssue, State: filter.StateOpen, Author: "octocat", Labels: []string{"triaged"}},
	}
	if mod != nil {
		mod(&it)
	}
	if f.items[repo] == nil {
		f.items[repo] = map[int]ghapi.Item{}
	}
	f.items[repo][n] = it
}

func itoa(n int) string { return strconv.Itoa(n) }

func notFoundErr() error {
	return &api.HTTPError{StatusCode: 404, Message: "Not Found", Headers: http.Header{}}
}

func (f *fakeAPI) call(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeAPI) GetProject(context.Context, string, int) (ghapi.Project, error) {
	f.call("GetProject")
	return f.project, f.projectErr
}
func (f *fakeAPI) ListItemNumbers(_ context.Context, repo string, states []string) ([]int, error) {
	f.call("ListItemNumbers")
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []int
	for n := 1; n <= 1000; n++ {
		if it, ok := f.items[repo][n]; ok {
			for _, s := range states {
				if s == it.State {
					out = append(out, n)
				}
			}
		}
	}
	return out, nil
}
func (f *fakeAPI) GetItem(_ context.Context, repo string, n int) (ghapi.Item, error) {
	f.call("GetItem")
	if err := f.itemErr[n]; err != nil {
		return ghapi.Item{}, err
	}
	it, ok := f.items[repo][n]
	if !ok {
		return ghapi.Item{}, notFoundErr()
	}
	return it, nil
}
func (f *fakeAPI) FindProjectItem(_ context.Context, _, content string) (string, error) {
	f.call("FindProjectItem")
	return f.inProject[content], nil
}
func (f *fakeAPI) AddItem(_ context.Context, _, content string) (string, error) {
	f.call("AddItem")
	if f.addErr != nil {
		return "", f.addErr
	}
	f.added = append(f.added, content)
	return "PVTI_" + content, nil
}

// env describes the terminal a test runs in.
type env struct {
	vars   map[string]string
	inTTY  bool
	outTTY bool
	errTTY bool
	stdin  string
	width  int
	ctx    context.Context
	noAPI  bool // fail the test if the API is created
	repo   string
	cwd    string // where the config file search starts; "" searches nothing
}

type result struct {
	code   int
	stdout string
	stderr string
}

func run(t *testing.T, f *fakeAPI, e env, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	vars := e.vars
	s := Streams{
		In: strings.NewReader(e.stdin), Out: &out, Err: &errb,
		InTTY: e.inTTY, OutTTY: e.outTTY, ErrTTY: e.errTTY, Width: e.width,
		Getenv: func(k string) string { return vars[k] },
	}
	d := Deps{
		NewAPI: func() (API, error) {
			if e.noAPI {
				t.Fatalf("the API must not be created for %v", args)
			}
			if f == nil {
				return nil, errors.New("no fake")
			}
			return f, nil
		},
		CurrentRepo: func() (string, error) {
			if e.repo == "" {
				return "", errors.New("not a git repository")
			}
			return e.repo, nil
		},
		Getwd:   func() (string, error) { return e.cwd, nil },
		Version: "v1.2.3",
	}
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	code := Run(ctx, args, s, d)
	return result{code, out.String(), errb.String()}
}

// tty is an interactive terminal with color.
func tty() env {
	return env{inTTY: true, outTTY: true, errTTY: true, width: 80, vars: map[string]string{"TERM": "xterm-256color"}, repo: "acme/web"}
}

// ci is a non-interactive run with pipes, as in GitHub Actions.
func ci() env {
	return env{width: 0, vars: map[string]string{"CI": "true", "GITHUB_REPOSITORY": "acme/web"}}
}
