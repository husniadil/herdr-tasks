package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// renderHuman prints for a person reading a terminal. §6.2: this output is not
// for parsing, which is what --json is for. Both audiences are first-class, so
// neither rendering is a rendering of the other.
func renderHuman(v verbs.Verb, raw json.RawMessage) error {
	switch v.Name {
	case "task.list":
		var res daemon.TaskListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if res.Count == 0 {
			fmt.Println("No tasks match.")
			return nil
		}
		for _, t := range res.Tasks {
			fmt.Println(taskLine(t))
		}
		return nil
	case "note.list":
		var res daemon.NoteListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		if res.Count == 0 {
			fmt.Println("No notes match.")
			return nil
		}
		for _, n := range res.Notes {
			fmt.Printf("#%d  %-11s %s\n", n.Seq, n.Status, firstLine(n.Body))
		}
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
		}
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
			printTask(one.Task)
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

func taskLine(t *tasks.Task) string {
	mark := " "
	switch {
	case t.Blocked:
		mark = "-"
	case t.ClaimedBy != "":
		mark = "*"
	}
	line := fmt.Sprintf("%s #%-4d %-9s %s", mark, t.Seq, t.Status, t.Title)
	if t.ClaimedBy != "" {
		line += fmt.Sprintf("  (held by %s)", t.ClaimedBy)
	}
	return line
}

func printTask(t *tasks.Task) {
	fmt.Printf("#%d  %s\n", t.Seq, t.Title)
	fmt.Printf("     %s", t.Status)
	if t.Blocked {
		fmt.Print(", blocked")
	}
	if t.ClaimedBy != "" {
		fmt.Printf(", held by %s", t.ClaimedBy)
		if t.ClaimedByHarness != "" {
			fmt.Printf(" (%s)", t.ClaimedByHarness)
		}
		if t.LeaseUntil > 0 {
			fmt.Printf(", lease until %s", stamp(t.LeaseUntil))
		}
	}
	fmt.Printf("\n     id %s\n", t.ID)
	if t.Description != "" {
		fmt.Printf("\n%s\n", t.Description)
	}
	if len(t.Validation) > 0 {
		fmt.Println("\nDone when:")
		for _, c := range t.Validation {
			suffix := ""
			if !c.Required {
				suffix = " (optional)"
			}
			fmt.Printf("  - %s%s\n", c.Text, suffix)
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
		fmt.Printf("\nPromoted to task %s\n", n.TaskID)
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
	fmt.Printf("ht %s (%s), shared plugin contract %s\n", r.Version, r.Plugin, r.Contract)
	fmt.Printf("state dir     %s\n", r.StateDir)
	fmt.Printf("config        %s (%s)\n", r.ConfigFile, present(r.ConfigPresent))
	fmt.Printf("socket        %s (%s)\n", r.SocketPath, live(r.SocketLive))
	fmt.Printf("build         %s, surface %s\n", r.Build.Short(), r.Fingerprint)
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
	if len(line) > 80 {
		line = line[:80] + "…"
	}
	return line
}
