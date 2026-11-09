package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

func TestExampleConfigIsValid(t *testing.T) {
	f, err := Load("../../examples/pj-synker.yml", "examples/pj-synker.yml")
	if err != nil {
		t.Fatal(err)
	}
	if f.Owner == "" || f.Number == 0 {
		t.Fatalf("the example must set the required settings: %+v", f)
	}
}

type schemaNode struct {
	Type       string                `json:"type"`
	Const      any                   `json:"const"`
	Properties map[string]schemaNode `json:"properties"`
	Items      *schemaNode           `json:"items"`
}

// sample renders a valid YAML value for a schema node.
func sample(n schemaNode) string {
	switch {
	case n.Const != nil:
		return fmt.Sprint(n.Const)
	case n.Type == "integer":
		return "1"
	case n.Type == "boolean":
		return "true"
	case n.Type == "array":
		return "[]"
	}
	return "acme"
}

// The JSON Schema in docs/ must describe exactly the keys the parser accepts.
func TestSchemaMatchesParser(t *testing.T) {
	data, err := os.ReadFile("../../docs/pj-synker.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var root schemaNode
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for name, n := range root.Properties {
		if n.Type == "object" {
			for child, c := range n.Properties {
				keys = append(keys, name+"."+child)
				src := fmt.Sprintf("%s:\n  %s: %s\n", name, child, sample(c))
				if _, err := Parse([]byte(src), "x.yml"); err != nil {
					t.Errorf("schema key %s.%s: %v %+v", name, child, err, err)
				}
			}
			continue
		}
		keys = append(keys, name)
		if _, err := Parse([]byte(fmt.Sprintf("%s: %s\n", name, sample(n))), "x.yml"); err != nil {
			t.Errorf("schema key %s: %v", name, err)
		}
	}
	sort.Strings(keys)
	want := "filters.authors filters.bots filters.drafts filters.exclude-labels filters.labels filters.states " +
		"filters.types project.number project.owner repositories version"
	if got := strings.Join(keys, " "); got != want {
		t.Fatalf("schema keys:\n got  %s\n want %s", got, want)
	}
}
