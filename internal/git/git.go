// Package git is a thin wrapper around the git binary. Everything gituy
// knows about a repository comes from git's machine-readable output.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes git commands in a directory.
type Runner struct {
	Dir string
	// Background marks commands that must never prompt the user
	// (background fetch, status polling).
	Background bool
}

// Error carries git's stderr alongside the exit error.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Run executes git with args and returns stdout.
func (r Runner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = os.Environ()
	if r.Background {
		cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+batchSSH())
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// batchSSH returns an ssh command that fails instead of prompting,
// preserving any ssh command the user already configured.
func batchSSH() string {
	base := os.Getenv("GIT_SSH_COMMAND")
	if base == "" {
		base = "ssh"
	}
	return base + " -o BatchMode=yes"
}

// IsRepo reports whether dir is inside a git work tree.
func (r Runner) IsRepo(ctx context.Context) bool {
	out, err := r.Run(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// GitDir returns the absolute path of the repository's git directory.
func (r Runner) GitDir(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "rev-parse", "--absolute-git-dir")
	return strings.TrimSpace(out), err
}

// Version returns git's version as major, minor.
func (r Runner) Version(ctx context.Context) (int, int, error) {
	out, err := r.Run(ctx, "version")
	if err != nil {
		return 0, 0, err
	}
	return parseVersion(out)
}

func parseVersion(out string) (int, int, error) {
	// "git version 2.43.0" or "git version 2.39.3 (Apple Git-145)"
	fields := strings.Fields(out)
	if len(fields) < 3 {
		return 0, 0, errors.New("unexpected git version output: " + out)
	}
	var major, minor int
	if _, err := fmt.Sscanf(fields[2], "%d.%d", &major, &minor); err != nil {
		return 0, 0, fmt.Errorf("unexpected git version %q", fields[2])
	}
	return major, minor, nil
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
