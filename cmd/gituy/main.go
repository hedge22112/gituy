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
	"path/filepath"
	"strings"
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
	if err := checkGit(ctx, git.Runner{Dir: *dir}); err != nil {
		fmt.Fprintln(os.Stderr, "gituy:", err)
		return 1
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

	workDir := *dir
	var activity []ui.Activity
	var lastFetch time.Time
	for {
		// Checked every time: running init or clone can create a repo.
		fg := git.Runner{Dir: workDir}
		inRepo := fg.IsRepo(ctx)
		gitDir := ""
		if inRepo {
			gitDir, _ = fg.GitDir(ctx)
		}
		cfg, err := config.Load(gitDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gituy: config:", err)
			return 1
		}
		if *noFetch {
			cfg.Fetch.Enabled = false
		}

		model := ui.New(ui.Model{Specs: specs, Rules: engine, Cfg: cfg, Git: fg, Activity: &activity, NoRepo: !inRepo})
		var sup *bg.Supervisor
		if inRepo {
			sup = &bg.Supervisor{
				Git:       git.Runner{Dir: workDir, Background: true},
				Cfg:       cfg,
				LastFetch: lastFetch,
			}
			model.Sup = sup
		}

		// The UI draws on stderr so --print output on stdout stays clean,
		// e.g. cmd=$(gituy --print).
		p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithOutput(os.Stderr), tea.WithReportFocus())
		bgCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		if sup != nil {
			sup.Send = func(msg any) { p.Send(msg) }
			go func() { sup.Run(bgCtx); close(done) }()
		} else {
			close(done)
		}
		_, err = p.Run()
		cancel()
		<-done // background git commands have stopped before ours runs
		if sup != nil {
			lastFetch = sup.LastFetched()
		}
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

		code, runErr := execGit(workDir, res.Argv)
		activity = append(activity, ui.Activity{At: time.Now(), Argv: res.Argv, Err: runErr})
		if runErr == nil && !inRepo {
			// carry on in the repository that was just created
			if d := createdRepoDir(specs, res.Argv); filepath.IsAbs(d) {
				workDir = d
				fmt.Fprintf(os.Stderr, "gituy: now working in %s\n", workDir)
			} else if d != "" {
				workDir = filepath.Join(workDir, d)
				fmt.Fprintf(os.Stderr, "gituy: now working in %s\n", workDir)
			}
		}
		if !cfg.ReturnAfterRun {
			return code
		}
		if !waitForReturn() {
			return code
		}
	}
}

// createdRepoDir returns the directory a successful init or clone created,
// relative to where it ran ("" if it was the current directory).
func createdRepoDir(specs *spec.Set, argv []string) string {
	c, sel := builder.Match(specs, argv, nil)
	if d := sel.Args["directory"]; len(d) > 0 {
		return d[0]
	}
	if c.ID == "clone" && len(sel.Args["url"]) > 0 {
		return cloneDirName(sel.Args["url"][0])
	}
	return ""
}

// cloneDirName mirrors git's default: the last path component of the URL
// without ".git", e.g. https://host/org/repo.git and host:org/repo -> repo.
func cloneDirName(url string) string {
	url = strings.TrimRight(url, "/")
	url = strings.TrimSuffix(url, "/.git")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		url = url[i+1:]
	}
	return strings.TrimSuffix(url, ".git")
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
