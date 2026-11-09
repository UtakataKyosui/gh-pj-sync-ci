package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakeIssue struct {
	Number int
	Title  string
	State  string
	Author string
	Bot    bool
	PR     bool
	Draft  bool
	Labels []string
}

// fakeGitHub is a small stateful GitHub: REST issues and pulls, and the GraphQL
// project queries and mutation the CLI uses.
type fakeGitHub struct {
	t        *testing.T
	mu       sync.Mutex
	repo     string
	issues   map[int]fakeIssue
	project  int
	closed   bool
	items    map[string]string // content ID -> project item ID
	adds     int
	auth     []string
	blockAll bool
	started  chan struct{}
	release  chan struct{}
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	f := &fakeGitHub{t: t, repo: "acme/web", issues: map[int]fakeIssue{}, project: 1, items: map[string]string{},
		started: make(chan struct{}, 16), release: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(f.release) }) // runs first: unblocks held requests
	return f, srv
}

var (
	issueRe = regexp.MustCompile(`^/repos/([^/]+/[^/]+)/(issues|pulls)/(\d+)$`)
	listRe  = regexp.MustCompile(`^/repos/([^/]+/[^/]+)/issues$`)
)

// The test goroutine and the server's handlers share the state, so it is only touched under mu.

func (f *fakeGitHub) setIssue(it fakeIssue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issues[it.Number] = it
}

func (f *fakeGitHub) setClosed(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = v
}

func (f *fakeGitHub) setBlocking() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockAll = true
}

func (f *fakeGitHub) addCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.adds
}

func (f *fakeGitHub) authHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

func (f *fakeGitHub) contentID(n int) string {
	if f.issues[n].PR {
		return fmt.Sprintf("PR_%d", n)
	}
	return fmt.Sprintf("I_%d", n)
}

func (f *fakeGitHub) issueJSON(it fakeIssue) map[string]any {
	labels := []map[string]string{}
	for _, l := range it.Labels {
		labels = append(labels, map[string]string{"name": l})
	}
	typ := "User"
	if it.Bot {
		typ = "Bot"
	}
	var pr any
	if it.PR {
		pr = map[string]string{"url": "x"}
	}
	return map[string]any{
		"number": it.Number, "node_id": fmt.Sprintf("I_%d", it.Number), "title": it.Title,
		"html_url": fmt.Sprintf("https://github.com/%s/issues/%d", f.repo, it.Number),
		"state":    it.State, "pull_request": pr, "user": map[string]string{"login": it.Author, "type": typ}, "labels": labels,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	f.mu.Lock()
	blocking := f.blockAll
	f.mu.Unlock()
	if blocking {
		// Read the body so the server notices when the client hangs up.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-f.release:
		}
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))

	if m := issueRe.FindStringSubmatch(r.URL.Path); m != nil {
		n, _ := strconv.Atoi(m[3])
		it, ok := f.issues[n]
		if m[1] != f.repo || !ok || (m[2] == "pulls" && !it.PR) {
			writeJSON(w, 404, map[string]string{"message": "Not Found"})
			return
		}
		if m[2] == "pulls" {
			writeJSON(w, 200, map[string]any{"node_id": f.contentID(n), "draft": it.Draft})
			return
		}
		writeJSON(w, 200, f.issueJSON(it))
		return
	}
	if m := listRe.FindStringSubmatch(r.URL.Path); m != nil {
		state := r.URL.Query().Get("state")
		out := []map[string]any{}
		if r.URL.Query().Get("page") == "1" {
			for n := 1; n <= 100; n++ {
				if it, ok := f.issues[n]; ok && (state == "all" || it.State == state) {
					out = append(out, f.issueJSON(it))
				}
			}
		}
		writeJSON(w, 200, out)
		return
	}
	if r.URL.Path == "/graphql" {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		switch {
		case strings.Contains(req.Query, "repositoryOwner"):
			if int(req.Variables["number"].(float64)) != f.project {
				writeJSON(w, 200, map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{"projectV2": nil}}})
				return
			}
			writeJSON(w, 200, map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{
				"projectV2": map[string]any{"id": "PVT_1", "title": "Roadmap", "url": "https://github.com/orgs/acme/projects/1", "closed": f.closed}}}})
		case strings.Contains(req.Query, "projectItems"):
			nodes := []map[string]any{}
			if id, ok := f.items[req.Variables["id"].(string)]; ok {
				nodes = append(nodes, map[string]any{"id": id, "project": map[string]string{"id": "PVT_1"}})
			}
			writeJSON(w, 200, map[string]any{"data": map[string]any{"node": map[string]any{"projectItems": map[string]any{"nodes": nodes}}}})
		case strings.Contains(req.Query, "addProjectV2ItemById"):
			content := req.Variables["contentId"].(string)
			id, ok := f.items[content]
			if !ok {
				id = "PVTI_" + content
				f.items[content] = id
				f.adds++
			}
			writeJSON(w, 200, map[string]any{"data": map[string]any{"addProjectV2ItemById": map[string]any{"item": map[string]string{"id": id}}}})
		default:
			f.t.Errorf("unexpected GraphQL query %s", req.Query)
			writeJSON(w, 400, map[string]string{"message": "bad query"})
		}
		return
	}
	f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
	writeJSON(w, 404, map[string]string{"message": "Not Found"})
}
