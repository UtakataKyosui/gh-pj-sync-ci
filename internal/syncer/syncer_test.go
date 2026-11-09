package syncer

import (
	"context"
	"errors"
	"testing"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
)

type fakeAPI struct {
	items    map[int]ghapi.Item
	existing map[string]string
	findErr  error
	addErr   error
	added    []string
}

func (f *fakeAPI) GetItem(_ context.Context, repo string, n int) (ghapi.Item, error) {
	it, ok := f.items[n]
	if !ok {
		return ghapi.Item{}, errors.New("not found")
	}
	it.Repository, it.Number = repo, n
	return it, nil
}
func (f *fakeAPI) FindProjectItem(_ context.Context, _, c string) (string, error) {
	return f.existing[c], f.findErr
}
func (f *fakeAPI) AddItem(_ context.Context, _, c string) (string, error) {
	if f.addErr != nil {
		return "", f.addErr
	}
	f.added = append(f.added, c)
	return "PVTI_" + c, nil
}

var cfg = filter.Config{Types: []string{filter.TypeIssue, filter.TypePullRequest}, States: []string{filter.StateOpen}}
var project = ghapi.Project{ID: "PVT_1"}

func issue(content string, mod func(*ghapi.Item)) ghapi.Item {
	it := ghapi.Item{ContentID: content, Item: filter.Item{Type: filter.TypeIssue, State: filter.StateOpen, Author: "a"}}
	if mod != nil {
		mod(&it)
	}
	return it
}

func refs(ns ...int) []Ref {
	out := make([]Ref, len(ns))
	for i, n := range ns {
		out[i] = Ref{"o/r", n}
	}
	return out
}

func TestPlanAndApply(t *testing.T) {
	f := &fakeAPI{
		items: map[int]ghapi.Item{
			1: issue("I1", nil),
			2: issue("I2", func(i *ghapi.Item) { i.State = filter.StateClosed }),
			3: issue("I3", nil),
		},
		existing: map[string]string{"I3": "PVTI_old"},
	}
	results := Plan(context.Background(), f, project, cfg, refs(1, 2, 3, 4))
	want := []Outcome{Pending, Skipped, Unchanged, Failed}
	for i, o := range want {
		if results[i].Outcome != o {
			t.Fatalf("result %d: %s, want %s", i, results[i].Outcome, o)
		}
	}
	if results[1].Reason != filter.ReasonStateNotAllowed || results[2].ProjectItemID != "PVTI_old" {
		t.Fatalf("%+v", results)
	}
	if len(f.added) != 0 {
		t.Fatal("Plan must not add items")
	}
	Apply(context.Background(), f, project, results, false)
	if results[0].Outcome != Added || results[0].ProjectItemID != "PVTI_I1" || len(f.added) != 1 {
		t.Fatalf("%+v", results[0])
	}
	if Count(results, Added) != 1 || Count(results, Failed) != 1 {
		t.Fatal("bad counts")
	}
}

func TestApplyDryRunNeverAdds(t *testing.T) {
	f := &fakeAPI{items: map[int]ghapi.Item{1: issue("I1", nil)}}
	results := Plan(context.Background(), f, project, cfg, refs(1))
	Apply(context.Background(), f, project, results, true)
	if results[0].Outcome != Planned || len(f.added) != 0 {
		t.Fatalf("%+v", results[0])
	}
}

func TestErrorsBecomeFailedResults(t *testing.T) {
	f := &fakeAPI{items: map[int]ghapi.Item{1: issue("I1", nil), 2: issue("I2", nil)}, findErr: errors.New("boom")}
	results := Plan(context.Background(), f, project, cfg, refs(1, 2))
	if Count(results, Failed) != 2 {
		t.Fatalf("%+v", results)
	}

	f = &fakeAPI{items: map[int]ghapi.Item{1: issue("I1", nil)}, addErr: errors.New("boom")}
	results = Plan(context.Background(), f, project, cfg, refs(1))
	Apply(context.Background(), f, project, results, false)
	if results[0].Outcome != Failed || results[0].Err == nil {
		t.Fatalf("%+v", results[0])
	}
}

func TestCanceledContextStopsWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeAPI{items: map[int]ghapi.Item{1: issue("I1", nil)}}
	if got := Plan(ctx, f, project, cfg, refs(1)); len(got) != 0 {
		t.Fatalf("planned %d items after cancel", len(got))
	}
	results := []Result{{Outcome: Pending}}
	Apply(ctx, f, project, results, false)
	if results[0].Outcome != Pending || len(f.added) != 0 {
		t.Fatal("Apply must not add after cancel")
	}
}
