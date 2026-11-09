// Package ghapi talks to the GitHub REST and GraphQL APIs through go-gh,
// which honours `gh auth login`, GH_TOKEN, GITHUB_TOKEN and GH_HOST.
package ghapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
)

// TestAPIEnv is an internal variable used by end-to-end tests. When set to a
// loopback http URL, every API request is sent there instead. It is never documented.
const TestAPIEnv = "__GH_PJ_SYNKER_API_URL"

// Item is a fetched issue or pull request.
type Item struct {
	filter.Item
	Repository string
	Number     int
	Title      string
	URL        string
	ContentID  string // GraphQL node ID of the issue or pull request
}

// Project is a Projects (v2) board.
type Project struct {
	ID     string
	Title  string
	URL    string
	Closed bool
}

// ErrProjectNotFound means the owner has no such project, or the token can't see it.
var ErrProjectNotFound = fmt.Errorf("project not found")

// Client implements the calls the CLI needs.
type Client struct {
	REST    *api.RESTClient
	GraphQL *api.GraphQLClient
}

// New creates a client with the default gh authentication.
func New() (*Client, error) {
	opts := api.ClientOptions{Timeout: 30 * time.Second}
	if raw := os.Getenv(TestAPIEnv); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
			return nil, fmt.Errorf("%s must be a loopback http URL", TestAPIEnv)
		}
		opts.Host, opts.AuthToken, opts.Transport = "github.com", "test-token", Redirect{Target: u}
	}
	return NewWithOptions(opts)
}

// NewWithOptions creates a client with explicit options.
func NewWithOptions(opts api.ClientOptions) (*Client, error) {
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, err
	}
	gql, err := api.NewGraphQLClient(opts)
	if err != nil {
		return nil, err
	}
	return &Client{REST: rest, GraphQL: gql}, nil
}

// Redirect sends every request to Target, keeping the path and query. Used by tests.
type Redirect struct{ Target *url.URL }

// RoundTrip implements http.RoundTripper.
func (r Redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.Target.Scheme, r.Target.Host
	return http.DefaultTransport.RoundTrip(req)
}

