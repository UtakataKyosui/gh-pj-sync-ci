package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UtakataKyosui/gh-pj-synker/internal/config"
	"github.com/UtakataKyosui/gh-pj-synker/internal/filter"
)

var configOption = option{name: "config", kind: stringOpt, hint: "<FILE>", desc: "Config file to read", def: "found automatically"}

var configCommand = command{
	name:    "config",
	usage:   binName + " config <command> [options]",
	summary: "Inspect the config file",
	subs:    []command{configCheckCommand},
}

var configCheckCommand = command{
	name:    "config check",
	usage:   binName + " config check [options]",
	summary: "Validate the config file and show the settings it resolves to",
	options: []option{
		configOption,
		{name: "json", kind: boolOpt, desc: "Print the settings as JSON on stdout"},
	},
	examples: []example{
		{"Check the config file of the current repository", []string{"gh", "pj-synker", "config", "check"}},
		{"Check a specific file in CI", []string{"gh", "pj-synker", "config", "check", "--config", "ci/pj-synker.yml", "--json"}},
	},
	notes: []string{
		"The file is read from --config, GH_PJ_SYNKER_CONFIG, or the first of these",
		"found from the current directory up to the root of the git checkout:",
		"  .github/pj-synker.yml or .yaml, then .pj-synker.yml or .yaml",
		"Options given on the command line override the file.",
	},
}

// loadedConfig is the config file a run uses, if any.
type loadedConfig struct {
	file     *config.File
	display  string // path shown to people
	explicit bool   // named with --config or GH_PJ_SYNKER_CONFIG
}

// loadConfig resolves and reads the config file. noConfig skips the search. An explicit
// file that is missing is an error; a missing file found by search just means none.
func loadConfig(p parsed, noConfig bool, getenv func(string) string, getwd func() (string, error)) (*loadedConfig, *Error) {
	if noConfig {
		return nil, nil
	}
	cwd, _ := getwd()
	path, field := p.strs["config"], "--config"
	if !p.has("config") {
		path, field = strings.TrimSpace(getenv(config.EnvVar)), config.EnvVar
	}
	explicit := path != ""
	if !explicit {
		if cwd == "" {
			return nil, nil
		}
		found, err := config.Discover(cwd)
		var amb *config.ErrAmbiguous
		if errors.As(err, &amb) {
			var shown []string
			for _, a := range amb.Paths {
				shown = append(shown, displayPath(a, cwd))
			}
			return nil, &Error{Exit: exitUsage, Status: statusActionRequired, Reason: "ambiguous_config",
				Message: fmt.Sprintf("Can't choose a config file. Found %s.", strings.Join(shown, " and ")),
				Data:    map[string]any{"paths": shown},
				Recovery: []Recovery{{Instruction: "Keep one of them, or pick one with --config",
					Argv: []string{"gh", "pj-synker", "config", "check", "--config", "<FILE>"}}}}
		}
		if err != nil {
			return nil, &Error{Exit: exitFailure, Status: statusError, Reason: "invalid_config",
				Message: "Couldn't search for a config file.", Details: []detail{{"Error", sanitize(err.Error())}}}
		}
		if found == "" {
			return nil, nil
		}
		path = found
	} else if cwd != "" && !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	display := displayPath(path, cwd)
	f, err := config.Load(path, display)
	if err != nil {
		if config.IsNotExist(err) {
			if !explicit {
				return nil, nil
			}
			return nil, &Error{Exit: exitUsage, Status: statusError, Reason: "invalid_arguments",
				Message: fmt.Sprintf("Can't read config file %s. It doesn't exist.", display),
				Data:    map[string]any{"issues": []map[string]string{{"field": field, "reason": "not_found", "message": "The file doesn't exist."}}}}
		}
		return nil, configError(err, display, explicit)
	}
	return &loadedConfig{file: f, display: display, explicit: explicit}, nil
}

// displayPath shows a path relative to the working directory when it is inside it.
func displayPath(path, cwd string) string {
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}

// configError renders an invalid config file: its location, a code frame and every issue.
func configError(err error, display string, explicit bool) *Error {
	checkArgv := []string{"gh", "pj-synker", "config", "check"}
	if explicit {
		checkArgv = append(checkArgv, "--config", display)
	}
	recovery := []Recovery{{Instruction: "Fix the file, then check it", Argv: checkArgv}}
	var cerr *config.Error
	if !errors.As(err, &cerr) {
		return &Error{Exit: exitFailure, Status: statusError, Reason: "invalid_config", Recovery: recovery,
			Message: fmt.Sprintf("Couldn't read config file %s.", display), Details: []detail{{"Error", sanitize(err.Error())}}}
	}
	e := &Error{Exit: exitFailure, Status: statusError, Reason: "invalid_config", Recovery: recovery}
	if cerr.Syntax != "" {
		loc := display
		if cerr.Line > 0 {
			loc += ":" + strconv.Itoa(cerr.Line)
		}
		e.Message = loc + " isn't valid YAML."
		e.Details = []detail{{"YAML", sanitize(cerr.Syntax)}}
		e.Frame = codeFrame(cerr.Source, cerr.Line, 0)
		e.Data = map[string]any{"file": display, "line": cerr.Line, "syntax": cerr.Syntax}
		return e
	}
	var issues []map[string]any
	for _, is := range cerr.Issues {
		issues = append(issues, map[string]any{"line": is.Line, "column": is.Column, "message": is.Message})
	}
	e.Data = map[string]any{"file": display, "issues": issues}
	if len(cerr.Issues) == 1 {
		is := cerr.Issues[0]
		e.Message = fmt.Sprintf("%s:%d:%d %s", display, is.Line, is.Column, is.Message)
		e.Frame = codeFrame(cerr.Source, is.Line, is.Column)
		return e
	}
	e.Message = fmt.Sprintf("Can't read %s. %d settings are invalid.", display, len(cerr.Issues))
	for i, is := range cerr.Issues {
		if i == listLimit {
			e.Details = append(e.Details, detail{"", fmt.Sprintf("and %d more", len(cerr.Issues)-listLimit)})
			break
		}
		e.Details = append(e.Details, detail{fmt.Sprintf("%d:%d", is.Line, is.Column), is.Message})
	}
	return e
}

