package syncer

import (
	"context"
	"errors"
	"testing"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
)

type fakeAPI struct {
	items        map[int]Item
	project      Project
	projectErr   error
	added        []string
	projectCalls int
}

func (f *fakeAPI) GetItem(_ context.Context, _ string, n int) (Item, error) {
	it, ok := f.items[n]
	if !ok {
		return Item{}, errors.New("not found")
	}
	return it, nil
}
func (f *fakeAPI) ListItemNumbers(context.Context, string, []string) ([]int, error) {
	return []int{1, 2, 3}, nil
}
func (f *fakeAPI) GetProject(context.Context, string, int) (Project, error) {
	f.projectCalls++
	return f.project, f.projectErr
}
func (f *fakeAPI) AddItem(_ context.Context, _, contentID string) (string, error) {
	f.added = append(f.added, contentID)
	return "PVTI_" + contentID, nil
}

func opts() Options {
	return Options{ProjectOwner: "o", ProjectNumber: 1, Filter: filter.Config{
		Types: []string{"issue", "pull_request"}, States: []string{"open"}, ExcludeDrafts: true, ExcludeBots: true,
	}}
}

func item(n int, mod func(*Item)) Item {
	it := Item{Number: n, ContentID: "C" + string(rune('0'+n)), Item: filter.Item{Type: "issue", State: "open", Author: "a"}}
	if mod != nil {
		mod(&it)
	}
	return it
}

func TestSyncOne(t *testing.T) {
	ctx := context.Background()
	t.Run("synced", func(t *testing.T) {
		f := &fakeAPI{items: map[int]Item{1: item(1, nil)}, project: Project{ID: "P"}}
		r, err := (&Syncer{API: f, Opts: opts()}).SyncOne(ctx, "o/r", 1)
		if err != nil || r.Result != ResultSynced || r.ItemID != "PVTI_C1" {
			t.Fatalf("%+v %v", r, err)
		}
	})
	t.Run("skipped does not touch project", func(t *testing.T) {
		f := &fakeAPI{items: map[int]Item{1: item(1, func(i *Item) { i.IsBot = true })}}
		r, err := (&Syncer{API: f, Opts: opts()}).SyncOne(ctx, "o/r", 1)
		if err != nil || r.Result != ResultSkipped || f.projectCalls != 0 || len(f.added) != 0 {
			t.Fatalf("%+v %v", r, err)
		}
	})
	t.Run("closed project", func(t *testing.T) {
		f := &fakeAPI{items: map[int]Item{1: item(1, nil)}, project: Project{ID: "P", Closed: true}}
		r, _ := (&Syncer{API: f, Opts: opts()}).SyncOne(ctx, "o/r", 1)
		if r.Result != ResultSkipped || len(f.added) != 0 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		o := opts()
		o.DryRun = true
		f := &fakeAPI{items: map[int]Item{1: item(1, nil)}, project: Project{ID: "P"}}
		r, _ := (&Syncer{API: f, Opts: o}).SyncOne(ctx, "o/r", 1)
		if r.Result != ResultDryRun || len(f.added) != 0 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("project error", func(t *testing.T) {
		f := &fakeAPI{items: map[int]Item{1: item(1, nil)}, projectErr: errors.New("boom")}
		if _, err := (&Syncer{API: f, Opts: opts()}).SyncOne(ctx, "o/r", 1); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestSyncAllCollectsErrors(t *testing.T) {
	f := &fakeAPI{items: map[int]Item{1: item(1, nil), 3: item(3, nil)}, project: Project{ID: "P"}}
	s := &Syncer{API: f, Opts: opts()}
	results, errs := s.SyncAll(context.Background(), "o/r")
	if len(results) != 2 || len(errs) != 1 || f.projectCalls != 1 {
		t.Fatalf("results=%d errs=%d projectCalls=%d", len(results), len(errs), f.projectCalls)
	}
}
