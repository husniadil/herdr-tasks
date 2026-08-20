package daemon

import (
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// handler answers one verb. The registry below is keyed by the same names
// verbs.All declares, and a startup check asserts the two agree.
type handler func(*Daemon, protocol.Request, tasks.Actor) (any, error)

// handlers is filled in init rather than in a composite literal: one handler
// (parked.resolve) re-runs another verb, which a literal would make a
// self-referential initialisation.
var handlers = map[string]handler{}

func init() {
	handlers = map[string]handler{
		"task.create":  hTaskCreate,
		"task.list":    hTaskList,
		"task.get":     hTaskGet,
		"task.claim":   hTaskClaim,
		"task.touch":   hTaskTouch,
		"task.release": hTaskRelease,
		"task.submit":  hTaskSubmit,
		"task.approve": hTaskApprove,
		"task.reject":  hTaskReject,
		"task.cancel":  hTaskCancel,
		"task.update":  hTaskUpdate,
		"task.archive": hTaskArchive,
		"task.delete":  hTaskDelete,
		"task.goal":    hTaskGoal,

		"note.add":     hNoteAdd,
		"note.list":    hNoteList,
		"note.get":     hNoteGet,
		"note.discuss": hNoteDiscuss,
		"note.verdict": hNoteVerdict,
		"note.promote": hNotePromote,
		"note.keep":    hNoteKeep,
		"note.drop":    hNoteDrop,
		"note.delete":  hNoteDelete,

		"parked.list":    hParkedList,
		"parked.resolve": hParkedResolve,

		"events": hEvents,
		"sweep":  hSweep,
		"doctor": hDoctor,
		"dump":   hDump,
	}
}

// dispatch runs one verb's handler. It is the one place that looks a verb up,
// which is what keeps the gate in front of every call that goes through Handle.
func (d *Daemon) dispatch(name string, req protocol.Request, by tasks.Actor) (any, error) {
	h, ok := handlers[name]
	if !ok {
		return nil, codes.Errorf(codes.Unexpected, "verb %q has no handler", name)
	}
	return h(d, req, by)
}

// HandledVerbs is the set of verbs with a handler, for the registry test.
func HandledVerbs() []string {
	out := make([]string, 0, len(handlers))
	for name := range handlers {
		out = append(out, name)
	}
	return out
}

// TaskResult is the §6.1 result shape of every single-task verb: the task,
// plus the derived facts a caller would otherwise have to compute.
type TaskResult struct {
	Task       *tasks.Task `json:"task"`
	Ready      bool        `json:"ready"`
	Dependents []string    `json:"dependents"`
}

func (d *Daemon) taskResult(t *tasks.Task) (any, error) {
	deps, err := d.Store.Dependents(t.ID)
	if err != nil {
		return nil, err
	}
	return TaskResult{Task: t, Ready: tasks.Ready(t), Dependents: deps}, nil
}

func hTaskCreate(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	in := tasks.NewTaskInput{
		Project:        req.Project,
		Title:          argString(req.Args, "title"),
		Description:    argString(req.Args, "description"),
		Priority:       argInt(req.Args, "priority"),
		Validation:     criteria(argStrings(req.Args, "validation")),
		Deps:           argStrings(req.Args, "depends-on"),
		DiscoveredFrom: argString(req.Args, "discovered-from"),
		PaneID:         req.PaneID,
		TabID:          req.TabID,
		WorkspaceID:    req.WorkspaceID,
	}
	if in.DiscoveredFrom != "" {
		origin, err := d.Store.GetTask(req.Project, in.DiscoveredFrom)
		if err != nil {
			return nil, err
		}
		in.DiscoveredFrom = origin.ID
	}
	for i, ref := range in.Deps {
		dep, err := d.Store.GetTask(req.Project, ref)
		if err != nil {
			return nil, err
		}
		in.Deps[i] = dep.ID
	}
	t, err := d.Store.CreateTask(in, by, d.Now())
	if err != nil {
		return nil, err
	}
	d.emitted(t.Project, "task", t.ID)
	return d.taskResult(t)
}

// TaskListResult is what `task list` answers with.
type TaskListResult struct {
	Tasks []*tasks.Task `json:"tasks"`
	Count int           `json:"count"`
}

func hTaskList(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	f := store.TaskFilter{
		Project:     req.Project,
		AllProjects: req.AllProjects,
		Status:      argString(req.Args, "status"),
		Ready:       argBool(req.Args, "ready"),
		Query:       argString(req.Args, "query"),
		Archived:    argBool(req.Args, "archived"),
		Limit:       int(argInt(req.Args, "limit")),
	}
	if argBool(req.Args, "mine") {
		f.Mine = by.Principal
	}
	list, err := d.Store.ListTasks(f)
	if err != nil {
		return nil, err
	}
	return TaskListResult{Tasks: list, Count: len(list)}, nil
}

func hTaskGet(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	t, err := d.Store.GetTask(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	return d.taskResult(t)
}

func (d *Daemon) transition(req protocol.Request, fn func(*tasks.Task) (tasks.Event, error)) (any, error) {
	t, err := d.Store.TaskTransition(req.Project, argString(req.Args, "id"), req.BaseUpdatedAt, fn)
	if err != nil {
		return nil, err
	}
	d.emitted(t.Project, "task", t.ID)
	return d.taskResult(t)
}

func hTaskClaim(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Claim(t, by, d.Now(), d.Config.LeaseMS())
	})
}

