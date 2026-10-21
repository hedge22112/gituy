package main

import (
	"testing"

	"github.com/hedge22112/gituy/internal/spec"
)

func TestCreatedRepoDir(t *testing.T) {
	specs, err := spec.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for argv, want := range map[string][]string{
		"":         {"git", "init"},
		"proj":     {"git", "init", "proj"},
		"repo":     {"git", "clone", "https://github.com/org/repo.git"},
		"tool":     {"git", "clone", "--depth", "1", "git@github.com:org/tool.git"},
		"x":        {"git", "clone", "https://example.com/org/repo/", "x"},
		"thing":    {"git", "clone", "/srv/git/thing/.git"},
		"plainurl": {"git", "clone", "https://example.com/plainurl"},
	} {
		if got := createdRepoDir(specs, want); got != argv {
			t.Errorf("%v -> %q, want %q", want, got, argv)
		}
	}
}
