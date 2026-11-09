// Package ghapi implements syncer.API with go-gh (honours `gh auth` and GH_TOKEN/GITHUB_TOKEN).
package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/syncer"
)

// Client talks to the GitHub REST and GraphQL APIs.
type Client struct {
	REST    *api.RESTClient
	GraphQL *api.GraphQLClient
}

// New creates a client using the default gh authentication.
func New() (*Client, error) {
	rest, err := api.DefaultRESTClient()
	if err != nil {
		return nil, err
	}
	gql, err := api.DefaultGraphQLClient()
	if err != nil {
		return nil, err
	}
	return &Client{REST: rest, GraphQL: gql}, nil
}

type issuePayload struct {
	Number      int             `json:"number"`
	NodeID      string          `json:"node_id"`
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

// GetItem fetches an issue or pull request. Labels are read in full from the issue
// payload of the REST API, which returns all labels of the item.
func (c *Client) GetItem(_ context.Context, repo string, number int) (syncer.Item, error) {
	var issue issuePayload
	if err := c.REST.Get(fmt.Sprintf("repos/%s/issues/%d", repo, number), &issue); err != nil {
		return syncer.Item{}, err
	}
	it := syncer.Item{Number: number, ContentID: issue.NodeID}
	it.Type = filter.TypeIssue
	if len(issue.PullRequest) > 0 && string(issue.PullRequest) != "null" {
		it.Type = filter.TypePullRequest
	}
	it.State = issue.State
	if issue.User != nil {
		it.Author = issue.User.Login
		it.IsBot = issue.User.Type == "Bot"
	}
	for _, l := range issue.Labels {
		it.Labels = append(it.Labels, l.Name)
	}
	if it.Type == filter.TypePullRequest {
		var pull struct {
			NodeID string `json:"node_id"`
			Draft  bool   `json:"draft"`
		}
		if err := c.REST.Get(fmt.Sprintf("repos/%s/pulls/%d", repo, number), &pull); err != nil {
			return syncer.Item{}, err
		}
		it.ContentID, it.Draft = pull.NodeID, pull.Draft
	}
	return it, nil
}

// ListItemNumbers returns the numbers of issues and PRs in the given states, oldest first.
func (c *Client) ListItemNumbers(_ context.Context, repo string, states []string) ([]int, error) {
	state := "all"
	if len(states) == 1 {
		state = states[0]
	}
	var numbers []int
	for page := 1; ; page++ {
		var items []issuePayload
		path := fmt.Sprintf("repos/%s/issues?state=%s&sort=created&direction=asc&per_page=%d&page=%d", repo, state, perPage, page)
		if err := c.REST.Get(path, &items); err != nil {
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

// GetProject resolves a Projects v2 board owned by a user or an organization.
func (c *Client) GetProject(_ context.Context, owner string, number int) (syncer.Project, error) {
	var resp struct {
		RepositoryOwner *struct {
			ProjectV2 *struct {
				ID     string `json:"id"`
				Closed bool   `json:"closed"`
			} `json:"projectV2"`
		} `json:"repositoryOwner"`
	}
	const query = `query($owner: String!, $number: Int!) {
  repositoryOwner(login: $owner) {
    ... on Organization { projectV2(number: $number) { id closed } }
    ... on User { projectV2(number: $number) { id closed } }
  }
}`
	if err := c.GraphQL.Do(query, map[string]interface{}{"owner": owner, "number": number}, &resp); err != nil {
		return syncer.Project{}, err
	}
	if resp.RepositoryOwner == nil || resp.RepositoryOwner.ProjectV2 == nil {
		return syncer.Project{}, errors.New("project not found or token lacks access (projects need the `project` scope)")
	}
	p := resp.RepositoryOwner.ProjectV2
	return syncer.Project{ID: p.ID, Closed: p.Closed}, nil
}

// AddItem adds content to a project. The API returns the existing item ID when it is already
// present, so repeated or concurrent runs never create duplicates.
func (c *Client) AddItem(_ context.Context, projectID, contentID string) (string, error) {
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
	err := c.GraphQL.Do(mutation, map[string]interface{}{"projectId": projectID, "contentId": contentID}, &resp)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.AddProjectV2ItemByID.Item.ID), nil
}
