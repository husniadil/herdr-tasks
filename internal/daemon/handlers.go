package daemon

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/project"
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
		"task.amend":   hTaskAmend,
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
		"note.update":  hNoteUpdate,
		"note.discuss": hNoteDiscuss,
		"note.verdict": hNoteVerdict,
		"note.promote": hNotePromote,
		"note.fold":    hNoteFold,
		"note.unfold":  hNoteUnfold,
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

// TaskListResult is what `task list` answers with. Project and Elsewhere are
// §4.2 made visible: an empty answer that does not say which project it is
// empty FOR is indistinguishable from a board scoped to the wrong one, which
// has twice been read as data loss. Elsewhere counts what the SAME filter
// matches outside this project, so it is never a promise the suggested
// command cannot keep, and it is only computed when there is nothing to show.
type TaskListResult struct {
	Tasks     []*tasks.Task `json:"tasks"`
	Count     int           `json:"count"`
	Project   string        `json:"project,omitempty"`
	Elsewhere int           `json:"elsewhere,omitempty"`
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
	// §4.4: an --all-projects answer was not scoped to a project, so it does
	// not claim one. Naming the project that merely happened to resolve would
	// describe a scope the caller did not ask for.
	res := TaskListResult{Tasks: list, Count: len(list)}
	if !req.AllProjects {
		res.Project = req.Project
	}
	if len(list) == 0 && !req.AllProjects {
		other := f
		other.Elsewhere, other.Limit = true, 0
		rest, err := d.Store.ListTasks(other)
		if err != nil {
			return nil, err
		}
		res.Elsewhere = len(rest)
	}
	return res, nil
}

func hTaskGet(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	ref := argString(req.Args, "id")
	get := func() (*tasks.Task, error) { return d.Store.GetTask(req.Project, ref) }
	if req.AllProjects {
		// §4.4: the caller asked past their own board, so the id is resolved
		// against every project. The task carries the project it was found in,
		// which is the only place that answer can come from.
		get = func() (*tasks.Task, error) { return d.Store.GetTaskAnyProject(ref) }
	}
	t, err := get()
	if err != nil {
		return nil, err
	}
	return d.taskResult(t)
}

func (d *Daemon) transition(req protocol.Request, fn func(*tasks.Task) (tasks.Event, error)) (any, error) {
	project, ref := req.Project, argString(req.Args, "id")
	if req.AllProjects {
		// §4.4: the caller asked past their own board, so the id is resolved
		// against every project first and the move happens on the board the
		// task was actually filed on. Only a ULID addresses a task across
		// boards — GetTaskAnyProject refuses a number with USAGE, the same
		// answer task.get gives.
		found, ferr := d.Store.GetTaskAnyProject(ref)
		if ferr != nil {
			return nil, ferr
		}
		project, ref = found.Project, found.ID
	}
	t, err := d.Store.TaskTransition(project, ref, req.BaseUpdatedAt, fn)
	if err != nil {
		return nil, err
	}
	d.emitted(t.Project, "task", t.ID)
	return d.taskResult(t)
}

func hTaskClaim(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Claim(t, by, d.Now(), d.Cfg().LeaseMS())
	})
}

func hTaskTouch(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Touch(t, by, d.Now(), d.Cfg().LeaseMS())
	})
}

func hTaskRelease(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Release(t, by, argString(req.Args, "note"), d.Now(), tasks.KindReleased)
	})
}

func hTaskSubmit(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Submit(t, by, argString(req.Args, "report"), argStrings(req.Args, "evidence"), argStrings(req.Args, "evidence-for"), d.Now())
	})
}

// Amending is Submit's missing other half: the same arguments, the same
// citation rules, and none of the submission's own facts. hTaskSubmit and this
// differ by exactly the state-machine call, which is the point — a caller that
// can write a submit can write the correction to it.
func hTaskAmend(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	// The `has` pattern hTaskUpdate uses, for the same reason: absent and
	// empty are different answers, and only one of them means "clear it".
	var evidence, evidenceFor *[]string
	if has(req.Args, "evidence") {
		v := argStrings(req.Args, "evidence")
		evidence = &v
	}
	if has(req.Args, "evidence-for") {
		v := argStrings(req.Args, "evidence-for")
		evidenceFor = &v
	}
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Amend(t, by, argString(req.Args, "report"), evidence, evidenceFor, d.Now())
	})
}

func hTaskApprove(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Approve(t, by, d.Now())
	})
}