func hTaskTouch(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Touch(t, by, d.Now(), d.Config.LeaseMS())
	})
}

func hTaskRelease(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Release(t, by, argString(req.Args, "note"), d.Now(), tasks.KindReleased)
	})
}

func hTaskSubmit(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Submit(t, by, argString(req.Args, "report"), argStrings(req.Args, "evidence"), d.Now())
	})
}

func hTaskApprove(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Approve(t, by, d.Now())
	})
}

func hTaskReject(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Reject(t, by, argString(req.Args, "feedback"), d.Now())
	})
}

func hTaskCancel(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Cancel(t, by, argString(req.Args, "reason"), d.Now())
	})
}

func hTaskArchive(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Archive(t, by, d.Now())
	})
}

func hTaskUpdate(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	patch := tasks.UpdatePatch{}
	if has(req.Args, "title") {
		v := argString(req.Args, "title")
		patch.Title = &v
	}
	if has(req.Args, "description") {
		v := argString(req.Args, "description")
		patch.Description = &v
	}
	if has(req.Args, "priority") {
		v := argInt(req.Args, "priority")
		patch.Priority = &v
	}
	if has(req.Args, "validation") {
		v := criteria(argStrings(req.Args, "validation"))
		patch.Validation = &v
	}
	if has(req.Args, "depends-on") {
		refs := argStrings(req.Args, "depends-on")
		resolved := make([]string, 0, len(refs))
		for _, ref := range refs {
			dep, err := d.Store.GetTask(req.Project, ref)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, dep.ID)
		}
		patch.Deps = &resolved
	}
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Update(t, by, patch, d.Now())
	})
}

