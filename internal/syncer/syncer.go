// Package syncer adds eligible issues and pull requests to a GitHub Project (v2), one way only.
package syncer

import (
	"context"
	"fmt"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
)

const (
	ResultSynced  = "synced"
	ResultSkipped = "skipped"
	ResultDryRun  = "dry-run"
)

// Item is a fetched issue or pull request.
type Item struct {
	filter.Item
	Number    int
	ContentID string // GraphQL node ID
}

// Project is the target Projects v2 board.
type Project struct {
	ID     string
	Closed bool
}

// API abstracts the GitHub calls so the sync logic can be tested.
type API interface {
	GetItem(ctx context.Context, repo string, number int) (Item, error)
	ListItemNumbers(ctx context.Context, repo string, states []string) ([]int, error)
	GetProject(ctx context.Context, owner string, number int) (Project, error)
	AddItem(ctx context.Context, projectID, contentID string) (string, error)
}

// Options configures a sync run.
type Options struct {
	ProjectOwner  string
	ProjectNumber int
	Filter        filter.Config
	DryRun        bool
}

// Result describes the outcome for one item.
type Result struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Type   string `json:"type,omitempty"`
	Result string `json:"result"`
	ItemID string `json:"item_id"`
	Reason string `json:"reason"`
}

// Syncer runs syncs against an API.
type Syncer struct {
	API  API
	Opts Options

	project *Project
}

func (s *Syncer) loadProject(ctx context.Context) (Project, error) {
	if s.project == nil {
		p, err := s.API.GetProject(ctx, s.Opts.ProjectOwner, s.Opts.ProjectNumber)
		if err != nil {
			return Project{}, err
		}
		s.project = &p
	}
	return *s.project, nil
}

// SyncOne checks a single item and adds it when it passes every condition.
// Eligibility is judged on fresh API data, never on cached event payloads.
func (s *Syncer) SyncOne(ctx context.Context, repo string, number int) (Result, error) {
	res := Result{Repo: repo, Number: number}
	item, err := s.API.GetItem(ctx, repo, number)
	if err != nil {
		return res, fmt.Errorf("%s#%d: %w", repo, number, err)
	}
	res.Type = item.Type
	if ok, reason := filter.Evaluate(s.Opts.Filter, item.Item); !ok {
		res.Result, res.Reason = ResultSkipped, reason
		return res, nil
	}
	project, err := s.loadProject(ctx)
	if err != nil {
		return res, err
	}
	if project.Closed {
		res.Result, res.Reason = ResultSkipped, "Target project is closed."
		return res, nil
	}
	if s.Opts.DryRun {
		res.Result, res.Reason = ResultDryRun, "Item passed all filters; no project changes were made."
		return res, nil
	}
	id, err := s.API.AddItem(ctx, project.ID, item.ContentID)
	if err != nil {
		return res, fmt.Errorf("%s#%d: %w", repo, number, err)
	}
	if id == "" {
		return res, fmt.Errorf("%s#%d: project API did not return an item ID", repo, number)
	}
	res.Result, res.ItemID, res.Reason = ResultSynced, id, "Item is present in the target project."
	return res, nil
}

// SyncAll checks every item of a repository whose state is allowed by the filter.
// Errors for individual items are recorded in errs; the run continues.
func (s *Syncer) SyncAll(ctx context.Context, repo string) (results []Result, errs []error) {
	numbers, err := s.API.ListItemNumbers(ctx, repo, s.Opts.Filter.States)
	if err != nil {
		return nil, []error{fmt.Errorf("%s: %w", repo, err)}
	}
	for _, n := range numbers {
		r, err := s.SyncOne(ctx, repo, n)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		results = append(results, r)
	}
	return results, errs
}
