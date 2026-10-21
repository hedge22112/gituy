// Package rules turns repository state into suggested commands. Rules are
// data: a condition (an expr expression over the state) and a preset that
// pre-fills the builder. Suggestions never run anything by themselves.
package rules

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"gopkg.in/yaml.v3"

	"github.com/hedge22112/gituy/internal/builder"
	"github.com/hedge22112/gituy/internal/git"
	"github.com/hedge22112/gituy/internal/spec"
)

//go:embed rules.yaml
var builtinRules []byte

// Rule is one suggestion rule.
type Rule struct {
	ID       string `yaml:"id"`
	When     string `yaml:"when"`
	Title    string `yaml:"title"`
	Explain  string `yaml:"explain"`
	Command  string `yaml:"command"`
	ForEach  string `yaml:"for_each"` // name of a list in the env; one suggestion per item
	Priority int    `yaml:"priority"`
	Preset   Preset `yaml:"preset"`

	when  *vm.Program
	title *template.Template
}

// Preset pre-fills a selection. Strings may use templates, e.g. {{.branch}}.
type Preset struct {
	Flags  []string            `yaml:"flags"`
	Values map[string]string   `yaml:"values"`
	Args   map[string][]string `yaml:"args"`
}

// Suggestion is a rule that matched the current state.
type Suggestion struct {
	ID        string
	Title     string
	Explain   string
	Command   *spec.Command
	Selection builder.Selection
	Priority  int
}

// Engine evaluates rules against state.
type Engine struct {
	rules []*Rule
	specs *spec.Set
}

// Builtin loads the embedded rules.
func Builtin(specs *spec.Set) (*Engine, error) {
	return Load(builtinRules, specs)
}

// Load parses rules from YAML and checks them against the command specs.
func Load(data []byte, specs *spec.Set) (*Engine, error) {
	var rules []*Rule
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return nil, err
	}
	sample := Env(git.State{})
	for _, r := range rules {
		c := specs.Get(r.Command)
		if c == nil {
			return nil, fmt.Errorf("rule %s: unknown command %q", r.ID, r.Command)
		}
		for _, f := range r.Preset.Flags {
			if c.Option(f) == nil {
				return nil, fmt.Errorf("rule %s: %s has no flag %s", r.ID, r.Command, f)
			}
		}
		prog, err := expr.Compile(r.When, expr.Env(sample), expr.AsBool())
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", r.ID, err)
		}
		r.when = prog
		r.title, err = template.New(r.ID).Option("missingkey=zero").Parse(r.Title)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", r.ID, err)
		}
	}
	return &Engine{rules: rules, specs: specs}, nil
}

// Env exposes state to rule expressions and templates.
func Env(st git.State) map[string]any {
	changed := append(append([]string{}, st.Unstaged...), st.Untracked...)
	remote := ""
	for _, r := range st.Remotes {
		if r == "origin" {
			remote = r
		}
	}
	if remote == "" && len(st.Remotes) > 0 {
		remote = st.Remotes[0]
	}
	return map[string]any{
		"branch":      st.Branch,
		"detached":    st.Detached,
		"has_commits": st.HasCommits,
		"upstream":    st.Upstream,
		"ahead":       st.Ahead,
		"behind":      st.Behind,
		"staged":      st.Staged,
		"unstaged":    st.Unstaged,
		"untracked":   st.Untracked,
		"changed":     changed,
		"conflicted":  st.Conflicted,
		"op":          string(st.Op),
		"stash":       st.StashCount,
		"head_pushed": st.HeadPushed,
		"remotes":     st.Remotes,
		"remote":      remote,
		"clean":       st.Clean(),
		"item":        "",

		"in_repo":         !st.NotRepo,
		"upstream_gone":   st.UpstreamGone,
		"last_action":     st.LastAction,
		"default_branch":  st.DefaultBranch,
		"merged_branches": st.MergedBranches,
		"gone_branches":   st.GoneBranches,
	}
}

// Suggest returns matching suggestions, highest priority first.
func (e *Engine) Suggest(st git.State) []Suggestion {
	env := Env(st)
	var out []Suggestion
	for _, r := range e.rules {
		ok, err := expr.Run(r.when, env)
		if err != nil || ok != true {
			continue
		}
		items := []string{""}
		if r.ForEach != "" {
			list, _ := env[r.ForEach].([]string)
			items = list
		}
		for _, item := range items {
			env["item"] = item
			s, err := e.suggestion(r, env)
			if err == nil {
				if item != "" {
					s.ID += ":" + item
				}
				out = append(out, s)
			}
		}
		env["item"] = ""
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	return out
}

func (e *Engine) suggestion(r *Rule, env map[string]any) (Suggestion, error) {
	c := e.specs.Get(r.Command)
	var title strings.Builder
	if err := r.title.Execute(&title, env); err != nil {
		return Suggestion{}, err
	}
	sel := builder.NewSelection(c)
	for _, f := range r.Preset.Flags {
		if !sel.Flags[f] {
			sel.Toggle(c, f)
		}
	}
	for k, v := range r.Preset.Values {
		val, err := expand(v, env)
		if err != nil {
			return Suggestion{}, err
		}
		sel.Values[k] = val
	}
	for name, vals := range r.Preset.Args {
		for _, v := range vals {
			// "{{list}}" style presets expand a whole list from the env.
			if key, ok := listRef(v); ok {
				list, _ := env[key].([]string)
				sel.Args[name] = append(sel.Args[name], list...)
				continue
			}
			val, err := expand(v, env)
			if err != nil {
				return Suggestion{}, err
			}
			if val != "" {
				sel.Args[name] = append(sel.Args[name], val)
			}
		}
	}
	explain, err := expand(r.Explain, env)
	if err != nil {
		return Suggestion{}, err
	}
	return Suggestion{
		ID: r.ID, Title: title.String(), Explain: explain,
		Command: c, Selection: sel, Priority: r.Priority,
	}, nil
}

// listRef recognises a preset value of the form "@name", meaning
// "every item of list name".
func listRef(v string) (string, bool) {
	if strings.HasPrefix(v, "@") {
		return v[1:], true
	}
	return "", false
}

func expand(s string, env map[string]any) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	t, err := template.New("").Option("missingkey=zero").Parse(s)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	err = t.Execute(&b, env)
	return b.String(), err
}
