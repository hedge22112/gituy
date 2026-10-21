package git

import (
	"context"
	"sort"
	"strings"
)

// FetchOptions controls a background fetch.
type FetchOptions struct {
	Remotes []string // empty means all remotes
	Prune   bool
	Tags    bool
}

// FetchArgs builds the argument list for a non-interactive fetch.
func FetchArgs(o FetchOptions) []string {
	args := []string{"-c", "gc.auto=0", "fetch", "--quiet", "--no-auto-maintenance"}
	if o.Prune {
		args = append(args, "--prune")
	}
	if !o.Tags {
		args = append(args, "--no-tags")
	}
	if len(o.Remotes) == 0 {
		args = append(args, "--all")
	} else if len(o.Remotes) > 1 {
		args = append(args, "--multiple")
	}
	return append(args, o.Remotes...)
}

// RefChange describes a remote-tracking ref that moved during a fetch.
type RefChange struct {
	Ref      string
	Old, New string // empty Old = created, empty New = deleted
}

// RemoteRefs returns remote-tracking refs mapped to their object ids.
func (r Runner) RemoteRefs(ctx context.Context) (map[string]string, error) {
	out, err := r.Run(ctx, "for-each-ref", "--format=%(refname) %(objectname) %(symref)", "refs/remotes")
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue // symbolic refs such as origin/HEAD
		}
		refs[strings.TrimPrefix(f[0], "refs/remotes/")] = f[1]
	}
	return refs, nil
}

// DiffRefs reports refs that differ between two snapshots.
func DiffRefs(before, after map[string]string) []RefChange {
	var changes []RefChange
	for ref, oid := range after {
		if before[ref] != oid {
			changes = append(changes, RefChange{Ref: ref, Old: before[ref], New: oid})
		}
	}
	for ref, oid := range before {
		if _, ok := after[ref]; !ok {
			changes = append(changes, RefChange{Ref: ref, Old: oid})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Ref < changes[j].Ref })
	return changes
}
