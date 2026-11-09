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
	Version     string
}

// Run runs the CLI and returns its exit code. Help and version never authenticate,
// read config or touch the network.
func Run(ctx context.Context, args []string, s Streams, d Deps) int {
	// Help wins over everything, at any depth, before any other validation.
	cmdName, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmdName, rest = args[0], args[1:]
	}
	g := scanGlobals(args)
	helpMode := resolveMode(s, g, false)

	if cmdName == "help" {
		return runHelp(rest, s, helpMode)
	}
	if wantsFlag(args, "help", "h") {
		if cmdName == "" {
			renderHelp(s.Out, helpMode.Out, rootCommand)
			return exitOK
		}
		if c, ok := findCommand(cmdName); ok {
			renderHelp(s.Out, helpMode.Out, c)
			return exitOK
		}
		return fail(s, helpMode, unknownCommand(cmdName))
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

	switch cmdName {
	case "":
		if _, err := parse(rest, globalOpts, binName); err != nil {
			return fail(s, helpMode, err)
		}
		renderHelp(s.Out, helpMode.Out, rootCommand)
		return exitOK
	case "add":
		return runAdd(ctx, rest, s, d)
	}
	return fail(s, helpMode, unknownCommand(cmdName))
}

func runHelp(rest []string, s Streams, m Mode) int {
	var topics []string
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			topics = append(topics, a)
		}
	}
	switch {
	case len(topics) == 0:
		renderHelp(s.Out, m.Out, rootCommand)
		return exitOK
	case len(topics) > 1:
		return fail(s, m, usageError("unknown_command",
			fmt.Sprintf("Unknown command %s for %s %s.", topics[1], binName, topics[0]), binName))
	}
	c, ok := findCommand(topics[0])
	if !ok {
		return fail(s, m, unknownCommand(topics[0]))
	}
	renderHelp(s.Out, m.Out, c)
	return exitOK
}

func unknownCommand(name string) *Error {
	var names []string
	for _, c := range commands() {
		names = append(names, c.name)
	}
	msg := fmt.Sprintf("Unknown command %s for %s.", sanitize(name), binName)
	if sug := suggest(name, names); sug != "" {
		msg += fmt.Sprintf(" Did you mean %s?", sug)
	}
	return &Error{Exit: exitUsage, Status: statusError, Reason: "unknown_command", Message: msg,
		Recovery: []Recovery{{Instruction: "See all commands", Argv: []string{"gh", "pj-synker", "--help"}}}}
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