type issuePayload struct {
	Number      int             `json:"number"`
	NodeID      string          `json:"node_id"`
	Title       string          `json:"title"`
	HTMLURL     string          `json:"html_url"`
	State       string          `json:"state"`
	PullRequest json.RawMessage `json:"pull_request"`
	User        *struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

const perPage = 100

// GetItem fetches an issue or pull request. The REST issue payload lists every label.
func (c *Client) GetItem(ctx context.Context, repo string, number int) (Item, error) {
	var issue issuePayload
	if err := c.REST.DoWithContext(ctx, http.MethodGet, fmt.Sprintf("repos/%s/issues/%d", repo, number), nil, &issue); err != nil {
		return Item{}, err
	}
	it := Item{Repository: repo, Number: number, Title: issue.Title, URL: issue.HTMLURL, ContentID: issue.NodeID}
	it.Type = filter.TypeIssue
	if len(issue.PullRequest) > 0 && string(issue.PullRequest) != "null" {
		it.Type = filter.TypePullRequest
	}
	it.State = issue.State
	if issue.User != nil {
		it.Author, it.IsBot = issue.User.Login, issue.User.Type == "Bot"
	}
	for _, l := range issue.Labels {
		it.Labels = append(it.Labels, l.Name)
	}
	if it.Type == filter.TypePullRequest {
		var pull struct {
			NodeID string `json:"node_id"`
			Draft  bool   `json:"draft"`
		}
		if err := c.REST.DoWithContext(ctx, http.MethodGet, fmt.Sprintf("repos/%s/pulls/%d", repo, number), nil, &pull); err != nil {
			return Item{}, err
		}
		it.ContentID, it.Draft = pull.NodeID, pull.Draft
	}
	return it, nil
}

// ListItemNumbers returns the numbers of issues and pull requests in the given states, oldest first.
func (c *Client) ListItemNumbers(ctx context.Context, repo string, states []string) ([]int, error) {
	state := "all"
	if len(states) == 1 {
		state = states[0]
	}
	var numbers []int
	for page := 1; ; page++ {
		var items []issuePayload
		path := fmt.Sprintf("repos/%s/issues?state=%s&sort=created&direction=asc&per_page=%d&page=%d", repo, state, perPage, page)
		if err := c.REST.DoWithContext(ctx, http.MethodGet, path, nil, &items); err != nil {
			return nil, err
		}
		for _, it := range items {
			numbers = append(numbers, it.Number)
		}
		if len(items) < perPage {
			return numbers, nil
		}
	}
}

// GetProject resolves a project owned by a user or an organization.
func (c *Client) GetProject(ctx context.Context, owner string, number int) (Project, error) {
	var resp struct {
		RepositoryOwner *struct {
			ProjectV2 *struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				URL    string `json:"url"`
				Closed bool   `json:"closed"`
			} `json:"projectV2"`
		} `json:"repositoryOwner"`
	}
	const query = `query($owner: String!, $number: Int!) {
  repositoryOwner(login: $owner) {
    ... on Organization { projectV2(number: $number) { id title url closed } }
    ... on User { projectV2(number: $number) { id title url closed } }
  }
}`
	err := c.GraphQL.DoWithContext(ctx, query, map[string]any{"owner": owner, "number": number}, &resp)
	if err != nil {
		return Project{}, err
	}
	if resp.RepositoryOwner == nil || resp.RepositoryOwner.ProjectV2 == nil {
		return Project{}, ErrProjectNotFound
	}
	p := resp.RepositoryOwner.ProjectV2
	return Project{ID: p.ID, Title: p.Title, URL: p.URL, Closed: p.Closed}, nil
}

// FindProjectItem returns the ID of the content's item in the project, or "" when it isn't there.
// Only the first 100 project memberships of the content are checked; adding stays idempotent either way.
func (c *Client) FindProjectItem(ctx context.Context, projectID, contentID string) (string, error) {
	type items struct {
		Nodes []struct {
			ID      string `json:"id"`
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
		} `json:"nodes"`
	}
	var resp struct {
		Node *struct {
			ProjectItems *items `json:"projectItems"`
		} `json:"node"`
	}
	const query = `query($id: ID!) {
  node(id: $id) {
    ... on Issue { projectItems(first: 100, includeArchived: true) { nodes { id project { id } } } }
    ... on PullRequest { projectItems(first: 100, includeArchived: true) { nodes { id project { id } } } }
  }
}`
	if err := c.GraphQL.DoWithContext(ctx, query, map[string]any{"id": contentID}, &resp); err != nil {
		return "", err
	}
	if resp.Node == nil || resp.Node.ProjectItems == nil {
		return "", nil
	}
	for _, n := range resp.Node.ProjectItems.Nodes {
		if n.Project.ID == projectID {
			return n.ID, nil
		}
	}
	return "", nil
}

// AddItem adds content to a project. The API returns the existing item when it is already
// there, so repeated or concurrent runs never create duplicates.
func (c *Client) AddItem(ctx context.Context, projectID, contentID string) (string, error) {
	var resp struct {
		AddProjectV2ItemByID struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"addProjectV2ItemById"`
	}
	const mutation = `mutation($projectId: ID!, $contentId: ID!) {
  addProjectV2ItemById(input: {projectId: $projectId, contentId: $contentId}) { item { id } }
}`
	err := c.GraphQL.DoWithContext(ctx, mutation, map[string]any{"projectId": projectID, "contentId": contentID}, &resp)
	if err != nil {
		return "", err
	}
	if resp.AddProjectV2ItemByID.Item.ID == "" {
		return "", fmt.Errorf("the API didn't return a project item ID")
	}
	return resp.AddProjectV2ItemByID.Item.ID, nil
}
