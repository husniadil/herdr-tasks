package tui

import (
	"fmt"
	"strings"
)

// Detail is the panel the operator opens on a selection. On the board it is
// the task in full — seq, status, the claim and its lease, the report and the
// evidence a reviewer needs (§14 evidence) — and on the notes view it is the
// note or the parked action with what the gate deferred.
func Detail(m Model, now int64) string {
	var b strings.Builder
	if m.View == ViewBoard {
		t := m.SelectedTask()
		if t == nil {
			return ""
		}
		fmt.Fprintf(&b, "#%d %s\n", t.Seq, t.Title)
		fmt.Fprintf(&b, "status: %s", t.Status)
		if t.Blocked {
			b.WriteString(" (blocked)")
		}
		b.WriteString("\n")
		if t.ClaimedBy != "" {
			fmt.Fprintf(&b, "claim: %s%s\n", t.ClaimedBy, lease(t, now))
		}
		if t.Description != "" {
			fmt.Fprintf(&b, "\n%s\n", t.Description)
		}
		for _, c := range t.Validation {
			fmt.Fprintf(&b, "  · %s\n", c.Text)
		}
		if t.Report != "" {
			fmt.Fprintf(&b, "\nreport: %s\n", t.Report)
		}
		for _, e := range t.Evidence {
			fmt.Fprintf(&b, "  evidence: %s\n", e)
		}
		if t.Feedback != "" {
			fmt.Fprintf(&b, "\nlast feedback: %s\n", t.Feedback)
		}
		return b.String()
	}
	if m.Pane == PaneParked {
		p := m.SelectedParked()
		if p == nil {
			return ""
		}
		fmt.Fprintf(&b, "%s\nsubject: %s\ntarget: %s\npayload: %s\n", p.Verb, p.Subject, p.Target, p.Payload)
		if p.Reason != "" {
			fmt.Fprintf(&b, "gate said: %s\n", p.Reason)
		}
		return b.String()
	}
	n := m.SelectedNote()
	if n == nil {
		return ""
	}
	fmt.Fprintf(&b, "#%d [%s] by %s\n\n%s\n", n.Seq, n.Status, n.Author, n.Body)
	if n.Verdict != "" {
		fmt.Fprintf(&b, "\nverdict: %s", n.Verdict)
		if n.Reason != "" {
			fmt.Fprintf(&b, " — %s", n.Reason)
		}
		b.WriteString("\n")
	}
	if n.Question != "" {
		fmt.Fprintf(&b, "asked: %s\n", n.Question)
	}
	if n.TaskID != "" {
		fmt.Fprintf(&b, "became task %s\n", n.TaskID)
	}
	return b.String()
}
