package builder

import (
	"strings"

	"github.com/hedge22112/gituy/internal/spec"
)

// Match turns a typed git command line back into a command spec and a
// selection, so hand edits show up as toggles. It picks the spec that
// explains the most of argv; prefer (if non-nil) wins ties. When no spec
// fits, it returns a "custom" command that runs argv exactly as typed.
func Match(specs *spec.Set, argv []string, prefer *spec.Command) (*spec.Command, Selection) {
	if len(argv) < 2 || !spec.Programs[argv[0]] {
		sel := empty()
		sel.Trailing = argv[min(len(argv), 2):]
		return Custom(argv), sel
	}
	rest := argv[1:]

	var best *spec.Command
	var bestSel Selection
	bestScore, bestLen := 0, -1
	for _, c := range specs.All() {
		if c.Program() != argv[0] {
			continue
		}
		prefix := append(append([]string{}, c.Cmd...), c.Fixed...)
		if !hasPrefix(rest, prefix) {
			continue
		}
		sel, score := parseArgs(c, rest[len(prefix):])
		better := best == nil ||
			len(prefix) > bestLen ||
			(len(prefix) == bestLen && score > bestScore) ||
			(len(prefix) == bestLen && score == bestScore && c == prefer)
		if better {
			best, bestSel, bestScore, bestLen = c, sel, score, len(prefix)
		}
	}
	if best == nil {
		sel := empty()
		sel.Trailing = argv[2:]
		return Custom(argv), sel
	}
	return best, bestSel
}

// Custom is a command gituy has no spec for. It has no options or
// arguments, so the selection's Trailing words are the whole command.
func Custom(argv []string) *spec.Command {
	sub := "?"
	if len(argv) > 1 {
		sub = argv[1]
	}
	bin := "git"
	if len(argv) > 0 && spec.Programs[argv[0]] {
		bin = argv[0]
	}
	return &spec.Command{
		ID:      "custom",
		Bin:     bin,
		Cmd:     []string{sub},
		Summary: "Not in gituy's command list; runs exactly as typed",
	}
}

func empty() Selection {
	return Selection{Flags: map[string]bool{}, Values: map[string]string{}, Args: map[string][]string{}}
}

func hasPrefix(argv, prefix []string) bool {
	if len(prefix) > len(argv) {
		return false
	}
	for i := range prefix {
		if argv[i] != prefix[i] {
			return false
		}
	}
	return true
}

// parseArgs maps the words after the command onto c's options and
// arguments. The score counts words it understood, minus words it had to
// pass through untouched, minus unmet requirements.
func parseArgs(c *spec.Command, words []string) (Selection, int) {
	sel := empty()
	score := 0
	var before, after []string // positionals before and after "--"
	dashed := false

	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case dashed:
			after = append(after, w)
		case w == "--":
			dashed = true
		case c.Option(w) != nil:
			o := c.Option(w)
			sel.Flags[w] = true
			if o.TakesValue() && i+1 < len(words) {
				sel.Values[w] = words[i+1]
				i++
			}
			score++
		case strings.HasPrefix(w, "--") && strings.Contains(w, "="):
			name, val, _ := strings.Cut(w, "=")
			if o := c.Option(name); o != nil && o.TakesValue() {
				sel.Flags[name] = true
				sel.Values[name] = val
				score++
			} else {
				sel.Extra = append(sel.Extra, w)
				score -= 2
			}
		case strings.HasPrefix(w, "-") && w != "-":
			sel.Extra = append(sel.Extra, w)
			score -= 2
		default:
			before = append(before, w)
		}
	}

	// Without "--", positionals fill the arguments in order. With it,
	// words after "--" go to the argument that's written after "--".
	var plain, dd []*spec.Arg
	for i := range c.Args {
		a := &c.Args[i]
		if dashed && a.DashDash {
			dd = append(dd, a)
		} else {
			plain = append(plain, a)
		}
	}
	left := fill(sel, plain, before)
	left = append(left, fill(sel, dd, after)...)
	if len(left) > 0 {
		if dashed && len(dd) == 0 {
			// keep the "--" the user typed
			sel.Trailing = append([]string{"--"}, left...)
		} else {
			sel.Trailing = left
		}
		score -= 2 * len(left)
	}
	for _, a := range c.Args {
		if len(sel.Args[a.Name]) > 0 {
			score++
		} else if a.Required {
			score--
		}
	}
	// A radio group marked with a default (e.g. branch --delete) means the
	// command needs one of those flags to be the command it says it is.
	for _, o := range c.Options {
		if o.Default && o.Group != "" && !groupSet(c, sel, o.Group) {
			score--
		}
	}
	return sel, score
}

func fill(sel Selection, args []*spec.Arg, words []string) []string {
	for _, a := range args {
		if len(words) == 0 {
			break
		}
		if a.Multi {
			sel.Args[a.Name] = append([]string(nil), words...)
			words = nil
		} else {
			sel.Args[a.Name] = []string{words[0]}
			words = words[1:]
		}
	}
	return words
}

func groupSet(c *spec.Command, sel Selection, group string) bool {
	for _, o := range c.Options {
		if o.Group == group && sel.Flags[o.Flag] {
			return true
		}
	}
	return false
}
