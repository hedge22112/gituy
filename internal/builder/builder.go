// Package builder turns a command spec plus the user's selections into a
// git argv. It is pure: no I/O, no UI.
package builder

import (
	"fmt"
	"strings"

	"github.com/kballard/go-shellquote"

	"github.com/hedge22112/gituy/internal/spec"
)

// Selection is what the user has chosen for a command.
type Selection struct {
	Flags  map[string]bool     // toggled flags
	Values map[string]string   // values for flags that take one
	Args   map[string][]string // positional args by name

	// From a hand-edited command line: flags the spec doesn't know (kept
	// after the known flags) and positionals that didn't fit an argument
	// (kept at the end). Both are passed through as typed.
	Extra    []string
	Trailing []string
}

// NewSelection returns a selection with the spec's defaults applied.
func NewSelection(c *spec.Command) Selection {
	s := Selection{Flags: map[string]bool{}, Values: map[string]string{}, Args: map[string][]string{}}
	for _, o := range c.Options {
		if o.Default {
			s.Flags[o.Flag] = true
		}
	}
	return s
}

// Toggle flips a flag, enforcing radio groups: turning one on turns the
// rest of its group off.
func (s Selection) Toggle(c *spec.Command, flag string) {
	o := c.Option(flag)
	if o == nil {
		return
	}
	on := !s.Flags[flag]
	if on && o.Group != "" {
		for _, other := range c.Options {
			if other.Group == o.Group {
				delete(s.Flags, other.Flag)
			}
		}
	}
	if on {
		s.Flags[flag] = true
	} else {
		delete(s.Flags, flag)
	}
}

// Build returns the argv (starting with "git") and any problems that
// would make the command fail or be invalid.
func Build(c *spec.Command, s Selection) (argv []string, problems []string) {
	argv = append([]string{"git"}, c.Cmd...)
	argv = append(argv, c.Fixed...)

	for _, o := range c.Options {
		if !s.Flags[o.Flag] {
			continue
		}
		if o.TakesValue() {
			v := s.Values[o.Flag]
			if v == "" {
				problems = append(problems, fmt.Sprintf("%s needs a %s", o.Flag, o.Value))
				continue
			}
			argv = append(argv, o.Flag, v)
		} else {
			argv = append(argv, o.Flag)
		}
		for _, cf := range o.Conflicts {
			if s.Flags[cf] {
				problems = append(problems, fmt.Sprintf("%s can't be used with %s", o.Flag, cf))
			}
		}
	}
	problems = append(problems, groupConflicts(c, s)...)
	argv = append(argv, s.Extra...)

	dashed := false
	for _, a := range c.Args {
		vals := s.Args[a.Name]
		if len(vals) == 0 {
			if a.Required {
				problems = append(problems, "choose "+a.Name)
			}
			continue
		}
		if a.DashDash && !dashed {
			argv = append(argv, "--")
			dashed = true
		}
		argv = append(argv, vals...)
	}
	argv = append(argv, s.Trailing...)
	return argv, problems
}

// groupConflicts reports radio groups with more than one flag on. Toggle
// prevents this; a hand-edited command line can still do it.
func groupConflicts(c *spec.Command, s Selection) []string {
	var problems []string
	first := map[string]string{}
	for _, o := range c.Options {
		if o.Group == "" || !s.Flags[o.Flag] {
			continue
		}
		if f, ok := first[o.Group]; ok {
			problems = append(problems, fmt.Sprintf("%s can't be used with %s", o.Flag, f))
			continue
		}
		first[o.Group] = o.Flag
	}
	return problems
}

// Danger returns the highest danger level of the command and its
// selected options.
func Danger(c *spec.Command, s Selection) string {
	level := c.Danger
	for _, o := range c.Options {
		if s.Flags[o.Flag] && rank(o.Danger) > rank(level) {
			level = o.Danger
		}
	}
	return level
}

func rank(d string) int {
	switch d {
	case spec.DangerHigh:
		return 2
	case spec.DangerLow:
		return 1
	}
	return 0
}

// Quote renders argv as a shell command line, safe to copy and paste.
func Quote(argv []string) string {
	return shellquote.Join(argv...)
}

// Parse splits an edited command line back into argv. It must start with git.
func Parse(line string) ([]string, error) {
	argv, err := shellquote.Split(line)
	if err != nil {
		return nil, err
	}
	if len(argv) == 0 || argv[0] != "git" {
		return nil, fmt.Errorf("command must start with git")
	}
	for _, a := range argv {
		if strings.ContainsAny(a, "\x00") {
			return nil, fmt.Errorf("invalid character in command")
		}
	}
	return argv, nil
}

// Clone returns a deep copy, so editing it can't change the original
// (e.g. a suggestion's preset).
func (s Selection) Clone() Selection {
	c := Selection{Flags: map[string]bool{}, Values: map[string]string{}, Args: map[string][]string{}}
	for k, v := range s.Flags {
		c.Flags[k] = v
	}
	for k, v := range s.Values {
		c.Values[k] = v
	}
	for k, v := range s.Args {
		c.Args[k] = append([]string(nil), v...)
	}
	c.Extra = append([]string(nil), s.Extra...)
	c.Trailing = append([]string(nil), s.Trailing...)
	return c
}