func hTaskReject(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.transition(req, func(t *tasks.Task) (tasks.Event, error) {
		return tasks.Reject(t, by, argString(req.Args, "feedback"), d.Now(), d.Cfg().LeaseMS())
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
	if argBool(req.Args, "one-line") {
		text = BuildGoalOneLine(t)
	}
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

// NoteListResult is what `note list` answers with, carrying the same §4.2
// scope facts as TaskListResult and for the same reason.
type NoteListResult struct {
	Notes     []*tasks.Note `json:"notes"`
	Count     int           `json:"count"`
	Project   string        `json:"project,omitempty"`
	Elsewhere int           `json:"elsewhere,omitempty"`
}

func hNoteList(d *Daemon, req protocol.Request, _ tasks.Actor) (any, error) {
	f := store.NoteFilter{
		Project:     req.Project,
		AllProjects: req.AllProjects,
		Status:      argString(req.Args, "status"),
		Query:       argString(req.Args, "query"),
		Limit:       int(argInt(req.Args, "limit")),
	}
	list, err := d.Store.ListNotes(f)
	if err != nil {
		return nil, err
	}
	res := NoteListResult{Notes: list, Count: len(list)}
	if !req.AllProjects {
		res.Project = req.Project
	}
	if len(list) == 0 && !req.AllProjects {
		other := f
		other.Elsewhere, other.Limit = true, 0
		rest, err := d.Store.ListNotes(other)
		if err != nil {
			return nil, err
		}
		res.Elsewhere = len(rest)
	}
	return res, nil
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

func hNoteUpdate(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.noteTransition(req, func(n *tasks.Note) (tasks.Event, error) {
		return tasks.NoteUpdate(n, by, argString(req.Args, "body"), d.Now())
	})
}

func hNoteDiscuss(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	question := argString(req.Args, "question")
	// Checked before the FIRST transition: the question is only read by the
	// second one, and a question over the bound would otherwise open a
	// discussion and then refuse to ask it — a half-written call the caller
	// is told failed.
	if err := tasks.BoundText("question", question); err != nil {
		return nil, err
	}
	res, err := d.noteTransition(req, func(n *tasks.Note) (tasks.Event, error) {
		return tasks.NoteDiscuss(n, by, d.Now())
	})
	if err != nil || question == "" {
		return res, err
	}
	// Two state changes, so two events (§5.5). The guard is dropped for the
	// second one: the row moved because the first one moved it.
	req.BaseUpdatedAt = 0
	return d.noteTransition(req, func(n *tasks.Note) (tasks.Event, error) {
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

// Promotion is the operator's authority and no longer the operator's alone to
// exercise (§3.7): an agent confirms with the user and promotes, and
// tasks.NotePromote marks the event with who actually did it.
func hNotePromote(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	n, err := d.Store.GetNote(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	target, err := promoteTarget(n.Project, argString(req.Args, "to-project"))
	if err != nil {
		return nil, err
	}
	title := argString(req.Args, "title")
	if title == "" {
		title = firstLine(n.Body)
	}
	updated, t, err := d.Store.PromoteNote(req.Project, n.ID, req.BaseUpdatedAt, tasks.NewTaskInput{
		Project:     target,
		Title:       title,
		Description: n.Body,
		Validation:  criteria(argStrings(req.Args, "validation")),
		PaneID:      req.PaneID,
	}, argStrings(req.Args, "also"), by, d.Now())
	if err != nil {
		return nil, err
	}
	d.emitted(n.Project, "note", n.ID)
	for _, ref := range argStrings(req.Args, "also") {
		if folded, err := d.Store.GetNote(req.Project, ref); err == nil {
			d.emitted(folded.Project, "note", folded.ID)
		}
	}
	// The task's created event lands on the TARGET project, which is a board
	// nothing else in this call touches: without this, a cross-project promote
	// is invisible to everyone watching the board that got the work.
	d.emitted(t.Project, "task", t.ID)
	return PromoteResult{Note: updated, Task: t}, nil
}

// promoteTarget is the board the task lands on: the note's own project unless
// --to-project named another one. The door resolves the flag the way §4.2
// resolves --project, because a relative path is relative to the CALLER's
// working directory and the daemon's is somewhere else; what arrives here is
// already canonical, and anything that is not is a caller that skipped that
// step rather than a path to guess at.
func promoteTarget(noteProject, to string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return noteProject, nil
	}
	if !filepath.IsAbs(to) {
		return "", codes.Errorf(codes.Usage,
			"the target project must be a resolved absolute path, not %q", to)
	}
	// The board must already exist, and the path must canonicalize the way
	// --project does (§4.2): a promote into a path that is not there is a
	// typo, and inventing a board nobody will find fails quietly. Checked
	// HERE rather than in one door, so the other door cannot skip it.
	if info, err := os.Stat(to); err != nil || !info.IsDir() {
		return "", codes.Errorf(codes.Usage,
			"cannot resolve the target project %q: not a directory", to)
	}
	proj, err := project.Resolve(project.Options{Explicit: to})
	if err != nil {
		return "", codes.Errorf(codes.Usage, "cannot resolve the target project %q: %v", to, err)
	}
	return proj, nil
}

// FoldResult is a fold: the note and the task it was folded into. It carries
// the task for the reason PromoteResult does — the caller asked to put a note
// on a task and gets back what both of them are now.
type FoldResult struct {
	Note *tasks.Note `json:"note"`
	Task *tasks.Task `json:"task"`
}

// Folding follows promotion (§3.7): advisory, marked by tasks.NoteFold.
func hNoteFold(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	target, err := promoteTarget(req.Project, argString(req.Args, "to-project"))
	if err != nil {
		return nil, err
	}
	n, t, err := d.Store.FoldNote(req.Project, argString(req.Args, "id"), req.BaseUpdatedAt,
		target, argString(req.Args, "into"), by, d.Now())
	if err != nil {
		return nil, err
	}
	d.emitted(n.Project, "note", n.ID)
	return FoldResult{Note: n, Task: t}, nil
}

func hNoteUnfold(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	return d.noteTransition(req, func(n *tasks.Note) (tasks.Event, error) {
		return tasks.NoteUnfold(n, by, d.Now())
	})
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

func hNoteDelete(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	n, err := d.Store.GetNote(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	if err := d.Store.DeleteNote(req.Project, n.ID, by); err != nil {
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

// Resolving a deferral is the operator overruling the gate, and §3.7 makes
// that advice an agent confirms rather than a refusal this door makes. The
// parked row records WHO resolved it, because §9.3 re-runs the verb under the
// ORIGINAL subject: without resolved_by the trail would show the deferred
// agent acting and no one deciding it could.
func hParkedResolve(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	p, err := d.Store.GetParked(req.Project, argString(req.Args, "id"))
	if err != nil {
		return nil, err
	}
	if argBool(req.Args, "reject") {
		if err := d.Store.ResolveParked(req.Project, p.ID, "rejected", by.Principal, d.Now()); err != nil {
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
	// The subject is carried back in the form the door would have derived it:
	// a pane for an agent, an explicit --as for the principals §3.2 allows to
	// declare themselves. Reconstructing only the agent case would silently
	// re-run a cron's verb as the operator, who is recusal-exempt.
	if pane, ok := strings.CutPrefix(p.Subject, "agent:"); ok {
		rerun.PaneID = pane
	} else if p.Subject == string(tasks.PrincipalHuman) {
		// §3.7: `human` with no pane is reproducible only by a process that
		// speaks for the operator, which is what parked the action.
		rerun.Operator = true
	} else if p.Subject != string(tasks.PrincipalNone) {
		rerun.As = p.Subject
	}
	actor, err := d.actor(rerun)
	if err != nil {
		return nil, err
	}
	// Mark BEFORE dispatching. The UPDATE's `state = 'parked'` guard is the
	// one-winner CAS, so putting it after the verb left a window in which two
	// resolves both read `parked` and both ran the verb — the side effect
	// really happened twice, and the loser was told CONFLICT for work that had
	// already committed.
	if err := d.Store.ResolveParked(req.Project, p.ID, "resolved", by.Principal, d.Now()); err != nil {
		return nil, err
	}
	out, err := d.dispatch(verb.Name, rerun, actor)
	if err != nil {
		// The decision stands; the verb did not run. Say why, in the verb's
		// own words — the caller needs to know their approve was refused, not
		// that a queue row was busy.
		if ferr := d.Store.FailParked(req.Project, p.ID, err.Error(), d.Now()); ferr != nil {
			return nil, ferr
		}
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

func hSweep(d *Daemon, req protocol.Request, by tasks.Actor) (any, error) {
	if pane := argString(req.Args, "pane"); pane != "" {
		// A pane's leases are that pane's to give back, or the operator's to
		// take. Any principal could strip every lease a LIVE rival held, with
		// one flag and no id. Herdr is not consulted for liveness: it can be
		// unreachable, and a rule that silently allows when it cannot answer
		// is worse than one that never asks.
		if !by.IsHuman() && by.Principal != tasks.Principal("agent:"+pane) {
			return nil, codes.Errorf(codes.Forbidden,
				"%s holds the leases of pane %s; that pane or the operator releases them", by.Principal, pane)
		}
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

// firstLine is the title a promoted note becomes. Runes, not bytes: this
// result is STORED, so a cut landing inside a multi-byte character writes half
// of one into the database and it is read back that way forever.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(line); len(r) > 120 {
		line = strings.TrimSpace(string(r[:120]))
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
