// Package forge talks to GitHub and GitLab through their CLIs (gh and
// glab). gituy never handles tokens itself: if the CLI is installed and
// logged in, pull/merge request features light up; otherwise they stay
// out of the way.
package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/hedge22112/gituy/internal/git"
)

// Kind is a code-hosting service.
type Kind string

const (
	None   Kind = ""
	GitHub Kind = "github"
	GitLab Kind = "gitlab"
)

// CLI is the command-line tool for the service.
func (k Kind) CLI() string {
	switch k {
	case GitHub:
		return "gh"
	case GitLab:
		return "glab"
	}
	return ""
}

// PR is a pull request (GitHub) or merge request (GitLab), normalised.
type PR struct {
	Number    int
	Title     string
	URL       string
	State     string // open, merged, closed
	Draft     bool
	Review    string // approved, changes_requested, review_required, or ""
	Checks    string // passing, failing, pending, none
	Behind    bool   // base branch has moved on; needs updating
	Conflicts bool
	Base      string
}

// Status is what gituy knows about the forge for the current branch.
type Status struct {
	Kind        Kind
	Host        string
	Installed   bool // the CLI is on PATH
	Authed      bool // and logged in to Host
	PR          *PR  // nil when the branch has no PR/MR
	ReviewQueue int  // PRs/MRs waiting for the user's review
	Err         error
}

// CLI is the tool in use, e.g. "gh".
func (s Status) CLI() string { return s.Kind.CLI() }

// Ready reports whether the CLI can be used.
func (s Status) Ready() bool { return s.Kind != None && s.Installed && s.Authed }

// Client runs the forge CLI for one repository.
type Client struct {
	Kind Kind
	Host string
	Dir  string

	mu     sync.Mutex
	authed bool // cached once true; rechecked after an auth error
}

// ErrNoCLI is returned when the CLI isn't installed.
var ErrNoCLI = errors.New("CLI not installed")

// Run executes the CLI without ever prompting (it runs in the
// background, with nobody to answer).
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	bin := c.Kind.CLI()
	if bin == "" {
		return "", errors.New("no forge")
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", ErrNoCLI
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(),
		"GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_SPINNER_DISABLED=1",
		"NO_PROMPT=true", "GLAB_CHECK_UPDATE=false",
		"NO_COLOR=1", "CLICOLOR=0", "PAGER=cat", "GH_PAGER=cat", "GLAB_PAGER=cat",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s %s: %s", bin, strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// Installed reports whether the CLI is on PATH.
func (c *Client) Installed() bool {
	_, err := exec.LookPath(c.Kind.CLI())
	return err == nil
}

// Authed reports whether the CLI is logged in to the host.
func (c *Client) Authed(ctx context.Context) bool {
	c.mu.Lock()
	ok := c.authed
	c.mu.Unlock()
	if ok {
		return true
	}
	_, err := c.Run(ctx, "auth", "status", "--hostname", c.Host)
	c.mu.Lock()
	c.authed = err == nil
	c.mu.Unlock()
	return err == nil
}

func (c *Client) forgetAuth() {
	c.mu.Lock()
	c.authed = false
	c.mu.Unlock()
}

// Status reads the PR/MR for the current branch, and optionally how many
// are waiting for the user's review.
func (c *Client) Status(ctx context.Context, withQueue bool) Status {
	st := Status{Kind: c.Kind, Host: c.Host, Installed: c.Installed()}
	if !st.Installed {
		return st
	}
	if st.Authed = c.Authed(ctx); !st.Authed {
		return st
	}
	switch c.Kind {
	case GitHub:
		st.PR, st.Err = c.ghPR(ctx)
	case GitLab:
		st.PR, st.Err = c.glabMR(ctx)
	}
	if st.Err != nil && isAuthError(st.Err) {
		c.forgetAuth()
		st.Authed, st.Err = false, nil
		return st
	}
	if withQueue && st.Err == nil {
		st.ReviewQueue, _ = c.reviewQueue(ctx)
	}
	return st
}

func isAuthError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "auth login") || strings.Contains(s, "not logged in") ||
		strings.Contains(s, "401") || strings.Contains(s, "authentication")
}

// Detect works out which forge hosts the repository's remote. overrides
// maps hostnames to "github" or "gitlab" for self-hosted instances whose
// names don't say. Returns nil when there's no supported forge.
func Detect(ctx context.Context, r git.Runner, remote string, overrides map[string]string) *Client {
	if remote == "" {
		return nil
	}
	out, err := r.Run(ctx, "remote", "get-url", remote)
	if err != nil {
		return nil
	}
	host := HostOf(strings.TrimSpace(out))
	if host == "" {
		return nil
	}
	kind := KindOf(host, overrides)
	if kind == None {
		// A self-hosted instance with an unremarkable name: ask whichever
		// CLI is installed whether it's logged in there.
		for _, k := range []Kind{GitHub, GitLab} {
			c := &Client{Kind: k, Host: host, Dir: r.Dir}
			if c.Installed() && c.Authed(ctx) {
				return c
			}
		}
		return nil
	}
	return &Client{Kind: kind, Host: host, Dir: r.Dir}
}

// KindOf guesses the forge from a hostname.
func KindOf(host string, overrides map[string]string) Kind {
	if k, ok := overrides[host]; ok {
		return Kind(k)
	}
	switch {
	case host == "github.com" || strings.HasPrefix(host, "github.") || strings.Contains(host, ".github."):
		return GitHub
	case host == "gitlab.com" || strings.HasPrefix(host, "gitlab.") || strings.Contains(host, ".gitlab."):
		return GitLab
	}
	return None
}

// HostOf extracts the hostname from a git remote URL: https://host/..,
// ssh://user@host:port/.., or scp-like user@host:path. Local paths give "".
func HostOf(remote string) string {
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.Scheme == "file" {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	// scp-like: [user@]host:path, but not a Windows path like C:\x
	at := strings.LastIndex(remote, "@")
	colon := strings.Index(remote, ":")
	if colon <= 0 || strings.ContainsAny(remote[:colon], `/\`) || (colon == 1 && at < 0) {
		return ""
	}
	return strings.ToLower(remote[at+1 : colon])
}
