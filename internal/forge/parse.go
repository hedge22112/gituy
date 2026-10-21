package forge

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// --- GitHub (gh) --------------------------------------------------------

const ghPRFields = "number,title,url,state,isDraft,reviewDecision,statusCheckRollup,mergeStateStatus,mergeable,baseRefName"

type ghPR struct {
	Number            int    `json:"number"`
	Title             string `json:"title"`
	URL               string `json:"url"`
	State             string `json:"state"`
	IsDraft           bool   `json:"isDraft"`
	ReviewDecision    string `json:"reviewDecision"`
	MergeStateStatus  string `json:"mergeStateStatus"`
	Mergeable         string `json:"mergeable"`
	BaseRefName       string `json:"baseRefName"`
	StatusCheckRollup []struct {
		Typename   string `json:"__typename"`
		Status     string `json:"status"`     // CheckRun
		Conclusion string `json:"conclusion"` // CheckRun
		State      string `json:"state"`      // StatusContext
	} `json:"statusCheckRollup"`
}

func (c *Client) ghPR(ctx context.Context) (*PR, error) {
	out, err := c.Run(ctx, "pr", "view", "--json", ghPRFields)
	if err != nil {
		if strings.Contains(err.Error(), "no pull requests found") {
			return nil, nil
		}
		return nil, err
	}
	return parseGHPR([]byte(out))
}

func parseGHPR(data []byte) (*PR, error) {
	var p ghPR
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	pr := &PR{
		Number: p.Number, Title: p.Title, URL: p.URL, Draft: p.IsDraft, Base: p.BaseRefName,
		State:     strings.ToLower(p.State), // OPEN, MERGED, CLOSED
		Behind:    p.MergeStateStatus == "BEHIND",
		Conflicts: p.Mergeable == "CONFLICTING" || p.MergeStateStatus == "DIRTY",
	}
	switch p.ReviewDecision {
	case "APPROVED":
		pr.Review = "approved"
	case "CHANGES_REQUESTED":
		pr.Review = "changes_requested"
	case "REVIEW_REQUIRED":
		pr.Review = "review_required"
	}

	pr.Checks = "none"
	pending := false
	for _, ch := range p.StatusCheckRollup {
		if ch.Typename == "StatusContext" || (ch.Status == "" && ch.State != "") {
			switch ch.State {
			case "FAILURE", "ERROR":
				pr.Checks = "failing"
			case "PENDING", "EXPECTED":
				pending = true
			}
		} else if ch.Status != "COMPLETED" {
			pending = true
		} else {
			switch ch.Conclusion {
			case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
				pr.Checks = "failing"
			}
		}
		if pr.Checks == "failing" {
			break
		}
	}
	if pr.Checks != "failing" && len(p.StatusCheckRollup) > 0 {
		pr.Checks = "passing"
		if pending {
			pr.Checks = "pending"
		}
	}
	return pr, nil
}

// --- GitLab (glab) ------------------------------------------------------

type glabMR struct {
	IID                 int    `json:"iid"`
	Title               string `json:"title"`
	WebURL              string `json:"web_url"`
	State               string `json:"state"`
	Draft               bool   `json:"draft"`
	WorkInProgress      bool   `json:"work_in_progress"`
	DetailedMergeStatus string `json:"detailed_merge_status"`
	HasConflicts        bool   `json:"has_conflicts"`
	TargetBranch        string `json:"target_branch"`
	HeadPipeline        *struct {
		Status string `json:"status"`
	} `json:"head_pipeline"`
	Pipeline *struct {
		Status string `json:"status"`
	} `json:"pipeline"`
}

// glab's JSON is GitLab's REST API merge request object. glab only finds
// open merge requests for the current branch, so merged ones are spotted
// through the deleted-upstream rule instead.
func (c *Client) glabMR(ctx context.Context) (*PR, error) {
	out, err := c.Run(ctx, "mr", "view", "--output", "json")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no open merge request") {
			return nil, nil
		}
		return nil, err
	}
	return parseGLabMR([]byte(out))
}

