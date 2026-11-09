// Package syncer plans and applies one-way additions of issues and pull requests to a project.
// Planning only reads; Apply is the only step that changes anything.
package syncer

import (
	"context"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
)

// Outcome of one item.
type Outcome string

const (
	Pending   Outcome = "pending"   // eligible, not added yet (only between Plan and Apply)
	Added     Outcome = "added"     // added by this run
	Unchanged Outcome = "unchanged" // already in the project
	Planned   Outcome = "planned"   // would be added (dry run)
	Skipped   Outcome = "skipped"   // didn't pass the conditions
	Failed    Outcome = "failed"    // an API call failed
)

// API is the subset of the GitHub client the syncer uses.
type API interface {
	GetItem(ctx context.Context, repo string, number int) (ghapi.Item, error)
	FindProjectItem(ctx context.Context, projectID, contentID string) (string, error)
	AddItem(ctx context.Context, projectID, contentID string) (string, error)
}

// Result is the state of one item.
type Result struct {
	Item          ghapi.Item
	Outcome       Outcome
	ProjectItemID string
	Reason        string // skip reason code
	Message       string // skip reason sentence
	Err           error
	Mutating      bool // Err came from the add request, so the item may have been added
}

// Ref is an item reference.
type Ref struct {
	Repository string
	Number     int
}

// Plan fetches and checks every item, and finds the ones already in the project.
// It never changes anything. It stops early when ctx is canceled.
func Plan(ctx context.Context, api API, project ghapi.Project, cfg filter.Config, refs []Ref) []Result {
	results := make([]Result, 0, len(refs))
	for _, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		r := Result{Item: ghapi.Item{Repository: ref.Repository, Number: ref.Number}}
		item, err := api.GetItem(ctx, ref.Repository, ref.Number)
		if err != nil {
			r.Outcome, r.Err = Failed, err
			results = append(results, r)
			continue
		}
		r.Item = item
		if d := filter.Evaluate(cfg, item.Item); !d.OK {
			r.Outcome, r.Reason, r.Message = Skipped, d.Reason, d.Message
			results = append(results, r)
			continue
		}
		existing, err := api.FindProjectItem(ctx, project.ID, item.ContentID)
		switch {
		case err != nil:
			r.Outcome, r.Err = Failed, err
		case existing != "":
			r.Outcome, r.ProjectItemID = Unchanged, existing
		default:
			r.Outcome = Pending
		}
		results = append(results, r)
	}
	return results
}

// Apply adds every pending item, or marks it planned when dryRun is set.
// Items left pending when ctx is canceled stay pending.
func Apply(ctx context.Context, api API, project ghapi.Project, results []Result, dryRun bool) {
	for i := range results {
		r := &results[i]
		if r.Outcome != Pending {
			continue
		}
		if dryRun {
			r.Outcome = Planned
			continue
		}
		if ctx.Err() != nil {
			return
		}
		id, err := api.AddItem(ctx, project.ID, r.Item.ContentID)
		if err != nil {
			r.Outcome, r.Err, r.Mutating = Failed, err, true
			continue
		}
		r.Outcome, r.ProjectItemID = Added, id
	}
}

// Count returns the number of results with the outcome.
func Count(results []Result, o Outcome) int {
	n := 0
	for _, r := range results {
		if r.Outcome == o {
			n++
		}
	}
	return n
}
