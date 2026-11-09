// Package filter decides whether an issue or pull request may be added to a project.
package filter

import (
	"fmt"
	"strings"
)

const (
	TypeIssue       = "issue"
	TypePullRequest = "pull_request"
	StateOpen       = "open"
	StateClosed     = "closed"
)

// Config holds the eligibility conditions. List values must be normalized with ParseList.
type Config struct {
	Types          []string
	States         []string
	RequiredLabels []string
	ExcludedLabels []string
	Authors        []string
	ExcludeDrafts  bool
	ExcludeBots    bool
}

// Item is the information about an issue or pull request needed for the checks.
type Item struct {
	Type   string // TypeIssue or TypePullRequest
	State  string // StateOpen or StateClosed
	Author string
	IsBot  bool
	Draft  bool
	Labels []string
}

// ParseList splits comma/newline separated values, trims them and lower-cases them.
func ParseList(values ...string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' }) {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// Validate reports invalid configuration.
func (c Config) Validate() error {
	if len(c.Types) == 0 {
		return fmt.Errorf("types must contain issue and/or pull_request")
	}
	for _, t := range c.Types {
		if t != TypeIssue && t != TypePullRequest {
			return fmt.Errorf("invalid type %q: must be issue and/or pull_request", t)
		}
	}
	if len(c.States) == 0 {
		return fmt.Errorf("states must contain open and/or closed")
	}
	for _, s := range c.States {
		if s != StateOpen && s != StateClosed {
			return fmt.Errorf("invalid state %q: must be open and/or closed", s)
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Evaluate returns whether the item passes all conditions, and the reason when it does not.
// Excluded labels take priority over required labels.
func Evaluate(c Config, it Item) (bool, string) {
	if !contains(c.Types, it.Type) {
		return false, "Item type is not allowed."
	}
	if !contains(c.States, it.State) {
		return false, "Item state is not allowed."
	}
	if c.ExcludeBots && it.IsBot {
		return false, "Bot-authored items are excluded."
	}
	if len(c.Authors) > 0 && !contains(c.Authors, strings.ToLower(it.Author)) {
		return false, "Item author is not allowed."
	}
	if c.ExcludeDrafts && it.Type == TypePullRequest && it.Draft {
		return false, "Draft pull requests are excluded."
	}
	names := make([]string, 0, len(it.Labels))
	for _, l := range it.Labels {
		names = append(names, strings.ToLower(l))
	}
	for _, l := range c.ExcludedLabels {
		if contains(names, l) {
			return false, "Item has an excluded label."
		}
	}
	for _, l := range c.RequiredLabels {
		if !contains(names, l) {
			return false, "Item is missing a required label."
		}
	}
	return true, ""
}
