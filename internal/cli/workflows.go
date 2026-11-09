package cli

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// workflowCheckTimeout bounds the read-only check so that init and config check stay quick offline.
const workflowCheckTimeout = 5 * time.Second

// overlappingWorkflows are the built-in project workflows that add items on their own,
// by exact name. Their conditions can't be read, so a match is only a hint.
var overlappingWorkflows = []string{"Auto-add to project", "Auto-add sub-issues to project"}

// overlapWarning is a project with workflows that may add items this tool's filters exclude.
type overlapWarning struct {
	owner     string
	number    int
	workflows []string
}

func (w overlapWarning) ref() string { return fmt.Sprintf("%s/%d", w.owner, w.number) }

func (w overlapWarning) message() string {
	return fmt.Sprintf("Project %s has %s turned on. It can add items that your filters exclude.",
		w.ref(), quoteList(w.workflows))
}

func (w overlapWarning) json() map[string]any {
	return map[string]any{"reason": "overlapping_workflow", "message": w.message(), "workflows": w.workflows}
}

func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = `"` + n + `"`
	}
	return strings.Join(quoted, " and ")
}

// checkOverlappingWorkflows reads the project's workflows and reports the enabled ones that add
// items by themselves. It is best effort: without a token, scope, access or network it reports
// nothing, so init and config check keep working as before.
func checkOverlappingWorkflows(ctx context.Context, d Deps, owner string, number int) *overlapWarning {
	if d.NewAPI == nil || owner == "" || number < 1 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, workflowCheckTimeout)
	defer cancel()
	api, err := d.NewAPI()
	if err != nil {
		return nil
	}
	list, err := api.ListWorkflows(ctx, owner, number)
	if err != nil {
		return nil
	}
	var found []string
	for _, want := range overlappingWorkflows {
		for _, w := range list {
			if w.Enabled && w.Name == want {
				found = append(found, want)
				break
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &overlapWarning{owner: owner, number: number, workflows: found}
}

// printOverlapWarning writes the warning to stderr. Nothing is changed in the project.
func printOverlapWarning(p printer, w *overlapWarning) {
	p.blank()
	p.text("Warning: " + w.message())
	p.text("To turn it off, open the project, choose Workflows, select it and turn it off.")
	p.text("gh pj-synker doesn't change the project's workflows.")
}

// warningsJSON is the value of the "warnings" key, or nil when there is none.
func warningsJSON(w *overlapWarning) []map[string]any {
	if w == nil {
		return nil
	}
	return []map[string]any{w.json()}
}
