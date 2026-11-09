// Package cli implements the gh pj-synker command line.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
	"github.com/UtakataKyosui/gh-pj-synker/internal/syncer"
)

// API is every GitHub call the CLI makes.
type API interface {
	syncer.API
	GetProject(ctx context.Context, owner string, number int) (ghapi.Project, error)
	ListItemNumbers(ctx context.Context, repo string, states []string) ([]int, error)
}

// Deps are the side effects the CLI needs, injected so tests never touch the network.
type Deps struct {
	NewAPI      func() (API, error)
	CurrentRepo func() (string, error) // the repository of the current git checkout
	Getwd       func() (string, error) // where the config file search starts
	Version     string
}

// Run runs the CLI and returns its exit code. Help and version never authenticate,
// read config or touch the network.
func Run(ctx context.Context, args []string, s Streams, d Deps) int {
	g := scanGlobals(args)
	helpMode := resolveMode(s, g, false)
	c, rest, unknown := resolveCommand(args)

	// Help wins over everything, at any depth, before any other validation.
	if c.name == "help" {
		return runHelp(rest, s, helpMode)
	}
	if wantsFlag(args, "help", "h") {
		if unknown != "" {
			return fail(s, helpMode, unknownCommand(c, unknown))
		}
		renderHelp(s.Out, helpMode.Out, c)
		return exitOK
	}
	if wantsFlag(args, "version", "v") {
		if wantsFlag(args, "json", "") {
			b, _ := json.Marshal(map[string]string{"version": d.Version})
			fmt.Fprintln(s.Out, string(b))
		} else {
			fmt.Fprintln(s.Out, d.Version)
		}
		return exitOK
	}
	if unknown != "" {
		return fail(s, helpMode, unknownCommand(c, unknown))
	}

	switch c.name {
	case "add":
		return runAdd(ctx, rest, s, d)
	case "config check":
		return runConfigCheck(rest, s, d)
	}
	// A group without an action shows its help.
	if _, err := parse(rest, globalOpts, c.path()); err != nil {
		return fail(s, helpMode, err)
	}
	renderHelp(s.Out, helpMode.Out, c)
	return exitOK
}

func runHelp(topics []string, s Streams, m Mode) int {
	var words []string
	for _, a := range topics {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	c, _, unknown := resolveCommand(words)
	if unknown != "" || c.name == "help" && len(words) > 1 {
		return fail(s, m, unknownCommand(c, unknown))
	}
	renderHelp(s.Out, m.Out, c)
	return exitOK
}

func unknownCommand(parent command, name string) *Error {
	var names []string
	for _, c := range parent.children() {
		names = append(names, c.leaf())
	}
	path := strings.TrimSpace(binName + " " + parent.name)
	msg := fmt.Sprintf("Unknown command %s for %s.", sanitize(name), path)
	if sug := suggest(name, names); sug != "" {
		msg += fmt.Sprintf(" Did you mean %s?", sug)
	}
	return &Error{Exit: exitUsage, Status: statusError, Reason: "unknown_command", Message: msg,
		Recovery: []Recovery{{Instruction: "See all commands", Argv: append(strings.Fields(path), "--help")}}}
}

// wantsFlag reports whether a Boolean option appears before "--".
func wantsFlag(args []string, long, short string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--"+long || a == "--"+long+"=true" || (short != "" && a == "-"+short) {
			return true
		}
	}
	return false
}

// scanGlobals reads the presentation options early, so errors from parsing are styled right.
func scanGlobals(args []string) globalOptions {
	return globalOptions{noColor: wantsFlag(args, "no-color", ""), nonInteractive: wantsFlag(args, "non-interactive", "")}
}

// fail renders an error: one JSON line on stderr with --json, the human layout otherwise.
func fail(s Streams, m Mode, e *Error) int {
	if m.JSON {
		fmt.Fprintln(s.Err, e.renderJSON())
		return e.Exit
	}
	p := printer{w: s.Err, st: m.Err, width: m.Width}
	e.renderHuman(p)
	return e.Exit
}
