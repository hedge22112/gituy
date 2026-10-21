// Package spec loads the command definitions gituy builds commands from.
// Commands are data (YAML), not code, so adding one is a data change.
package spec

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed commands/*.yaml
var builtin embed.FS

// Danger levels for options and commands.
const (
	DangerNone = ""
	DangerLow  = "low"  // rewrites history or is surprising
	DangerHigh = "high" // can lose work
)

// Command describes one buildable git command.
type Command struct {
	ID      string   `yaml:"id"`
	Cmd     []string `yaml:"cmd"`   // e.g. [stash, push]
	Fixed   []string `yaml:"fixed"` // flags always present, e.g. [--continue]
	Label   string   `yaml:"label"` // display name when cmd alone is ambiguous
	Summary string   `yaml:"summary"`
	Help    string   `yaml:"help"`
	Danger  string   `yaml:"danger"`
	Options []Option `yaml:"options"`
	Args    []Arg    `yaml:"args"`
}

// Option is a flag. Options sharing a Group are mutually exclusive.
type Option struct {
	Flag      string   `yaml:"flag"`
	Desc      string   `yaml:"desc"`
	Group     string   `yaml:"group"`
	Value     string   `yaml:"value"` // placeholder when the flag takes a value
	Default   bool     `yaml:"default"`
	Danger    string   `yaml:"danger"`
	Conflicts []string `yaml:"conflicts"`
}

// TakesValue reports whether the option needs a value, e.g. -m <message>.
func (o Option) TakesValue() bool { return o.Value != "" }

// Arg is a positional argument.
type Arg struct {
	Name     string `yaml:"name"`
	Desc     string `yaml:"desc"`
	Source   string `yaml:"source"` // where choices come from; see Sources
	Multi    bool   `yaml:"multi"`
	Required bool   `yaml:"required"`
	DashDash bool   `yaml:"dashdash"` // emit "--" before this argument
}

// Sources a picker can draw choices from.
var Sources = map[string]bool{
	"text": true, "branches": true, "remote_branches": true, "all_branches": true,
	"remotes": true, "changed": true, "unstaged": true, "staged": true,
	"untracked": true, "conflicted": true, "commits": true, "stashes": true,
}

// Set is a loaded collection of commands.
type Set struct {
	byID  map[string]*Command
	order []string
}

// Builtin returns the embedded command set.
func Builtin() (*Set, error) {
	return Load(builtin, "commands")
}

// Load reads every *.yaml file in dir of fsys.
func Load(fsys fs.FS, dir string) (*Set, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	set := &Set{byID: map[string]*Command{}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		var cmds []Command
		if err := yaml.Unmarshal(data, &cmds); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		for i := range cmds {
			c := cmds[i]
			if err := c.validate(); err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			if _, dup := set.byID[c.ID]; dup {
				return nil, fmt.Errorf("%s: duplicate command id %q", e.Name(), c.ID)
			}
			set.byID[c.ID] = &c
			set.order = append(set.order, c.ID)
		}
	}
	sort.Strings(set.order)
	return set, nil
}

// Get returns the command with id, or nil.
func (s *Set) Get(id string) *Command { return s.byID[id] }

// All returns commands sorted by id.
func (s *Set) All() []*Command {
	out := make([]*Command, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.byID[id])
	}
	return out
}

func (c *Command) validate() error {
	if c.ID == "" || len(c.Cmd) == 0 {
		return fmt.Errorf("command %q needs id and cmd", c.ID)
	}
	flags := map[string]bool{}
	for _, o := range c.Options {
		if o.Flag == "" || o.Desc == "" {
			return fmt.Errorf("%s: option needs flag and desc", c.ID)
		}
		if flags[o.Flag] {
			return fmt.Errorf("%s: duplicate flag %s", c.ID, o.Flag)
		}
		flags[o.Flag] = true
	}
	for _, o := range c.Options {
		for _, cf := range o.Conflicts {
			if !flags[cf] {
				return fmt.Errorf("%s: %s conflicts with unknown flag %s", c.ID, o.Flag, cf)
			}
		}
	}
	for i := range c.Args {
		a := &c.Args[i]
		if a.Name == "" {
			return fmt.Errorf("%s: arg needs a name", c.ID)
		}
		if a.Source == "" {
			a.Source = "text"
		}
		if !Sources[a.Source] {
			return fmt.Errorf("%s: arg %s has unknown source %q", c.ID, a.Name, a.Source)
		}
	}
	return nil
}

// Option returns the option with flag, or nil.
func (c *Command) Option(flag string) *Option {
	for i := range c.Options {
		if c.Options[i].Flag == flag {
			return &c.Options[i]
		}
	}
	return nil
}

// Title is the command as typed, e.g. "stash push", or its label.
func (c *Command) Title() string {
	if c.Label != "" {
		return c.Label
	}
	return strings.Join(append(append([]string{}, c.Cmd...), c.Fixed...), " ")
}
