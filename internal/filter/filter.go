// Package filter decides whether an issue or pull request may be added to a project.
package filter

import (
	"fmt"
	"strings"
)

// Item types and states, as accepted by --type and --state.
const (
	TypeIssue       = "issue"
	TypePullRequest = "pull-request"
	StateOpen       = "open"
	StateClosed     = "closed"
)

// Skip reasons. These are stable codes that appear in --json output.
const (
	ReasonTypeNotAllowed   = "type_not_allowed"
	ReasonStateNotAllowed  = "state_not_allowed"
	ReasonBotAuthor        = "bot_author"
	ReasonAuthorNotAllowed = "author_not_allowed"
	ReasonDraft            = "draft"
	ReasonExcludedLabel    = "excluded_label"
	ReasonMissingLabel     = "missing_label"
)

// Config holds the eligibility conditions. List values are lower-cased.
type Config struct {
	Types          []string
	States         []string
	RequiredLabels []string
	ExcludedLabels []string
	Authors        []string
	IncludeDrafts  bool
	IncludeBots    bool
}

// Item is the information about an issue or pull request needed for the checks.
type Item struct {
	Type   string
	State  string
	Author string
	IsBot  bool
	Draft  bool
	Labels []string
}

// Decision is the result of Evaluate.
type Decision struct {
	OK      bool
	Reason  string // stable code, empty when OK
	Message string // human sentence, empty when OK
}

// SplitList splits comma separated values, trims them, lower-cases them and drops empties.
func SplitList(values ...string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func skip(reason, format string, args ...any) Decision {
	return Decision{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

func typeNoun(t string) string {
	if t == TypePullRequest {
		return "Pull requests"
	}
	return "Issues"
}

// Evaluate checks the item against every condition, in a fixed order.
// Excluded labels take priority over required labels.
func Evaluate(c Config, it Item) Decision {
	if !contains(c.Types, it.Type) {
		return skip(ReasonTypeNotAllowed, "%s aren't included. Allowed types: %s.", typeNoun(it.Type), strings.Join(c.Types, ", "))
	}
	if !contains(c.States, it.State) {
		return skip(ReasonStateNotAllowed, "It's %s. Allowed states: %s.", it.State, strings.Join(c.States, ", "))
	}
	if !c.IncludeBots && it.IsBot {
		return skip(ReasonBotAuthor, "%s is a bot account. Use --bots to include bot items.", it.Author)
	}
	if len(c.Authors) > 0 && !contains(c.Authors, strings.ToLower(it.Author)) {
		return skip(ReasonAuthorNotAllowed, "%s isn't an allowed author.", it.Author)
	}
	if !c.IncludeDrafts && it.Type == TypePullRequest && it.Draft {
		return skip(ReasonDraft, "It's a draft pull request. Use --drafts to include drafts.")
	}
	names := make([]string, 0, len(it.Labels))
	for _, l := range it.Labels {
		names = append(names, strings.ToLower(l))
	}
	for _, l := range c.ExcludedLabels {
		if contains(names, l) {
			return skip(ReasonExcludedLabel, "It has the excluded label %q.", l)
		}
	}
	for _, l := range c.RequiredLabels {
		if !contains(names, l) {
			return skip(ReasonMissingLabel, "It's missing the required label %q.", l)
		}
	}
	return Decision{OK: true}
}
