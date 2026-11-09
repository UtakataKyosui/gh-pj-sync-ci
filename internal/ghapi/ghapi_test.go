package ghapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c, err := NewWithOptions(api.ClientOptions{Host: "github.com", AuthToken: "test", Transport: Redirect{Target: u}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestGetItemIssue(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/issues/3" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "token test" {
			t.Errorf("auth header %q", got)
		}
		w.Write([]byte(`{"number":3,"node_id":"I_3","title":"Fix it","html_url":"https://github.com/o/r/issues/3","state":"open","pull_request":null,
			"user":{"login":"Alice","type":"User"},"labels":[{"name":"Triaged"},{"name":"bug"}]}`))
	})
	it, err := c.GetItem(context.Background(), "o/r", 3)
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "Fix it" || it.URL == "" || it.Repository != "o/r" {
		t.Fatalf("%+v", it)
	}
	if it.Type != "issue" || it.ContentID != "I_3" || it.Author != "Alice" || it.IsBot || len(it.Labels) != 2 {
		t.Fatalf("%+v", it)
	}
}

func TestGetItemPullRequestUsesPullNodeAndDraft(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/issues/7":
			w.Write([]byte(`{"number":7,"node_id":"I_7","state":"open","pull_request":{"url":"x"},
				"user":{"login":"dependabot[bot]","type":"Bot"},"labels":[]}`))
		case "/repos/o/r/pulls/7":
			w.Write([]byte(`{"node_id":"PR_7","draft":true}`))
		default:
			http.NotFound(w, r)
		}
	})
	it, err := c.GetItem(context.Background(), "o/r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if it.Type != "pull-request" || it.ContentID != "PR_7" || !it.Draft || !it.IsBot {
		t.Fatalf("%+v", it)
	}
}

func TestGetItemNotFound(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	})
	if _, err := c.GetItem(context.Background(), "o/r", 1); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

func TestListItemNumbersPaginates(t *testing.T) {
	var pages []string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pages = append(pages, q.Get("page"))
		if q.Get("state") != "open" || q.Get("per_page") != "100" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		n := 100
		if q.Get("page") == "2" {
			n = 1
		}
		items := make([]map[string]int, n)
		for i := range items {
			items[i] = map[string]int{"number": i + 1}
		}
		writeJSON(w, items)
	})
	got, err := c.ListItemNumbers(context.Background(), "o/r", []string{"open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 101 || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("len=%d pages=%v", len(got), pages)
	}
}

func TestListItemNumbersBothStatesUsesAll(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if s := r.URL.Query().Get("state"); s != "all" {
			t.Errorf("state=%s", s)
		}
		writeJSON(w, []any{})
	})
	if _, err := c.ListItemNumbers(context.Background(), "o/r", []string{"open", "closed"}); err != nil {
		t.Fatal(err)
	}
}

func graphqlHandler(t *testing.T, respond func(query string, vars map[string]any) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Errorf("path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respond(req.Query, req.Variables)))
	}
}

func TestGetProject(t *testing.T) {
	c := newClient(t, graphqlHandler(t, func(q string, v map[string]any) string {
		if v["owner"] != "my-org" || v["number"] != float64(4) {
			t.Errorf("vars %v", v)
		}
		return `{"data":{"repositoryOwner":{"projectV2":{"id":"PVT_1","title":"Roadmap","closed":true}}}}`
	}))
	p, err := c.GetProject(context.Background(), "my-org", 4)
	if err != nil || p.ID != "PVT_1" || p.Title != "Roadmap" || !p.Closed {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestGetProjectMissing(t *testing.T) {
	c := newClient(t, graphqlHandler(t, func(string, map[string]any) string {
		return `{"data":{"repositoryOwner":{}}}`
	}))
	if _, err := c.GetProject(context.Background(), "x", 1); err != ErrProjectNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestGetProjectGraphQLError(t *testing.T) {
	c := newClient(t, graphqlHandler(t, func(string, map[string]any) string {
		return `{"data":null,"errors":[{"message":"Resource not accessible by integration"}]}`
	}))
	_, err := c.GetProject(context.Background(), "x", 1)
	if err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddItem(t *testing.T) {
	c := newClient(t, graphqlHandler(t, func(q string, v map[string]any) string {
		if !strings.Contains(q, "addProjectV2ItemById") || v["projectId"] != "PVT_1" || v["contentId"] != "I_3" {
			t.Errorf("q=%s v=%v", q, v)
		}
		return `{"data":{"addProjectV2ItemById":{"item":{"id":"PVTI_9"}}}}`
	}))
	id, err := c.AddItem(context.Background(), "PVT_1", "I_3")
	if err != nil || id != "PVTI_9" {
		t.Fatalf("%q %v", id, err)
	}
}

func TestFindProjectItem(t *testing.T) {
	c := newClient(t, graphqlHandler(t, func(q string, v map[string]any) string {
		if v["id"] != "I_3" || !strings.Contains(q, "includeArchived: true") {
			t.Errorf("q=%s v=%v", q, v)
		}
		return `{"data":{"node":{"projectItems":{"nodes":[
			{"id":"PVTI_other","project":{"id":"PVT_2"}},
			{"id":"PVTI_mine","project":{"id":"PVT_1"}}]}}}}`
	}))
	id, err := c.FindProjectItem(context.Background(), "PVT_1", "I_3")
	if err != nil || id != "PVTI_mine" {
		t.Fatalf("%q %v", id, err)
	}
	id, err = c.FindProjectItem(context.Background(), "PVT_9", "I_3")
	if err != nil || id != "" {
		t.Fatalf("%q %v", id, err)
	}
}

func TestAddItemWithoutIDFails(t *testing.T) {
	c := newClient(t, graphqlHandler(t, func(string, map[string]any) string {
		return `{"data":{"addProjectV2ItemById":{"item":null}}}`
	}))
	if _, err := c.AddItem(context.Background(), "P", "C"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewRejectsNonLoopbackTestURL(t *testing.T) {
	t.Setenv(TestAPIEnv, "https://evil.example.com")
	if _, err := New(); err == nil {
		t.Fatal("expected error")
	}
	t.Setenv(TestAPIEnv, "http://127.0.0.1:1")
	if _, err := New(); err != nil {
		t.Fatal(err)
	}
}
