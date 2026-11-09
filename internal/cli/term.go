package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// Streams is everything the CLI reads from its environment. Commands never look at
// TTYs or environment variables directly; they use the Mode resolved from Streams.
type Streams struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	InTTY  bool
	OutTTY bool
	ErrTTY bool
	Width  int // terminal width of stderr, 0 when unknown
	Getenv func(string) string
}

// Style is the presentation of one stream.
type Style struct {
	Rich  bool // glyphs, alignment and animation (TTY and TERM isn't dumb)
	Color bool // ANSI color
}

// Mode is the resolved interaction, format and presentation for a run.
type Mode struct {
	Interactive bool
	JSON        bool
	Out         Style
	Err         Style
	Width       int
}

type globalOptions struct {
	noColor        bool
	nonInteractive bool
}

func resolveMode(s Streams, g globalOptions, json bool) Mode {
	env := s.Getenv
	m := Mode{JSON: json, Width: s.Width}
	if m.Width <= 0 {
		m.Width = 80
	}
	m.Interactive = !g.nonInteractive && !json && env("CI") == "" && s.InTTY && s.ErrTTY
	style := func(tty bool) Style {
		rich := tty && env("TERM") != "dumb"
		color := rich
		switch {
		case g.noColor, env("NO_COLOR") != "":
			color = false
		case env("FORCE_COLOR") != "":
			color = env("FORCE_COLOR") != "0"
		}
		return Style{Rich: rich, Color: color}
	}
	m.Out, m.Err = style(s.OutTTY), style(s.ErrTTY)
	return m
}

const (
	ansiBold    = "1"
	ansiRed     = "31"
	ansiGreen   = "32"
	ansiMagenta = "35"
	ansiCyan    = "36"
)

func (s Style) paint(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// labelWidth is the label column: content starts at column 2 and values at column 18.
const labelWidth = 16

// printer writes human output to one stream (stderr for receipts).
type printer struct {
	w     io.Writer
	st    Style
	width int
}

func (p printer) line(s string) { fmt.Fprintln(p.w, s) }
func (p printer) blank()        { fmt.Fprintln(p.w) }

// text prints a content line at column 2.
func (p printer) text(s string) { p.line("  " + s) }

// row prints an aligned label/value row. A label that doesn't fit puts the value on the next line.
func (p printer) row(label, value string) {
	if uniseg.StringWidth(label) >= labelWidth {
		p.text(label)
		p.line("  " + strings.Repeat(" ", labelWidth) + value)
		return
	}
	p.line("  " + pad(label, labelWidth) + value)
}

// descRow is a row whose value is optional description text, shortened with … to fit a TTY.
func (p printer) descRow(label, value string) {
	if p.st.Rich {
		value = truncate(value, p.width-2-labelWidth)
	}
	p.row(label, value)
}

// success prints the one primary completed result.
func (p printer) success(label, value string) {
	if !p.st.Rich {
		p.line(label + " " + value)
		return
	}
	p.line(p.st.paint(ansiGreen, "✓") + " " + p.st.paint(ansiBold, pad(label, labelWidth)+value))
}

func (p printer) errorHeadline(msg string) {
	if !p.st.Rich {
		p.line(p.st.paint(ansiRed, "Error:") + " " + msg)
		return
	}
	p.line(p.st.paint(ansiRed, "✗") + " " + p.st.paint(ansiBold, msg))
}

// command prints a complete command on its own line, indented 4 spaces.
// Templates with placeholders stay in the normal foreground.
func (p printer) command(cmd string) {
	if p.st.Rich && !strings.Contains(cmd, "<") {
		cmd = p.st.paint(ansiCyan, cmd)
	}
	p.line("    " + cmd)
}

func pad(s string, width int) string {
	if w := uniseg.StringWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s + " "
}

func truncate(s string, width int) string {
	if width < 1 || uniseg.StringWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cw := g.Width()
		if w+cw > width-1 {
			break
		}
		b.WriteString(g.Str())
		w += cw
	}
	return b.String() + "…"
}

// sanitize makes remote text safe for a terminal: control, escape, bidirectional and
// zero-width characters are removed, and newlines and tabs become visible escapes.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			// dropped: C0/C1 controls (including ESC) and format characters (bidi, zero-width)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// plural renders a count with the right noun form.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%s %s", commas(n), many)
}

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