// DeleteResult is what a hard delete answers with (§5.7).
type DeleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func hTaskDelete(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	t, err := d.Store.GetTask(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	if err := d.Store.DeleteTask(req.Project, t.ID); err != nil {
		return nil, err
	}
	return DeleteResult{ID: t.ID, Deleted: true}, nil
}

// GoalResult carries the paste-ready /goal condition (§16.2).
type GoalResult struct {
	TaskID string `json:"task_id"`
	Seq    int64  `json:"seq"`
	Goal   string `json:"goal"`
	Length int    `json:"length"`
}

func hTaskGoal(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	t, err := d.Store.GetTask(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	text := BuildGoal(t)
	return GoalResult{TaskID: t.ID, Seq: t.Seq, Goal: text, Length: len(text)}, nil
}

// NoteResult is the result shape of every single-note verb.
type NoteResult struct {
	Note *tasks.Note `json:"note"`
}

func hNoteAdd(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	n, err := d.Store.CreateNote(tasks.NewNoteInput{
		Project: req.Project,
		Body:    argString(req.Args, "body"),
		PaneID:  req.PaneID,
	}, by, d.Now())
	if err != nil {
		return nil, err
	}
	d.emitted(n.Project, "note", n.ID)
	return NoteResult{Note: n}, nil
}

// NoteListResult is what `note list` answers with.
type NoteListResult struct {
	Notes []*tasks.Note `json:"notes"`
	Count int           `json:"count"`
}

func hNoteList(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	list, err := d.Store.ListNotes(store.NoteFilter{
		Project:     req.Project,
		AllProjects: req.AllProjects,
		Status:      argString(req.Args, "status"),
		Limit:       int(argInt(req.Args, "limit")),
	})
	if err != nil {
		return nil, err
	}
	return NoteListResult{Notes: list, Count: len(list)}, nil
}

func hNoteGet(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	n, err := d.Store.GetNote(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	return NoteResult{Note: n}, nil
}

func (d *Daemon) noteTransition(req protocol.Request, fn func(*tasks.Note) (tasks.Event, error)) (any, error) {
	n, err := d.Store.NoteTransition(req.Project, argString(req.Args, "id"), req.BaseUpdatedAt, fn)
	if err != nil {
		return nil, err
	}
	d.emitted(n.Project, "note", n.ID)
	return NoteResult{Note: n}, nil
}

func hNoteDiscuss(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	question := argString(req.Args, "question")
	return d.noteTransition(req, func(n *tasks.Note) (tasks.Event, error) {
		ev, err := tasks.NoteDiscuss(n, by, d.Now())
		if err != nil || question == "" {
			return ev, err
		}
		return tasks.NoteAskInput(n, by, question, d.Now())
	})
}

func hNoteVerdict(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.noteTransition(req, func(n *tasks.Note) (tasks.Event, error) {
		return tasks.NoteVerdict(n, by, tasks.Verdict(argString(req.Args, "verdict")),
			argString(req.Args, "reason"), d.Now())
	})
}

// PromoteResult is a promotion: the note and the task it became.
type PromoteResult struct {
	Note *tasks.Note `json:"note"`
	Task *tasks.Task `json:"task"`
}

func hNotePromote(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	if !by.IsHuman() {
		return nil, codes.New(codes.Forbidden, "only the operator promotes a note")
	}
	n, err := d.Store.GetNote(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	title := argString(req.Args, "title")
	if title == "" {
		title = firstLine(n.Body)
	}
	t, err := d.Store.CreateTask(tasks.NewTaskInput{
		Project:     n.Project,
		Title:       title,
		Description: n.Body,
		PaneID:      req.PaneID,
	}, by, d.Now())
	if err != nil {
		return nil, err
	}
	updated, err := d.Store.NoteTransition(req.Project, n.ID, req.BaseUpdatedAt, func(x *tasks.Note) (tasks.Event, error) {
		return tasks.NotePromote(x, by, t.ID, d.Now())
	})
	if err != nil {
		// The task exists but the note did not move: cancel the task rather
		// than leave an orphan claiming to be a promotion.
		d.Store.TaskTransition(t.Project, t.ID, 0, func(x *tasks.Task) (tasks.Event, error) {
			return tasks.Cancel(x, by, "promotion did not complete", d.Now())
		})
		return nil, err
	}
	d.emitted(n.Project, "note", n.ID)
	return PromoteResult{Note: updated, Task: t}, nil
}

func hNoteKeep(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.noteTransition(req, func(n *tasks.Note) (tasks.Event, error) {
		return tasks.NoteKeep(n, by, argString(req.Args, "reason"), d.Now())
	})
}

func hNoteDrop(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.noteTransition(req, func(n *tasks.Note) (tasks.Event, error) {
		return tasks.NoteDrop(n, by, argString(req.Args, "reason"), d.Now())
	})
}

func hNoteDelete(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	n, err := d.Store.GetNote(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	if err := d.Store.DeleteNote(req.Project, n.ID); err != nil {
		return nil, err
	}
	return DeleteResult{ID: n.ID, Deleted: true}, nil
}

// ParkedListResult is the queue of deferred actions (§9.3).
type ParkedListResult struct {
	Parked []store.Parked `json:"parked"`
	Count  int            `json:"count"`
}

func hParkedList(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	list, err := d.Store.ListParked(req.Project)
	if err != nil {
		return nil, err
	}
	return ParkedListResult{Parked: list, Count: len(list)}, nil
}

// ParkedResolveResult says what became of a parked action.
type ParkedResolveResult struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Result any    `json:"result,omitempty"`
}

func hParkedResolve(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	if !by.IsHuman() {
		return nil, codes.New(codes.Forbidden, "only the operator resolves a parked action (§9.3)")
	}
	p, err := d.Store.GetParked(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	if argBool(req.Args, "reject") {
		if err := d.Store.ResolveParked(req.Project, p.ID, "rejected", d.Now()); err != nil {
			return nil, err
		}
		return ParkedResolveResult{ID: p.ID, State: "rejected"}, nil
	}
	// §9.3: resolving re-runs the verb under the ORIGINAL subject, never the
	// resolver's. The re-run skips the gate, because the operator resolving it
	// is the decision the gate deferred to.
	verb, ok := verbFromGated(p.Verb)
	if !ok {
		return nil, codes.Errorf(codes.Usage, "parked verb %q is not a verb of this plugin", p.Verb)
	}
	rerun := protocol.Request{
		Verb:    verb.Name,
		Project: p.Project,
		Args:    map[string]any{},
	}
	if err := decodeArgs(p.Payload, &rerun.Args); err != nil {
		return nil, err
	}
	if pane, ok := strings.CutPrefix(p.Subject, "agent:"); ok {
		rerun.PaneID = pane
	}
	actor, err := d.actor(rerun)
	if err != nil {
		return nil, err
	}
	out, err := d.dispatch(verb.Name, rerun, actor)
	if err != nil {
		return nil, err
	}
	if err := d.Store.ResolveParked(req.Project, p.ID, "resolved", d.Now()); err != nil {
		return nil, err
	}
	return ParkedResolveResult{ID: p.ID, State: "resolved", Result: out}, nil
}

// EventsResult is one page of the trail (§8.2).
type EventsResult struct {
	Events []store.Event `json:"events"`
	Count  int           `json:"count"`
}

func hEvents(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	f := store.EventFilter{
		Project:     req.Project,
		AllProjects: req.AllProjects,
		Entity:      argString(req.Args, "entity"),
		Limit:       int(argInt(req.Args, "limit")),
	}
	if since := argString(req.Args, "since"); since != "" {
		if ms, ok := parseMS(since); ok {
			f.SinceAt = ms
		} else {
			f.SinceID = since
		}
	}
	list, err := d.Store.Events(f)
	if err != nil {
		return nil, err
	}
	return EventsResult{Events: list, Count: len(list)}, nil
}

// SweepResult says which leases came back (§11.5).
type SweepResult struct {
	Released []string `json:"released"`
	Count    int      `json:"count"`
}

func hSweep(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	if pane := argString(req.Args, "pane"); pane != "" {
		released, err := d.Store.ReleaseByPane(pane, d.Now())
		if err != nil {
			return nil, err
		}
		if len(released) > 0 {
			d.notify()
		}
		return SweepResult{Released: released, Count: len(released)}, nil
	}
	released := d.Sweep()
	return SweepResult{Released: released, Count: len(released)}, nil
}

func hDump(d *Daemon, _ protocol.Request, _ tasks.Actor) (any, error) {
	return d.Store.Dump()
}

func hDoctor(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.Doctor(req, by), nil
}

func criteria(list []string) []tasks.Criterion {
	if len(list) == 0 {
		return nil
	}
	out := make([]tasks.Criterion, 0, len(list))
	for _, s := range list {
		text := strings.TrimSpace(s)
		if text == "" {
			continue
		}
		// A criterion is required unless it is marked optional, because §16.1
		// wants criteria that an evaluator checks, not ones it may skip.
		required := true
		if rest, ok := strings.CutSuffix(text, " (optional)"); ok {
			text, required = rest, false
		}
		out = append(out, tasks.Criterion{Text: text, Required: required})
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 120 {
		line = strings.TrimSpace(line[:120])
	}
	return line
}

// verbFromGated maps a §9.4 gate verb name back to the registry entry.
func verbFromGated(gated string) (verbs.Verb, bool) {
	for _, v := range verbs.All {
		if v.Gated == gated {
			return v, true
		}
	}
	return verbs.Verb{}, false
}
