package daemon

import (
	"fmt"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// GoalLimit is §16.2's ceiling: a /goal condition must be paste-ready, which
// means it must fit.
const GoalLimit = 4000

// BuildGoal renders a task as a paste-ready `/goal` condition (§16.2):
// the directive from the title, context from the description and the latest
// feedback, "Done when" from the criteria plus the obligation to run
// `ht task submit` and show its output, and a stop clause that releases the
// claim with a note and files out-of-scope findings rather than doing them.
func BuildGoal(t *tasks.Task) string {
	ref := fmt.Sprintf("%d", t.Seq)
	var b strings.Builder

	b.WriteString(t.Title)
	b.WriteString(".\n")

	if d := strings.TrimSpace(t.Description); d != "" {
		b.WriteString("\nContext: ")
		b.WriteString(clip(d, 1200))
		b.WriteString("\n")
	}
	if fb := strings.TrimSpace(t.Feedback); fb != "" {
		b.WriteString("\nThis was rejected once. The reviewer said: ")
		b.WriteString(clip(fb, 600))
		b.WriteString("\nAddress that first.\n")
	}
	if note := strings.TrimSpace(t.ReleaseNote); note != "" {
		b.WriteString("\nThe last claimer left off here: ")
		b.WriteString(clip(note, 400))
		b.WriteString("\n")
	}

	b.WriteString("\nRenew the claim with `ht task touch ")
	b.WriteString(ref)
	b.WriteString("` at the start of every turn; a lapsed lease is swept and the task returns to the queue.\n")

	b.WriteString("\nDone when:\n")
	for _, c := range t.Validation {
		b.WriteString("- ")
		b.WriteString(clip(strings.TrimSpace(c.Text), 300))
		if !c.Required {
			b.WriteString(" (optional)")
		}
		b.WriteString("\n")
	}
	// The submit obligation is not optional and is not one criterion among
	// others: it is what turns a finished turn into a reviewable claim.
	b.WriteString("- `ht task submit ")
	b.WriteString(ref)
	b.WriteString(" --report \"<what you did and how you verified it>\" --evidence \"<a command you ran and what it printed>\"` has been run, and its output is shown in this conversation.\n")

	b.WriteString("\nStop instead of pushing on if you are blocked, out of scope, or out of turns: run `ht task release ")
	b.WriteString(ref)
	b.WriteString(" --note \"<what is left>\"` and say why. File anything you found that is not this task as `ht note add \"<the finding>\"`, or as `ht task create \"<the work>\" --discovered-from ")
	b.WriteString(ref)
	b.WriteString("` — do not do it under this task.\n")

	out := b.String()
	if len(out) > GoalLimit {
		// A goal that does not fit is not paste-ready. Trim the criteria block
		// last-in-first-out rather than truncating mid-sentence.
		out = clip(out, GoalLimit-1) + "\n"
	}
	return out
}

// clip cuts at a word boundary and marks the cut, so a trimmed goal reads as
// trimmed rather than as a sentence that trails off.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexAny(cut, " \n"); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \n") + " […]"
}
