// Package config finds, reads and strictly validates the YAML config file.
//
// A broken config file is always an error with its location; it is never treated as
// absent or replaced with defaults.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ident"
)

// EnvVar names the config file, like --config.
const EnvVar = "GH_PJ_SYNKER_CONFIG"

// CurrentVersion is the only file format version this build reads.
const CurrentVersion = 1

// Names are the file names looked up in each directory, in order.
var Names = []string{
	filepath.Join(".github", "pj-synker.yml"),
	filepath.Join(".github", "pj-synker.yaml"),
	".pj-synker.yml",
	".pj-synker.yaml",
}

// File is a parsed config file. Unset values are nil or zero.
type File struct {
	Path          string // as given or found, for display
	Version       int
	Owner         string
	Number        int
	Repositories  []string
	Types         []string
	States        []string
	Labels        []string
	ExcludeLabels []string
	Authors       []string
	Drafts        *bool
	Bots          *bool
}

// Issue is one problem at a position in the file.
type Issue struct {
	Line, Column int
	Message      string
}

// Error is an invalid config file: a YAML syntax error or a list of issues.
type Error struct {
	Path   string
	Source []byte
	Syntax string // the YAML parser's message, when the file isn't valid YAML
	Line   int    // line of the syntax error, 0 when unknown
	Issues []Issue
}

func (e *Error) Error() string {
	if e.Syntax != "" {
		return fmt.Sprintf("%s isn't valid YAML: %s", e.Path, e.Syntax)
	}
	return fmt.Sprintf("%s has %d invalid settings", e.Path, len(e.Issues))
}

// ErrAmbiguous means one directory holds more than one config file.
type ErrAmbiguous struct{ Paths []string }

func (e *ErrAmbiguous) Error() string {
	return "more than one config file: " + strings.Join(e.Paths, ", ")
}

// Discover looks for a config file from dir up to the root of its git checkout. Outside a
// checkout only dir is searched. It returns "" when there is none.
func Discover(dir string) (string, error) {
	for {
		var found []string
		for _, name := range Names {
			p := filepath.Join(dir, name)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				found = append(found, p)
			}
		}
		if len(found) > 1 {
			return "", &ErrAmbiguous{Paths: found}
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "", nil // reached the root of the checkout
		}
		parent := filepath.Dir(dir)
		if parent == dir || !insideGit(parent) {
			return "", nil
		}
		dir = parent
	}
}

// insideGit reports whether dir or one of its parents has a .git entry.
func insideGit(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// Load reads and validates a config file. display is the path shown in messages.
func Load(path, display string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, display)
}

var syntaxLineRe = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// Parse validates YAML source. Every problem is reported at once.
func Parse(data []byte, display string) (*File, error) {
	f := &File{Path: display, Version: CurrentVersion}
	syntax := func(err error) error {
		e := &Error{Path: display, Source: data, Syntax: strings.TrimPrefix(err.Error(), "yaml: ")}
		if m := syntaxLineRe.FindStringSubmatch(err.Error()); m != nil {
			e.Line, _ = strconv.Atoi(m[1])
			e.Syntax = m[2]
		}
		return e
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, syntax(err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return f, nil // an empty file sets nothing
	}
	d := &decoder{file: f}
	// Settings in a later document would be ignored without a word, so reject them.
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, syntax(err)
	case len(extra.Content) > 0 && (extra.Content[0].Kind != yaml.ScalarNode || extra.Content[0].Tag != "!!null"):
		d.issue(extra.Content[0], "Only one YAML document is allowed. Remove the extra \"---\" section or merge it into the first.")
	}
	root := resolve(doc.Content[0])
	if root.Kind != yaml.MappingNode {
		d.issue(root, "The file must be a mapping of settings, such as \"project:\".")
	} else {
		d.root(root)
	}
	if len(d.issues) > 0 {
		sort.SliceStable(d.issues, func(i, j int) bool {
			if d.issues[i].Line != d.issues[j].Line {
				return d.issues[i].Line < d.issues[j].Line
			}
			return d.issues[i].Column < d.issues[j].Column
		})
		return nil, &Error{Path: display, Source: data, Issues: d.issues}
	}
	return f, nil
}

type decoder struct {
	file   *File
	issues []Issue
}

func (d *decoder) issue(n *yaml.Node, format string, args ...any) {
	d.issues = append(d.issues, Issue{Line: n.Line, Column: n.Column, Message: fmt.Sprintf(format, args...)})
}

