package filter

import "testing"

func base() Config {
	return Config{
		Types: []string{TypeIssue, TypePullRequest}, States: []string{StateOpen},
		ExcludeDrafts: true, ExcludeBots: true,
	}
}

func TestParseList(t *testing.T) {
	got := ParseList(" Triaged, Accepted\nBlocked ,, ")
	want := []string{"triaged", "accepted", "blocked"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := base().Validate(); err != nil {
		t.Fatal(err)
	}
	c := base()
	c.Types = []string{"discussion"}
	if c.Validate() == nil {
		t.Fatal("expected invalid type error")
	}
	c = base()
	c.States = nil
	if c.Validate() == nil {
		t.Fatal("expected empty states error")
	}
}

func TestEvaluate(t *testing.T) {
	issue := Item{Type: TypeIssue, State: StateOpen, Author: "Alice", Labels: []string{"Triaged", "Accepted"}}
	tests := []struct {
		name   string
		mod    func(*Config, *Item)
		wantOK bool
		reason string
	}{
		{"passes", func(*Config, *Item) {}, true, ""},
		{"type", func(c *Config, _ *Item) { c.Types = []string{TypePullRequest} }, false, "Item type is not allowed."},
		{"state", func(_ *Config, i *Item) { i.State = StateClosed }, false, "Item state is not allowed."},
		{"closed allowed", func(c *Config, i *Item) { i.State = StateClosed; c.States = []string{StateClosed} }, true, ""},
		{"bot", func(_ *Config, i *Item) { i.IsBot = true }, false, "Bot-authored items are excluded."},
		{"bot allowed", func(c *Config, i *Item) { i.IsBot = true; c.ExcludeBots = false }, true, ""},
		{"author denied", func(c *Config, _ *Item) { c.Authors = []string{"bob"} }, false, "Item author is not allowed."},
		{"author case-insensitive", func(c *Config, _ *Item) { c.Authors = []string{"alice"} }, true, ""},
		{"draft", func(_ *Config, i *Item) { i.Type = TypePullRequest; i.Draft = true }, false, "Draft pull requests are excluded."},
		{"draft allowed", func(c *Config, i *Item) { i.Type = TypePullRequest; i.Draft = true; c.ExcludeDrafts = false }, true, ""},
		{"required ok", func(c *Config, _ *Item) { c.RequiredLabels = []string{"triaged", "accepted"} }, true, ""},
		{"required missing", func(c *Config, _ *Item) { c.RequiredLabels = []string{"triaged", "ready"} }, false, "Item is missing a required label."},
		{"excluded", func(c *Config, _ *Item) { c.ExcludedLabels = []string{"accepted"} }, false, "Item has an excluded label."},
		{"excluded wins over required", func(c *Config, _ *Item) {
			c.RequiredLabels = []string{"triaged"}
			c.ExcludedLabels = []string{"triaged"}
		}, false, "Item has an excluded label."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, it := base(), issue
			tt.mod(&c, &it)
			ok, reason := Evaluate(c, it)
			if ok != tt.wantOK || reason != tt.reason {
				t.Fatalf("got (%v, %q), want (%v, %q)", ok, reason, tt.wantOK, tt.reason)
			}
		})
	}
}
