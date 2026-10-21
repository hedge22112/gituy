package bg

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hedge22112/gituy/internal/config"
	"github.com/hedge22112/gituy/internal/forge"
	"github.com/hedge22112/gituy/internal/git"
)

type stubForge struct {
	calls  atomic.Int32
	queued atomic.Int32
}

func (f *stubForge) Status(ctx context.Context, withQueue bool) forge.Status {
	f.calls.Add(1)
	st := forge.Status{Kind: forge.GitHub, Installed: true, Authed: true, PR: &forge.PR{Number: 42, State: "open"}}
	if withQueue {
		f.queued.Add(1)
		st.ReviewQueue = 3
	}
	return st
}

func TestForgeLoop(t *testing.T) {
	dir := watchRepo(t)
	stub := &stubForge{}
	cfg := config.Default()
	cfg.Fetch.Enabled, cfg.Status.Watch, cfg.Status.PollInterval = false, false, 0
	cfg.Forge.Interval = time.Hour // only explicit triggers in this test

	msgs := make(chan ForgeMsg, 100)
	s := &Supervisor{
		Git: git.Runner{Dir: dir, Background: true}, Cfg: cfg,
		NewForge: func(context.Context) (ForgeStatuser, *forge.Client) { return stub, nil },
		Send: func(m any) {
			if f, ok := m.(ForgeMsg); ok {
				msgs <- f
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); waitStopped(t, done) }()

	next := func() ForgeMsg {
		t.Helper()
		select {
		case m := <-msgs:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("no ForgeMsg")
		}
		return ForgeMsg{}
	}
	quiet := func() {
		t.Helper()
		select {
		case m := <-msgs:
			t.Fatalf("unexpected ForgeMsg %+v", m)
		case <-time.After(300 * time.Millisecond):
		}
	}

	first := next()
	if first.Status.PR == nil || first.Status.PR.Number != 42 || first.Status.ReviewQueue != 3 {
		t.Fatalf("first = %+v", first.Status)
	}

	// A second check soon after keeps the review count without searching again.
	s.ForgeNow()
	if m := next(); m.Status.ReviewQueue != 3 || stub.queued.Load() != 1 {
		t.Fatalf("second: queue=%d searches=%d", m.Status.ReviewQueue, stub.queued.Load())
	}

	// While unfocused nothing is checked; regaining focus catches up once.
	s.SetFocused(false)
	s.ForgeNow()
	quiet()
	calls := stub.calls.Load()
	s.SetFocused(true)
	next()
	if got := stub.calls.Load(); got != calls+1 {
		t.Fatalf("calls after refocus = %d, want %d", got, calls+1)
	}
	s.SetFocused(true) // already focused and nothing stale: no extra check
	quiet()
}

func TestForgeLoopWithoutForge(t *testing.T) {
	dir := watchRepo(t) // no remotes at all
	cfg := config.Default()
	cfg.Fetch.Enabled, cfg.Status.Watch, cfg.Status.PollInterval = false, false, 0
	got := make(chan ForgeMsg, 1)
	s := &Supervisor{Git: git.Runner{Dir: dir, Background: true}, Cfg: cfg, Send: func(m any) {
		if f, ok := m.(ForgeMsg); ok {
			got <- f
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); waitStopped(t, done) }()
	select {
	case m := <-got:
		t.Fatalf("forge status sent without a forge: %+v", m)
	case <-time.After(500 * time.Millisecond):
	}
}
