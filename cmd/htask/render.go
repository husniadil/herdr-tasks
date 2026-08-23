package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/project"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// renderHuman prints for a person reading a terminal. §6.2: this output is not
// for parsing, which is what --json is for. Both audiences are first-class, so
// neither rendering is a rendering of the other.
func renderHuman(v verbs.Verb, raw json.RawMessage, now int64) error {
	switch v.Name {
	case "task.list":
		var res daemon.TaskListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if res.Count == 0 {
			fmt.Println(emptyLine("tasks", res.Project, res.Elsewhere))
			return nil
		}
		for _, t := range res.Tasks {
			fmt.Println(taskLine(t, now))
		}
		return nil
	case "note.list":
		var res daemon.NoteListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if res.Count == 0 {
			fmt.Println(emptyLine("notes", res.Project, res.Elsewhere))
			return nil
		}
		for _, n := range res.Notes {
			fmt.Printf("#%d  %-11s %s\n", n.Seq, n.Status, firstLine(n.Body))
		}
		return nil
	case "note.fold":
		// A fold answers with the note AND the task, and the note is what the
		// operator was deciding about: the task was already there, and printing
		// it instead would answer a question nobody asked.
		var res daemon.FoldResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		printNote(res.Note)
		return nil
	case "task.goal":
		var res daemon.GoalResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		fmt.Print(res.Goal)
		return nil
	case "events":
		var res daemon.EventsResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		for _, e := range res.Events {
			printEvent(e)
		}
		return nil
	case "doctor":
		var r daemon.DoctorReport
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		printDoctor(r)
		return nil
	case "parked.list":
		var res daemon.ParkedListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if res.Count == 0 {
			fmt.Println("Nothing is parked.")
			return nil
		}
		for _, p := range res.Parked {
			fmt.Printf("%s  %s by %s on %s\n", p.ID, p.Verb, p.Subject, p.Target)
			// A row that is not waiting is one the operator already decided
			// and whose verb then refused. Saying so is the difference between
			// a queue and a list of things that may or may not have happened.
			if p.State != "parked" {
				fmt.Printf("            %s: %s\n", p.State, p.Error)
			}
		}
		return nil
	case "stop":
		var res daemon.StopResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		fmt.Printf("the daemon on %s is stopping (pid %d)\n", res.Socket, res.PID)
		return nil
	case "dump":
		out, err := json.MarshalIndent(json.RawMessage(raw), "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	// The single-entity verbs share one rendering: what the thing is now.
	var one struct {
		Task *tasks.Task `json:"task"`
		Note *tasks.Note `json:"note"`
		ID   string      `json:"id"`
	}
	if err := json.Unmarshal(raw, &one); err == nil {
		switch {
		case one.Task != nil:
			printTask(one.Task, now)
			return nil
		case one.Note != nil:
			printNote(one.Note)
			return nil
		case one.ID != "":
			fmt.Println(one.ID)
			return nil
		}
	}
	out, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
	fmt.Println(string(out))
	return nil
}

func taskLine(t *tasks.Task, now int64) string {
	mark := " "
	switch {
	case t.Blocked:
		mark = "-"
	case t.ClaimedBy != "":
		mark = "*"
	}
	line := fmt.Sprintf("%s #%-4d %-9s %s", mark, t.Seq, t.Status, t.Title)
	if t.ClaimedBy != "" {
		line += fmt.Sprintf("  (held by %s%s)", t.ClaimedBy, reviewWait(t, now))
	}
	return line
}

func printTask(t *tasks.Task, now int64) {
	fmt.Printf("#%d  %s\n", t.Seq, t.Title)
	fmt.Printf("     %s", t.Status)
	if t.Blocked {
		fmt.Print(", blocked")
		// A cancelled dependency is never going to be done, so its dependent
		// is blocked until the edge is edited. Saying which one turns a wall
		// back into a decision.
		if len(t.Abandoned) > 0 {
			fmt.Printf(" by cancelled %s", seqList(t.Abandoned))
		}
	}
	if t.ClaimedBy != "" {
		fmt.Printf(", held by %s", t.ClaimedBy)
		if t.ClaimedByHarness != "" {
			fmt.Printf(" (%s)", t.ClaimedByHarness)
		}
		if t.LeaseUntil > 0 {
			fmt.Printf(", lease until %s", stamp(t.LeaseUntil))
		}
		fmt.Print(reviewWait(t, now))
	}
	fmt.Printf("\n     id %s\n", t.ID)
	// A task read across boards is only useful with the board named, and a
	// task read on your own board loses nothing by saying which one it is.
	fmt.Printf("     in %s\n", t.Project)
	if t.Description != "" {
		fmt.Printf("\n%s\n", t.Description)
	}
	if len(t.Validation) > 0 {
		fmt.Println("\nDone when:")
		// The box is derived from the citations a submission carried, never
		// stored and never flipped by hand (§16.1). The human door shows the
		// same coverage the TUI does, because §6.1 has two audiences.
		for i, c := range t.Validation {
			box := " "
			for _, e := range t.EvidenceFor {
				if e.Criterion == i+1 {
					box = "x"
					break
				}
			}
			suffix := ""
			if !c.Required {
				suffix = " (optional)"
			}
			fmt.Printf("  [%s] %d. %s%s\n", box, i+1, c.Text, suffix)
			for _, e := range t.EvidenceFor {
				if e.Criterion == i+1 {
					fmt.Printf("        %s\n", e.Text)
				}
			}
		}
		for _, e := range t.EvidenceFor {
			if e.Criterion < 1 || e.Criterion > len(t.Validation) {
				fmt.Printf("  [!] %d. cites a criterion this task no longer has\n        %s\n",
					e.Criterion, e.Text)
			}
		}
	}
	if len(t.Deps) > 0 {
		fmt.Printf("\nDepends on: %s\n", strings.Join(t.Deps, ", "))
	}
	if t.ReleaseNote != "" {
		fmt.Printf("\nLast release note: %s\n", t.ReleaseNote)
	}
	if t.Report != "" {
		fmt.Printf("\nReport: %s\n", t.Report)
	}
	for _, e := range t.Evidence {
		fmt.Printf("Evidence: %s\n", e)
	}
	// A reviewer reads this line before deciding, which is the whole reason
	// the marker is on the row rather than only in the trail: the report above
	// is not the one that was submitted, and nothing else here would say so.
	if t.AmendCount > 0 {
		fmt.Printf("Amended: the report above replaced the submitted one (%s)\n", amendments(t.AmendCount))
	}
	if t.Feedback != "" {
		fmt.Printf("\nFeedback: %s\n", t.Feedback)
	}
}

func printNote(n *tasks.Note) {
	fmt.Printf("#%d  %s\n", n.Seq, n.Status)
	fmt.Printf("     id %s, by %s\n\n%s\n", n.ID, n.Author, n.Body)
	if n.Question != "" {
		fmt.Printf("\nWaiting on you: %s\n", n.Question)
	}
	if n.Verdict != "" {
		fmt.Printf("\nProposed verdict: %s", n.Verdict)
		if n.Reason != "" {
			fmt.Printf(" — %s", n.Reason)
		}
		fmt.Println()
	}
	if n.TaskID != "" {
		// A folded note did not become this task on its own, and a board that
		// said "promoted" for both would lose the only difference an operator
		// needs: which of them the task was actually made from.
		how := "Promoted to"
		if n.Folded {
			how = "Folded into"
		}
		if n.TaskProject != "" {
			fmt.Printf("\n%s task %s on %s (%s)\n", how, n.TaskID, project.DisplayName(n.TaskProject), n.TaskProject)
		} else {
			fmt.Printf("\n%s task %s\n", how, n.TaskID)
		}
	}
}

func renderEvent(raw json.RawMessage) error {
	var e store.Event
	if err := json.Unmarshal(raw, &e); err != nil {
		return err
	}
	printEvent(e)
	return nil
}

func printEvent(e store.Event) {
	fmt.Printf("%s  %-24s %s  %s\n", stamp(e.At), e.Name, e.EntityID, e.Actor)
}

func printDoctor(r daemon.DoctorReport) {
	fmt.Printf("htask %s (%s), shared plugin contract %s\n", r.Version, r.Plugin, r.Contract)
	fmt.Printf("state dir     %s\n", r.StateDir)
	fmt.Printf("config        %s (%s)\n", r.ConfigFile, present(r.ConfigPresent))
	fmt.Printf("socket        %s (%s)\n", r.SocketPath, live(r.SocketLive))
	fmt.Printf("daemon lock   %s\n", r.LockPath)
	fmt.Printf("build         %s, surface %s\n", r.Build.Short(), r.Fingerprint)
	// Beside the daemon's own, because the pair is the answer: a door older
	// than the binary at its path is still serving that older binary's
	// instructions to every session it holds.
	if r.Door.Short() != "" {
		stale := ""
		if r.DoorSuperseded {
			stale = " (superseded — see degraded)"
		}
		fmt.Printf("door          %s%s\n", r.Door.Short(), stale)
	}
	fmt.Printf("schema        version %d\n", r.SchemaVersion)
	fmt.Printf("project       %s\n", r.Project)
	fmt.Printf("principal     %s", r.Principal)
	if r.Harness != "" {
		fmt.Printf(" (harness %s)", r.Harness)
	}
	fmt.Println()
	fmt.Printf("herdr         %s (%s)\n", r.HerdrBin, live(r.HerdrReachable))
	if r.HerdrReachable {
		// The protocol number is shown, never decided on (§11.2).
		fmt.Printf("herdr schema  protocol %d, %d requests, %d event kinds\n",
			r.HerdrProtocol, len(r.HerdrRequests), len(r.HerdrEvents))
	}
	fmt.Printf("lease         %ds, swept every %ds\n", r.LeaseSeconds, r.SweepSeconds)
	if r.GateConfigured {
		fmt.Printf("policy gate   %s\n", strings.Join(r.GateCommand, " "))
	} else {
		fmt.Println("policy gate   unconfigured, so every gated verb is allowed")
	}
	if r.HookConfigured {
		fmt.Printf("event hook    %s\n", strings.Join(r.HookCommand, " "))
	} else {
		fmt.Println("event hook    unconfigured")
	}
	fmt.Printf("gated verbs   %s\n", strings.Join(r.GatedVerbs, ", "))
	fmt.Printf("mcp tools     %s\n", strings.Join(r.MCPTools, ", "))
	fmt.Printf("backlog       %d tasks here, %d held, %d parked\n", r.TasksInProject, r.LeasesOutstands, r.ParkedWaiting)
	fmt.Printf("trust         %s\n", r.TrustBoundary)
	if len(r.Degraded) == 0 {
		fmt.Println("degraded      nothing")
		return
	}
	fmt.Println("degraded:")
	for _, line := range r.Degraded {
		fmt.Println("  - " + line)
	}
}

func present(b bool) string {
	if b {
		return "present"
	}
	return "not written yet, using defaults"
}

func live(b bool) string {
	if b {
		return "live"
	}
	return "not reachable"
}

// reviewWait is how long a task has been waiting for a reviewer, said in the
// same clause as who holds it. Only a review row has it: a doing row already
// prints a time — its lease — and a row that came back from review is working
// again, not waiting, however old its last submission is.
func reviewWait(t *tasks.Task, now int64) string {
	if t.Status != tasks.StatusReview || t.SubmittedAt == 0 {
		return ""
	}
	return ", submitted " + waited(t.SubmittedAt, now)
}

// waited says a duration in the largest unit that still has a whole number in
// it, which is the precision a wait is read at: seconds while it is fresh,
// days once nobody has looked. It is derived here and stored nowhere, so it
// is right whenever it is read. A submission stamped in the future is a clock
// disagreeing with itself, and the smallest true thing to say about it is
// that no time has passed.
func waited(submitted, now int64) string {
	d := time.Duration(now-submitted) * time.Millisecond
	switch {
	case d < time.Minute:
		if d < 0 {
			d = 0
		}
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours())/24)
}

// stamp renders a Unix-millisecond timestamp as ISO at the presentation edge,
// which is the only place §5.3 allows it.
func stamp(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.UnixMilli(ms).Format(time.RFC3339)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	// Runes, not bytes: a byte offset can land inside a multi-byte character
	// and leave half of one behind, which renders as a replacement character
	// and is not what anyone wrote.
	if r := []rune(line); len(r) > 80 {
		line = string(r[:80]) + "…"
	}
	return line
}

// emptyLine is what an empty list says (§4.2). It names the project it is
// empty FOR, because a board scoped to a project the caller was not thinking
// of looks exactly like a board with nothing on it — and when the same filter
// matches somewhere else, it says so and hands over the command that shows
// it. The hint appears only when it is true: pointing at nothing is worse
// than saying nothing.
func emptyLine(what, project string, elsewhere int) string {
	line := fmt.Sprintf("No %s match in %s.", what, project)
	if project == "" {
		line = fmt.Sprintf("No %s match.", what)
	}
	if elsewhere > 0 {
		line += fmt.Sprintf(" %d in other projects: htask %s list --all-projects",
			elsewhere, strings.TrimSuffix(what, "s"))
	}
	return line
}

// seqList renders task numbers the way an operator types them.
func seqList(seqs []int64) string {
	out := make([]string, 0, len(seqs))
	for _, n := range seqs {
		out = append(out, "#"+strconv.FormatInt(n, 10))
	}
	return strings.Join(out, ", ")
}

// amendments counts corrections in words a reader does not have to parse.
func amendments(n int64) string {
	if n == 1 {
		return "1 amendment"
	}
	return fmt.Sprintf("%d amendments", n)
}
