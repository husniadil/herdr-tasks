package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// This file is the note-editing trip, split so every step of it is testable
// without a terminal or an editor: the model asks (Edit), the runtime hands
// the file to $EDITOR through tea.ExecProcess, and these decide what the
// editor was given, what came back, and what happens to the operator's text
// if the daemon will not take it.

// editorCommand is which editor to run, resolved the way every terminal tool
// resolves it: VISUAL, then EDITOR. The value goes through `sh -c` because an
// editor is routinely set with flags ("code -w"), and the file arrives as $1
// so a path with a space in it cannot come apart.
//
// With neither set it REFUSES and names both. Falling back to vi is the
// tempting answer and the wrong one here: a popup that drops an operator into
// a modal editor they may not know how to leave is the trap §11.6's close key
// exists to have closed, and a refusal that names the variable to set is
// something they can act on.
func editorCommand(env func(string) string, path string) ([]string, error) {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(env(name)); v != "" {
			return []string{"sh", "-c", v + ` "$1"`, "sh", path}, nil
		}
	}
	return nil, codes.New(codes.Unsupported,
		"no editor: set VISUAL or EDITOR in the environment herdr was started from, then reopen the board")
}

// startEdit writes the body where the editor can reach it. The file keeps a
// .md suffix because that is what makes an editor treat prose as prose.
func startEdit(e Edit) (string, error) {
	f, err := os.CreateTemp("", fmt.Sprintf("ht-note-%d-*.md", e.Seq))
	if err != nil {
		return "", codes.Errorf(codes.Unexpected, "cannot write the note out to edit: %v", err)
	}
	return writeEditFile(f, e.Body)
}

// writeEditFile fills the file and closes it, and clears up after itself when
// it cannot. A file that was never written has nothing in it worth keeping —
// unlike the one finishEdit keeps and names when the DAEMON refuses the edit,
// which holds work the operator did and must survive.
func writeEditFile(f *os.File, body string) (string, error) {
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(f.Name())
		return "", codes.Errorf(codes.Unexpected, "cannot write the note out to edit: %v", err)
	}
	if _, err := f.WriteString(body + "\n"); err != nil {
		return fail(err)
	}
	// Closing is where a buffered write actually fails, so its error is the
	// same failure and gets the same clean-up.
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", codes.Errorf(codes.Unexpected, "cannot write the note out to edit: %v", err)
	}
	return f.Name(), nil
}

// finishEdit reads back what the editor left and decides whether there is
// anything to send. Nothing is sent for a body that did not change: an editor
// opened and closed is not an edit, and a note.update for identical text
// would still write an event saying it was edited.
func finishEdit(e Edit, path string, runErr error) (*Call, error) {
	if runErr != nil {
		return nil, codes.Errorf(codes.Unexpected, "the editor did not finish: %v", runErr)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, codes.Errorf(codes.Unexpected, "cannot read the edited note back: %v", err)
	}
	// An editor adds a trailing newline; the operator did not type it.
	body := strings.TrimRight(string(raw), "\n")
	if strings.TrimSpace(body) == "" {
		return nil, codes.New(codes.Usage,
			"the body was emptied, which is not an edit; drop the note instead (§5.7)")
	}
	if body == e.Body {
		return nil, nil
	}
	return &Call{Verb: "note.update", Args: map[string]any{"id": e.NoteID, "body": body}}, nil
}

// afterUpdate is what becomes of the scratch file. It goes when the daemon has
// the text and STAYS when it does not, with its path in the message: a note
// that lost a race while the operator was typing must not also lose what they
// typed.
func afterUpdate(path string, err error) Msg {
	if err == nil {
		os.Remove(path)
		return DoneMsg{Status: "note.update done"}
	}
	out := errMsg(err)
	out.Message += fmt.Sprintf(" — your text is kept at %s", path)
	return out
}