// codeFrame shows the line before and the line of a position, with a caret under the column.
func codeFrame(src []byte, line, col int) []string {
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	// A syntax error has no column and the parser reports where the construct started,
	// so show the next line too.
	last := line
	if col == 0 {
		last = min(len(lines), line+1)
	}
	width := len(strconv.Itoa(last))
	var out []string
	for n := max(1, line-1); n <= last; n++ {
		out = append(out, fmt.Sprintf("%*d | %s", width, n, sanitize(lines[n-1])))
	}
	if col > 0 {
		out = append(out, fmt.Sprintf("%s | %s^", strings.Repeat(" ", width), strings.Repeat(" ", col-1)))
	}
	return out
}

func runConfigCheck(args []string, s Streams, d Deps) int {
	m := resolveMode(s, scanGlobals(args), wantsFlag(args, "json", ""))
	p, perr := parse(args, configCheckCommand.allOptions(), configCheckCommand.path())
	if perr != nil {
		return fail(s, m, perr)
	}
	if len(p.positionals) > 0 {
		return fail(s, m, usageError("invalid_arguments",
			fmt.Sprintf("%s takes no arguments, not %q.", configCheckCommand.path(), sanitize(p.positionals[0])), configCheckCommand.path()))
	}
	m = resolveMode(s, globalOptions{noColor: p.bools["no-color"], nonInteractive: p.bools["non-interactive"]}, p.bools["json"])
	lc, err := loadConfig(p, false, s.Getenv, d.Getwd)
	if err != nil {
		return fail(s, m, err)
	}
	if lc == nil {
		var names []string
		for _, n := range config.Names {
			names = append(names, filepath.ToSlash(n))
		}
		return fail(s, m, &Error{Exit: exitFailure, Status: statusError, Reason: "config_not_found",
			Message: "Couldn't find a config file.",
			Details: []detail{{"Searched", ".github/pj-synker.yml or .yaml, .pj-synker.yml or .yaml"}},
			Data:    map[string]any{"searched": names},
			Recovery: []Recovery{{Instruction: "Create .github/pj-synker.yml at the root of the repository. See the format",
				URL: configDocsURL}}})
	}
	f := lc.file
	settings := effectiveSettings(f)
	if m.JSON {
		values := map[string]any{}
		var defaulted []string
		for _, st := range settings {
			values[st.key] = st.json
			if st.isDefault {
				defaulted = append(defaulted, st.key)
			}
		}
		if defaulted == nil {
			defaulted = []string{}
		}
		writeJSONDoc(s, map[string]any{"config": map[string]any{
			"path": lc.display, "version": f.Version, "settings": values, "defaults": defaulted,
		}})
		return exitOK
	}
	p2 := printer{w: s.Out, st: m.Out, width: m.Width}
	p2.success("Valid", lc.display)
	for _, st := range settings {
		p2.row(st.label, st.human)
	}
	if f.Owner == "" || f.Number == 0 {
		p2.blank()
		p2.text("Without project.owner and project.number, pass --owner and --project to add.")
	}
	return exitOK
}

type setting struct {
	key, label, human string
	json              any
	isDefault         bool
}

// effectiveSettings lists every setting with the value a run would use.
func effectiveSettings(f *config.File) []setting {
	list := func(key, label string, v, def []string, none string) setting {
		if v == nil {
			shown := none
			if len(def) > 0 {
				shown = strings.Join(def, ", ")
			}
			if def == nil {
				def = []string{}
			}
			return setting{key, label, shown + " (default)", def, true}
		}
		shown := strings.Join(v, ", ")
		if len(v) == 0 {
			shown = none
		}
		return setting{key, label, sanitize(shown), v, false}
	}
	boolean := func(key, label string, v *bool, what string) setting {
		if v == nil {
			return setting{key, label, what + " excluded (default)", false, true}
		}
		if *v {
			return setting{key, label, what + " included", true, false}
		}
		return setting{key, label, what + " excluded", false, false}
	}
	owner := setting{"project.owner", "Owner", f.Owner, f.Owner, false}
	if f.Owner == "" {
		owner = setting{"project.owner", "Owner", "not set", nil, true}
	}
	number := setting{"project.number", "Project", strconv.Itoa(f.Number), f.Number, false}
	if f.Number == 0 {
		number = setting{"project.number", "Project", "not set", nil, true}
	}
	repos := setting{"repositories", "Repositories", strings.Join(f.Repositories, ", "), f.Repositories, false}
	if len(f.Repositories) == 0 {
		repos = setting{"repositories", "Repositories", "current repository (default)", nil, true}
	}
	return []setting{
		owner, number, repos,
		list("filters.types", "Types", f.Types, []string{filter.TypeIssue, filter.TypePullRequest}, "none"),
		list("filters.states", "States", f.States, []string{filter.StateOpen}, "none"),
		list("filters.labels", "Labels", f.Labels, nil, "any"),
		list("filters.exclude-labels", "Exclude labels", f.ExcludeLabels, nil, "none"),
		list("filters.authors", "Authors", f.Authors, nil, "any"),
		boolean("filters.drafts", "Drafts", f.Drafts, "Draft pull requests"),
		boolean("filters.bots", "Bots", f.Bots, "Bot items"),
	}
}
