package filter

import (
	"reflect"
	"testing"
)

func base() Config {
	return Config{Types: []string{TypeIssue, TypePullRequest}, States: []string{StateOpen}}
}

func TestSplitList(t *testing.T) {
	got := SplitList(" Triaged, Accepted", "Blocked ,, ", "")
	want := []string{"triaged", "accepted", "blocked"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if SplitList() != nil || SplitList(" , ") != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestEvaluate(t *testing.T) {
	issue := Item{Type: TypeIssue, State: StateOpen, Author: "Alice", Labels: []string{"Triaged", "Accepted"}}
	tests := []struct {
		name    string
		mod     func(*Config, *Item)
		reason  string
		message string
	}{
		{"passes", func(*Config, *Item) {}, "", ""},
		{"type", func(c *Config, _ *Item) { c.Types = []string{TypePullRequest} },
			ReasonTypeNotAllowed, "Issues aren't included. Allowed types: pull-request."},
		{"pr type", func(c *Config, i *Item) { c.Types = []string{TypeIssue}; i.Type = TypePullRequest },
			ReasonTypeNotAllowed, "Pull requests aren't included. Allowed types: issue."},
		{"state", func(_ *Config, i *Item) { i.State = StateClosed },
			ReasonStateNotAllowed, "It's closed. Allowed states: open."},
		{"closed allowed", func(c *Config, i *Item) { i.State = StateClosed; c.States = []string{StateOpen, StateClosed} }, "", ""},
		{"bot", func(_ *Config, i *Item) { i.IsBot = true; i.Author = "dependabot[bot]" },
			ReasonBotAuthor, "dependabot[bot] is a bot account. Use --bots to include bot items."},
		{"bot included", func(c *Config, i *Item) { i.IsBot = true; c.IncludeBots = true }, "", ""},
		{"author denied", func(c *Config, _ *Item) { c.Authors = []string{"bob"} },
			ReasonAuthorNotAllowed, "Alice isn't an allowed author."},
		{"author ignores case", func(c *Config, _ *Item) { c.Authors = []string{"alice"} }, "", ""},
		{"draft", func(_ *Config, i *Item) { i.Type = TypePullRequest; i.Draft = true },
			ReasonDraft, "It's a draft pull request. Use --drafts to include drafts."},
		{"draft included", func(c *Config, i *Item) { i.Type = TypePullRequest; i.Draft = true; c.IncludeDrafts = true }, "", ""},
		{"draft flag ignored on issues", func(_ *Config, i *Item) { i.Draft = true }, "", ""},
		{"required present", func(c *Config, _ *Item) { c.RequiredLabels = []string{"triaged", "accepted"} }, "", ""},
		{"required missing", func(c *Config, _ *Item) { c.RequiredLabels = []string{"triaged", "ready"} },
			ReasonMissingLabel, `It's missing the required label "ready".`},
		{"excluded", func(c *Config, _ *Item) { c.ExcludedLabels = []string{"accepted"} },
			ReasonExcludedLabel, `It has the excluded label "accepted".`},
		{"excluded wins over required", func(c *Config, _ *Item) {
			c.RequiredLabels = []string{"triaged"}
			c.ExcludedLabels = []string{"triaged"}
		}, ReasonExcludedLabel, `It has the excluded label "triaged".`},
		{"state checked before bot", func(_ *Config, i *Item) { i.State = StateClosed; i.IsBot = true },
			ReasonStateNotAllowed, "It's closed. Allowed states: open."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, it := base(), issue
			tt.mod(&c, &it)
			d := Evaluate(c, it)
			if d.OK != (tt.reason == "") || d.Reason != tt.reason || d.Message != tt.message {
				t.Fatalf("got %+v, want reason %q message %q", d, tt.reason, tt.message)
			}
		})
	}
}
