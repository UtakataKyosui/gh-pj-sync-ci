package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rivo/uniseg"
)

const (
	binName   = "gh pj-synker"
	docsURL   = "https://github.com/UtakataKyosui/gh-pj-synker#readme"
	maxSyntx  = 28
	helpWidth = 80
)

type argDoc struct{ syntax, desc string }

type example struct {
	desc string
	argv []string
}

type command struct {
	name     string
	usage    string
	summary  string
	args     []argDoc
	options  []option
	examples []example
	notes    []string
}

func (c command) path() string { return binName + " " + c.name }

// allOptions is the full option set a leaf command parses: its own plus the globals.
func (c command) allOptions() []option {
	return append(append([]option(nil), c.options...), globalOpts...)
}

var rootCommand = command{
	usage:   binName + " <command> [options]",
	summary: "Add issues and pull requests to GitHub Projects",
}

func commands() []command { return []command{addCommand, helpCommand} }

var helpCommand = command{
	name:    "help",
	usage:   binName + " help [<command>]",
	summary: "Show help for a command",
}

func findCommand(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

type helpWriter struct {
	w  io.Writer
	st Style
}

func (h helpWriter) styled() bool { return h.st.Rich && h.st.Color }

func (h helpWriter) heading(s string) {
	if h.styled() {
		s = h.st.paint(ansiBold+";"+ansiMagenta, s)
	}
	fmt.Fprintln(h.w, s)
}

func (h helpWriter) entries(rows [][2]string) {
	width := 0
	for _, r := range rows {
		if w := uniseg.StringWidth(r[0]); w > width && w <= maxSyntx {
			width = w
		}
	}
	for _, r := range rows {
		name := r[0]
		if h.styled() {
			name = h.st.paint(ansiBold, name)
		}
		// When syntax and description don't fit side by side, the description moves below.
		if uniseg.StringWidth(r[0]) > width || 2+width+2+uniseg.StringWidth(r[1]) > helpWidth {
			fmt.Fprintf(h.w, "  %s\n      %s\n", name, r[1])
			continue
		}
		fmt.Fprintf(h.w, "  %s%s  %s\n", name, strings.Repeat(" ", width-uniseg.StringWidth(r[0])), r[1])
	}
}

func optionRows(opts []option) [][2]string {
	sorted := append([]option(nil), opts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	var rows [][2]string
	for _, o := range sorted {
		desc := o.desc
		if o.def != "" {
			desc += " (default: " + o.def + ")"
		}
		rows = append(rows, [2]string{o.syntax(), desc})
	}
	return rows
}

func renderHelp(w io.Writer, st Style, c command) {
	h := helpWriter{w, st}
	usage := c.usage
	if h.styled() {
		usage = h.st.paint(ansiBold, usage)
	}
	fmt.Fprintln(w, usage)
	fmt.Fprintln(w, c.summary)

	if c.name == "" {
		fmt.Fprintln(w)
		h.heading("Commands:")
		var rows [][2]string
		for _, sub := range commands() {
			rows = append(rows, [2]string{sub.name, sub.summary})
		}
		h.entries(rows)
		fmt.Fprintln(w)
		h.heading("Global Options:")
		h.entries(optionRows(globalOpts))
	} else {
		if len(c.args) > 0 {
			fmt.Fprintln(w)
			h.heading("Arguments:")
			var rows [][2]string
			for _, a := range c.args {
				rows = append(rows, [2]string{a.syntax, a.desc})
			}
			h.entries(rows)
		}
		if len(c.options) > 0 {
			fmt.Fprintln(w)
			h.heading("Options:")
			h.entries(optionRows(c.options))
		}
		fmt.Fprintln(w)
		h.heading("Global Options:")
		fmt.Fprintf(w, "  See `%s --help`\n", binName)
	}
	if len(c.examples) > 0 {
		fmt.Fprintln(w)
		h.heading("Examples:")
		for i, ex := range c.examples {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "  %s\n    %s\n", ex.desc, joinArgv(ex.argv))
		}
	}
	if len(c.notes) > 0 {
		fmt.Fprintln(w)
		h.heading("Notes:")
		for _, n := range c.notes {
			fmt.Fprintf(w, "  %s\n", n)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Learn more: %s\n", docsURL)
}
