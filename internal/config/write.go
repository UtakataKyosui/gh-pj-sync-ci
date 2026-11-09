package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SchemaURL is referenced from generated files so editors can complete and validate them.
const SchemaURL = "https://raw.githubusercontent.com/UtakataKyosui/gh-pj-synker/main/docs/pj-synker.schema.json"

var plainScalar = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// quote renders a string as a YAML scalar that is safe inside a flow sequence.
// Anything beyond plain words is written as a double-quoted (JSON) string.
func quote(s string) string {
	if plainScalar.MatchString(s) && !reserved[strings.ToLower(s)] {
		return s
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// reserved are plain scalars YAML would read as something other than a string.
var reserved = map[string]bool{
	"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true,
	"null": true, "y": true, "n": true,
}

func flow(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// Render writes a config file for f. Unset filters become a commented-out example.
func Render(f File) []byte {
	var b strings.Builder
	b.WriteString("# gh pj-synker settings. Options given on the command line override them.\n")
	b.WriteString("# Check the file with: gh pj-synker config check\n")
	b.WriteString("# yaml-language-server: $schema=" + SchemaURL + "\n")
	fmt.Fprintf(&b, "version: %d\n\n", CurrentVersion)
	b.WriteString("project:\n")
	fmt.Fprintf(&b, "  owner: %s\n", quote(f.Owner))
	fmt.Fprintf(&b, "  number: %d\n", f.Number)
	if len(f.Repositories) > 0 {
		fmt.Fprintf(&b, "\nrepositories: %s\n", flow(f.Repositories))
	}

	var filters []string
	add := func(key string, v []string) {
		if v != nil {
			filters = append(filters, fmt.Sprintf("  %s: %s", key, flow(v)))
		}
	}
	add("types", f.Types)
	add("states", f.States)
	add("labels", f.Labels)
	add("exclude-labels", f.ExcludeLabels)
	add("authors", f.Authors)
	if f.Drafts != nil {
		filters = append(filters, fmt.Sprintf("  drafts: %t", *f.Drafts))
	}
	if f.Bots != nil {
		filters = append(filters, fmt.Sprintf("  bots: %t", *f.Bots))
	}
	b.WriteString("\n")
	if len(filters) > 0 {
		b.WriteString("filters:\n" + strings.Join(filters, "\n") + "\n")
	} else {
		b.WriteString("# Only add items that pass these filters:\n")
		b.WriteString("# filters:\n#   labels: [triaged]\n#   exclude-labels: [blocked]\n")
	}
	return []byte(b.String())
}

// ErrExists means the file already exists and overwriting wasn't allowed.
var ErrExists = fs.ErrExist

// WriteFile writes data to path atomically: a temp file in the same directory is renamed
// or linked into place, so an interrupted write never leaves a truncated file. Without
// overwrite, an existing file is never replaced, even when it appears during the write.
func WriteFile(path string, data []byte, overwrite bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".pj-synker-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once it has been renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	if overwrite {
		return os.Rename(name, path)
	}
	// os.Link fails when path exists, which makes the no-clobber check atomic.
	if err := os.Link(name, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return err
	}
	return nil
}

// CheckoutRoot returns the root of the git checkout containing dir, or "" outside one.
func CheckoutRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
