package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/project"
	"github.com/husniadil/herdr-tasks/internal/tasks"
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
			b.WriteString(" (blocked")
			// Which dependency will never be done, so the operator reading
			// the board knows this one needs a decision, not patience.
			if len(t.Abandoned) > 0 {
				fmt.Fprintf(&b, " by cancelled %s", seqList(t.Abandoned))
			}
			b.WriteString(")")
		}
		b.WriteString("\n")
		if t.ClaimedBy != "" {
			fmt.Fprintf(&b, "claim: %s%s\n", t.ClaimedBy, lease(t, now))
		}
		if t.Description != "" {
			fmt.Fprintf(&b, "\n%s\n", t.Description)
		}
		checklist(&b, t)
		if t.Report != "" {
			fmt.Fprintf(&b, "\nreport: %s\n", t.Report)
		}
		for _, e := range t.Evidence {
			fmt.Fprintf(&b, "  evidence: %s\n", e)
		}
		// The operator approves from this pane, so the marker belongs here as
		// much as in the prose door: the report above is not the one that was
		// submitted, and nothing else on the card would say so.
		if t.AmendCount > 0 {
			fmt.Fprintf(&b, "amended: the report above replaced the submitted one (%s)\n", amendments(t.AmendCount))
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
		if n.TaskProject != "" {
			fmt.Fprintf(&b, "became task %s on %s\n", n.TaskID, project.DisplayName(n.TaskProject))
		} else {
			fmt.Fprintf(&b, "became task %s\n", n.TaskID)
		}
	}
	return b.String()
}

// seqList renders task numbers the way an operator types them.
func seqList(seqs []int64) string {
	out := make([]string, 0, len(seqs))
	for _, n := range seqs {
		out = append(out, "#"+strconv.FormatInt(n, 10))
	}
	return strings.Join(out, ", ")
}

// checklist renders validation as the DERIVED coverage the reviewer came for
// (§16.1): a box is checked because a submitted evidence entry cites that
// criterion, never because anyone flipped it, and the citing lines sit under
// the criterion they prove. The (optional) marker comes along because
// Criterion.Required decides whether an empty box is a gap or a choice.
func checklist(b *strings.Builder, t *tasks.Task) {
	for i, c := range t.Validation {
		box := " "
		for _, e := range t.EvidenceFor {
			if e.Criterion == i+1 {
				box = "x"
				break
			}
		}
		opt := ""
		if !c.Required {
			opt = " (optional)"
		}
		fmt.Fprintf(b, "  [%s] %d. %s%s\n", box, i+1, c.Text, opt)
		for _, e := range t.EvidenceFor {
			if e.Criterion == i+1 {
				fmt.Fprintf(b, "    %s\n", e.Text)
			}
		}
	}
	// A criteria list edited after a submission can leave a citation pointing
	// at nothing. Dropping it silently would let a reviewer read full coverage
	// off a checklist that quietly lost an item.
	for _, e := range t.EvidenceFor {
		if e.Criterion < 1 || e.Criterion > len(t.Validation) {
			fmt.Fprintf(b, "  [!] %d. cites a criterion this task no longer has\n    %s\n",
				e.Criterion, e.Text)
		}
	}
}

// amendments counts corrections in words a reader does not have to parse.
func amendments(n int64) string {
	if n == 1 {
		return "1 amendment"
	}
	return fmt.Sprintf("%d amendments", n)
}