func parseGLabMR(data []byte) (*PR, error) {
	var m glabMR
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	pr := &PR{
		Number: m.IID, Title: m.Title, URL: m.WebURL, Base: m.TargetBranch,
		Draft:     m.Draft || m.WorkInProgress || m.DetailedMergeStatus == "draft_status",
		Conflicts: m.HasConflicts || m.DetailedMergeStatus == "conflict",
		Behind:    m.DetailedMergeStatus == "need_rebase",
	}
	switch m.State {
	case "opened":
		pr.State = "open"
	case "merged":
		pr.State = "merged"
	default: // closed, locked
		pr.State = "closed"
	}
	switch m.DetailedMergeStatus {
	case "mergeable":
		pr.Review = "approved" // nothing (approvals included) stands in the way
	case "not_approved":
		pr.Review = "review_required"
	case "requested_changes":
		pr.Review = "changes_requested"
	}
	pipe := m.HeadPipeline
	if pipe == nil {
		pipe = m.Pipeline
	}
	pr.Checks = "none"
	if pipe != nil {
		switch pipe.Status {
		case "success":
			pr.Checks = "passing"
		case "failed", "canceled":
			pr.Checks = "failing"
		case "created", "waiting_for_resource", "preparing", "pending", "running", "scheduled":
			pr.Checks = "pending"
		}
	}
	return pr, nil
}

// --- Review queue and pickers -------------------------------------------

func (c *Client) reviewQueue(ctx context.Context) (int, error) {
	items, err := c.Choices(ctx, "review_prs", "")
	return len(items), err
}

// Choice is a pickable value (a PR number, issue number, run id) with a
// description.
type Choice struct {
	Value string
	Label string
}

// Choices lists values for a picker source: prs, review_prs, issues or
// ci_runs (for branch).
func (c *Client) Choices(ctx context.Context, source, branch string) ([]Choice, error) {
	var args []string
	switch c.Kind {
	case GitHub:
		switch source {
		case "prs":
			args = []string{"pr", "list", "--json", "number,title,headRefName", "--limit", "50"}
		case "review_prs":
			args = []string{"pr", "list", "--search", "review-requested:@me", "--json", "number,title,headRefName", "--limit", "50"}
		case "issues":
			args = []string{"issue", "list", "--json", "number,title", "--limit", "50"}
		case "ci_runs":
			args = []string{"run", "list", "--json", "databaseId,displayTitle,status,conclusion", "--limit", "20"}
			if branch != "" {
				args = append(args, "--branch", branch)
			}
		}
	case GitLab:
		switch source {
		case "prs":
			args = []string{"mr", "list", "--output", "json"}
		case "review_prs":
			args = []string{"mr", "list", "--reviewer=@me", "--output", "json"}
		case "issues":
			args = []string{"issue", "list", "--output", "json"}
		case "ci_runs":
			args = []string{"ci", "list", "--output", "json"}
		}
	}
	if args == nil {
		return nil, nil
	}
	out, err := c.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseChoices([]byte(out))
}

// parseChoices reads a JSON array of PRs, MRs, issues or runs from either
// CLI into choices.
func parseChoices(data []byte) ([]Choice, error) {
	var items []struct {
		Number       int    `json:"number"`
		IID          int    `json:"iid"`
		ID           int64  `json:"id"`
		DatabaseID   int64  `json:"databaseId"`
		Title        string `json:"title"`
		DisplayTitle string `json:"displayTitle"`
		HeadRefName  string `json:"headRefName"`
		SourceBranch string `json:"source_branch"`
		Ref          string `json:"ref"`
		Status       string `json:"status"`
		Conclusion   string `json:"conclusion"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	var out []Choice
	for _, it := range items {
		var c Choice
		switch {
		case it.Number != 0:
			c.Value = strconv.Itoa(it.Number)
		case it.IID != 0:
			c.Value = strconv.Itoa(it.IID)
		case it.DatabaseID != 0:
			c.Value = strconv.FormatInt(it.DatabaseID, 10)
		case it.ID != 0:
			c.Value = strconv.FormatInt(it.ID, 10)
		default:
			continue
		}
		title := it.Title
		if title == "" {
			title = it.DisplayTitle
		}
		var extra []string
		for _, s := range []string{it.HeadRefName, it.SourceBranch, it.Ref} {
			if s != "" {
				extra = append(extra, s)
			}
		}
		state := it.Conclusion
		if state == "" {
			state = it.Status
		}
		if state != "" {
			extra = append(extra, strings.ToLower(state))
		}
		c.Label = strings.TrimSpace(title)
		if len(extra) > 0 {
			c.Label += "  [" + strings.Join(extra, ", ") + "]"
		}
		out = append(out, c)
	}
	return out, nil
}
