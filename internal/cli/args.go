package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type optKind int

const (
	boolOpt optKind = iota
	stringOpt
	intOpt
	listOpt // comma separated; repeating the option appends
)

// option is the metadata every parse, help page and suggestion is generated from.
type option struct {
	name  string
	short string
	kind  optKind
	hint  string // value hint, such as <LOGIN>
	desc  string
	def   string // shown as (default: …)
}

func (o option) syntax() string {
	s := "--" + o.name
	if o.short != "" {
		s = "-" + o.short + ", " + s
	}
	if o.hint != "" {
		s += " " + o.hint
	}
	return s
}

var globalOpts = []option{
	{name: "help", short: "h", kind: boolOpt, desc: "Show help for a command"},
	{name: "no-color", kind: boolOpt, desc: "Turn off color"},
	{name: "non-interactive", kind: boolOpt, desc: "Never prompt; fail when input or confirmation is missing"},
	{name: "version", short: "v", kind: boolOpt, desc: "Print the version"},
}

type parsed struct {
	bools       map[string]bool
	strs        map[string]string
	lists       map[string][]string
	set         map[string]bool
	positionals []string
}

func (p parsed) has(name string) bool { return p.set[name] }

// parse parses args against opts. It accepts --name value, --name=value, -x value,
// --flag, --no-flag, --flag=true|false and "--". Unknown options and unexpected values
// are usage errors. Unique-prefix matching is never done.
func parse(args []string, opts []option, cmdPath string) (parsed, *Error) {
	p := parsed{bools: map[string]bool{}, strs: map[string]string{}, lists: map[string][]string{}, set: map[string]bool{}}
	byName, byShort := map[string]option{}, map[string]option{}
	for _, o := range opts {
		byName[o.name] = o
		if o.short != "" {
			byShort[o.short] = o
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.positionals = append(p.positionals, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			p.positionals = append(p.positionals, a)
			continue
		}
		var o option
		var ok bool
		name, value, hasValue := a, "", false
		if strings.HasPrefix(a, "--") {
			name = a[2:]
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name, value, hasValue = name[:eq], name[eq+1:], true
			}
			o, ok = byName[name]
			if !ok && strings.HasPrefix(name, "no-") {
				if neg, found := byName[strings.TrimPrefix(name, "no-")]; found && neg.kind == boolOpt && !hasValue {
					p.bools[neg.name], p.set[neg.name] = false, true
					continue
				}
			}
			if !ok {
				return p, unknownOption("--"+name, opts, cmdPath)
			}
		} else {
			name = a[1:]
			if len(name) > 1 {
				value, hasValue = name[1:], true
				name = name[:1]
			}
			if o, ok = byShort[name]; !ok {
				return p, unknownOption("-"+name, opts, cmdPath)
			}
		}
		flag := "--" + o.name
		if o.kind == boolOpt {
			v := true
			if hasValue {
				b, err := strconv.ParseBool(value)
				if err != nil || (value != "true" && value != "false") {
					return p, usageError("invalid_arguments", fmt.Sprintf("%s takes true or false, not %q.", flag, value), cmdPath)
				}
				v = b
			}
			p.bools[o.name], p.set[o.name] = v, true
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return p, usageError("invalid_arguments", fmt.Sprintf("%s needs a value, such as %s %s.", flag, flag, o.hint), cmdPath)
			}
			i++
			value = args[i]
		}
		switch o.kind {
		case listOpt:
			p.lists[o.name] = append(p.lists[o.name], value)
		default:
			if p.set[o.name] {
				return p, usageError("invalid_arguments", fmt.Sprintf("Can't use %s more than once.", flag), cmdPath)
			}
			p.strs[o.name] = value
		}
		p.set[o.name] = true
	}
	return p, nil
}

func unknownOption(token string, opts []option, cmdPath string) *Error {
	var names []string
	for _, o := range opts {
		names = append(names, "--"+o.name)
	}
	msg := fmt.Sprintf("Unknown option %s for %s.", token, cmdPath)
	if s := suggest(token, names); s != "" {
		msg += fmt.Sprintf(" Did you mean %s?", s)
	}
	return usageError("unknown_option", msg, cmdPath)
}

func usageError(reason, msg, cmdPath string) *Error {
	return &Error{
		Exit: exitUsage, Status: statusError, Reason: reason, Message: msg,
		Recovery: []Recovery{{Instruction: "See all options", Argv: append(strings.Fields(cmdPath), "--help")}},
	}
}

// suggest returns the closest candidate within a small edit distance, or "".
func suggest(input string, candidates []string) string {
	best, bestDist := "", 3
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)
	for _, c := range sorted {
		if d := levenshtein(input, c); d < bestDist {
			best, bestDist = c, d
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

// shellQuote quotes an argument for POSIX shells when needed. Placeholders like
// <OWNER/REPO> are left as they are so they read as templates.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		return s
	}
	safe := true
	for _, r := range s {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !alnum && !strings.ContainsRune("-_./:=,@%+", r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func joinArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}