func resolve(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

type field struct {
	name   string
	decode func(path string, value *yaml.Node)
}

// mapping walks a mapping node, rejecting unknown and repeated keys.
func (d *decoder) mapping(path string, n *yaml.Node, fields []field) {
	seen := map[string]bool{}
	var names []string
	for _, f := range fields {
		names = append(names, f.name)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], resolve(n.Content[i+1])
		key := join(path, k.Value)
		if seen[k.Value] {
			d.issue(k, "%q is set more than once.", key)
			continue
		}
		seen[k.Value] = true
		matched := false
		for _, f := range fields {
			if f.name == k.Value {
				f.decode(key, v)
				matched = true
				break
			}
		}
		if !matched {
			msg := fmt.Sprintf("%q isn't a setting.", key)
			if s := closest(k.Value, names); s != "" {
				msg += fmt.Sprintf(" Did you mean %q?", join(path, s))
			}
			d.issue(k, "%s", msg)
		}
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func (d *decoder) root(n *yaml.Node) {
	f := d.file
	d.mapping("", n, []field{
		{"version", func(key string, v *yaml.Node) {
			if version, ok := d.int(key, v); ok {
				if version != CurrentVersion {
					d.issue(v, "version %d isn't supported. This build reads version %d.", version, CurrentVersion)
				}
				f.Version = version
			}
		}},
		{"project", func(key string, v *yaml.Node) {
			if !d.isMapping(key, v) {
				return
			}
			d.mapping(key, v, []field{
				{"owner", func(key string, v *yaml.Node) {
					if s, ok := d.str(key, v); ok {
						if !ident.Login(s) {
							d.issue(v, "%q isn't a valid user or organization login.", s)
							return
						}
						f.Owner = s
					}
				}},
				{"number", func(key string, v *yaml.Node) {
					if n, ok := d.int(key, v); ok {
						f.Number = n
					}
				}},
			})
		}},
		{"repositories", func(key string, v *yaml.Node) {
			f.Repositories = d.list(key, v, func(item *yaml.Node, s string) bool {
				if !ident.Repo(s) {
					d.issue(item, "%q must look like OWNER/REPO.", s)
					return false
				}
				return true
			})
			if f.Repositories == nil {
				f.Repositories = []string{}
			}
		}},
		{"filters", func(key string, v *yaml.Node) {
			if !d.isMapping(key, v) {
				return
			}
			d.mapping(key, v, []field{
				{"types", func(key string, v *yaml.Node) {
					f.Types = d.lowerList(key, v, func(item *yaml.Node, s string) bool {
						if s != filter.TypeIssue && s != filter.TypePullRequest {
							d.issue(item, "%q isn't a type. Use issue or pull-request.", s)
							return false
						}
						return true
					})
				}},
				{"states", func(key string, v *yaml.Node) {
					f.States = d.lowerList(key, v, func(item *yaml.Node, s string) bool {
						if s != filter.StateOpen && s != filter.StateClosed {
							d.issue(item, "%q isn't a state. Use open or closed.", s)
							return false
						}
						return true
					})
				}},
				{"labels", func(key string, v *yaml.Node) { f.Labels = d.lowerList(key, v, nil) }},
				{"exclude-labels", func(key string, v *yaml.Node) { f.ExcludeLabels = d.lowerList(key, v, nil) }},
				{"authors", func(key string, v *yaml.Node) { f.Authors = d.lowerList(key, v, nil) }},
				{"drafts", func(key string, v *yaml.Node) { f.Drafts = d.bool(key, v) }},
				{"bots", func(key string, v *yaml.Node) { f.Bots = d.bool(key, v) }},
			})
		}},
	})
}

func (d *decoder) isMapping(key string, v *yaml.Node) bool {
	if v.Kind != yaml.MappingNode {
		d.issue(v, "%q must be a mapping of settings.", key)
		return false
	}
	return true
}

func (d *decoder) str(key string, v *yaml.Node) (string, bool) {
	if v.Kind != yaml.ScalarNode || v.Tag == "!!null" || v.Tag == "!!bool" || v.Tag == "!!float" {
		d.issue(v, "%q must be a string.", key)
		return "", false
	}
	return v.Value, true
}

func (d *decoder) int(key string, v *yaml.Node) (int, bool) {
	n, err := strconv.Atoi(v.Value)
	if v.Kind != yaml.ScalarNode || v.Tag != "!!int" || err != nil || n < 1 {
		d.issue(v, "%q must be a positive whole number.", key)
		return 0, false
	}
	return n, true
}

func (d *decoder) bool(key string, v *yaml.Node) *bool {
	if v.Kind != yaml.ScalarNode || v.Tag != "!!bool" {
		d.issue(v, "%q must be true or false.", key)
		return nil
	}
	b := v.Value == "true" || v.Value == "True" || v.Value == "TRUE"
	return &b
}

// list reads a sequence of strings. It returns a non-nil slice when the setting is present.
func (d *decoder) list(key string, v *yaml.Node, valid func(*yaml.Node, string) bool) []string {
	if v.Kind != yaml.SequenceNode {
		d.issue(v, "%q must be a list, such as [a, b].", key)
		return nil
	}
	out := []string{}
	for i, item := range v.Content {
		item = resolve(item)
		if item.Kind != yaml.ScalarNode || item.Tag == "!!null" || strings.TrimSpace(item.Value) == "" {
			d.issue(item, "%s[%d] must be a non-empty string.", key, i)
			continue
		}
		s := strings.TrimSpace(item.Value)
		if valid == nil || valid(item, s) {
			out = append(out, s)
		}
	}
	return out
}

func (d *decoder) lowerList(key string, v *yaml.Node, valid func(*yaml.Node, string) bool) []string {
	var wrapped func(*yaml.Node, string) bool
	if valid != nil {
		wrapped = func(n *yaml.Node, s string) bool { return valid(n, strings.ToLower(s)) }
	}
	out := d.list(key, v, wrapped)
	for i := range out {
		out[i] = strings.ToLower(out[i])
	}
	return out
}

// closest returns a candidate within a small edit distance, or "".
func closest(input string, candidates []string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if dist := levenshtein(input, c); dist < bestDist {
			best, bestDist = c, dist
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// IsNotExist reports whether err means the file doesn't exist.
func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
