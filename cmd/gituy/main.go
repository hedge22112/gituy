// Command gituy is an interactive git command builder. It suggests
// commands based on the repository's state, explains every option, and
// always shows the exact git command it will run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/hedge22112/gituy/internal/bg"
	"github.com/hedge22112/gituy/internal/builder"
	"github.com/hedge22112/gituy/internal/config"
	"github.com/hedge22112/gituy/internal/git"
	"github.com/hedge22112/gituy/internal/rules"
	"github.com/hedge22112/gituy/internal/spec"
	"github.com/hedge22112/gituy/internal/ui"
)

// Set by the release build.
var (
	version = "dev"
	commit  = ""
)

// Oldest git with every flag gituy relies on (e.g. fetch --no-auto-maintenance).
const minMajor, minMinor = 2, 30

func main() {
	os.Exit(run())
}

func run() int {
	var (
		dir       = flag.String("C", ".", "run as if started in this `directory`")
		printOnly = flag.Bool("print", false, "print the chosen command instead of running it")
		noFetch   = flag.Bool("no-fetch", false, "disable background fetching for this session")
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: gituy [flags]\n\nAn interactive git command builder.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVer {
		fmt.Println("gituy", version, commit)
		return 0
	}

	ctx := context.Background()
	fg := git.Runner{Dir: *dir}
	if err := checkGit(ctx, fg); err != nil {
		fmt.Fprintln(os.Stderr, "gituy:", err)
		return 1
	}
	if !fg.IsRepo(ctx) {
		fmt.Fprintln(os.Stderr, "gituy: not inside a git repository (try git init or git clone)")
		return 1
	}
	gitDir, _ := fg.GitDir(ctx)
	cfg, err := config.Load(gitDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gituy: config:", err)
		return 1
	}
	if *noFetch {
		cfg.Fetch.Enabled = false
	}

	specs, err := spec.Builtin()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gituy: command specs:", err)
		return 1
	}
	engine, err := rules.Builtin(specs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gituy: rules:", err)
		return 1
	}

	var activity []ui.Activity
	var lastFetch time.Time
	for {
		model := ui.New(ui.Model{Specs: specs, Rules: engine, Cfg: cfg, Git: fg, Activity: &activity})
		sup := &bg.Supervisor{
			Git:       git.Runner{Dir: *dir, Background: true},
			Cfg:       cfg,
			LastFetch: lastFetch,
		}
		model.Sup = sup

		// The UI draws on stderr so --print output on stdout stays clean,
		// e.g. cmd=$(gituy --print).
		p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
		sup.Send = func(msg any) { p.Send(msg) }
		bgCtx, cancel := context.WithCancel(ctx)
		go sup.Run(bgCtx)
		_, err := p.Run()
		cancel()
		lastFetch = sup.LastFetched()
		if err != nil {
			fmt.Fprintln(os.Stderr, "gituy:", err)
			return 1
		}

		res := model.Result
		if res.Action != ui.ActionRun {
			return 0
		}
		if *printOnly {
			fmt.Println(builder.Quote(res.Argv))
			return 0
		}

		code, runErr := execGit(*dir, res.Argv)
		activity = append(activity, ui.Activity{At: time.Now(), Argv: res.Argv, Err: runErr})
		if !cfg.ReturnAfterRun {
			return code
		}
		if !waitForReturn() {
			return code
		}
	}
}

func checkGit(ctx context.Context, r git.Runner) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed or not on PATH")
	}
	major, minor, err := r.Version(ctx)
	if err != nil {
		return err
	}
	if major < minMajor || (major == minMajor && minor < minMinor) {
		return fmt.Errorf("git %d.%d is too old; gituy needs %d.%d or newer", major, minor, minMajor, minMinor)
	}
	return nil
}

// execGit runs the chosen command attached to the terminal, so editors,
// pagers, hooks and credential prompts all work normally.
func execGit(dir string, argv []string) (int, error) {
	fmt.Fprintf(os.Stderr, "\033[1m$ %s\033[0m\n", builder.Quote(argv))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		fmt.Fprintf(os.Stderr, "\033[31mexit status %d\033[0m\n", exit.ExitCode())
		return exit.ExitCode(), err
	default:
		fmt.Fprintln(os.Stderr, err)
		return 1, err
	}
}

// waitForReturn asks whether to go back to gituy. It reads a single key.
func waitForReturn() bool {
	fmt.Fprint(os.Stderr, "\n\033[2menter: back to gituy · q: quit\033[0m ")
	defer fmt.Fprintln(os.Stderr)
	fd := os.Stdin.Fd()
	if !term.IsTerminal(fd) {
		return false
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer term.Restore(fd, state)
	buf := make([]byte, 1)
	if _, err := os.Stdin.Read(buf); err != nil {
		return false
	}
	switch buf[0] {
	case 'q', 'Q', 3 /* ctrl+c */, 4 /* ctrl+d */, 27 /* esc */ :
		return false
	}
	return true
}
