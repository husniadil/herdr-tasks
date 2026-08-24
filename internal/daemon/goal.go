package daemon

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// GoalLimit is §16.2's ceiling: a /goal condition must be paste-ready, which
// means it must fit. The limit is a MUST, so it holds for a task written to
// break it as much as for one written carefully.
const GoalLimit = 4000

// What a section may take when there is room to spare. These are taste, not
// contract: they keep an ordinary goal readable rather than letting one long
// description crowd out the rest.
const (
	descriptionCap = 1200
	feedbackCap    = 600
	releaseNoteCap = 400
	criterionCap   = 300
	// A section trimmed below this is not worth printing: an opening clause
	// and an ellipsis tell the reader less than its absence does.
	sectionFloor = 48
)

// BuildGoal renders a task as a paste-ready `/goal` condition (§16.2):
// the directive from the title, context from the description and the latest
// feedback, "Done when" from the criteria plus the obligation to run
// `htask submit` and show its output, and a stop clause that releases the
// claim with a note and files out-of-scope findings rather than doing them.
//
// When it does not all fit, the order of sacrifice is fixed. Context gives way
// first — description, release note and reviewer feedback are what a claimer
// can recover with `htask get`. Criteria give way next, and say how many
// went and where to read them. The directive, the submit obligation and the
// stop clause never give way: a condition missing those is not a shorter
// version of this task, it is a different and worse one.
func BuildGoal(t *tasks.Task) string { return buildGoal(t, "\n") }

// BuildGoalOneLine renders the same condition with no newline byte anywhere,
// for the channel §11.4 points a plugin at: Herdr refuses any newline in agent
// argv outright, so a multi-line condition cannot be delivered as the initial
// prompt of `herdr agent start` at all, while the same document flattened to
// one line registers whole.
//
// A break costs one byte as a document and four as a line, so the ceiling
// binds sooner here. The budget is spent on what is printed: the order of
// sacrifice above is the same one, it just has less to spend, and a one-line
// goal that dropped criteria says so exactly as the document does.
func BuildGoalOneLine(t *tasks.Task) string {
	return strings.TrimSuffix(buildGoal(t, oneLineSep), oneLineSep)
}

// oneLineSep is what a line break becomes on the argv path. It was measured
// through a real `herdr agent start`: the flattened condition registered whole
// and the pane reported the goal active.
const oneLineSep = " · "

func buildGoal(t *tasks.Task, nl string) string {
	ref := fmt.Sprintf("%d", t.Seq)

	touch := nl + "Renew the claim with `htask touch " + ref +
		"` at the start of every turn; a lapsed lease is swept and the task returns to the queue." + nl
	doneHeader := nl + "Done when:" + nl
	submit := "- `htask submit " + ref +
		" --report \"<what you did and how you verified it>\" --evidence \"<a command you ran and what it printed>\"`" +
		" has been run, and its output is shown in this conversation." + nl
	stop := nl + "Stop instead of pushing on if you are blocked, out of scope, or out of turns: run `htask release " +
		ref + " --note \"<what is left>\"` and say why. File anything you found that is not this task as " +
		"`htask note add \"<the finding>\"`, or as `htask create \"<the work>\" --discovered-from " + ref +
		"` — do not do it under this task." + nl

	fixed := len(touch) + len(doneHeader) + len(submit) + len(stop)
	// Room for the line that says criteria were left out, held back before
	// anything discretionary is measured: a goal that silently drops half its
	// acceptance criteria is worse than one that says it did.
	if len(t.Validation) > 0 {
		fixed += len(criteriaNotice(len(t.Validation), ref, nl))
	}
	// The directive is mandatory, but nothing bounds a title's length and the
	// ceiling is a MUST. A title with no room left is clipped rather than
	// allowed to push the obligations out of the document.
	title := flatten(strings.TrimSpace(t.Title), nl)
	directive := title + "." + nl
	if room := GoalLimit - fixed; len(directive) > room {
		directive = clip(title, max(room-2, 0)) + "." + nl
	}

	// fixed already holds back the notice, so hand fitCriteria that room back:
	// it spends it only if it actually drops something.
	budget := GoalLimit - fixed - len(directive)
	if len(t.Validation) > 0 {
		budget += len(criteriaNotice(len(t.Validation), ref, nl))
	}
	criteria, notice := fitCriteria(t.Validation, budget, ref, nl)
	context := fitContext(t, budget-len(criteria)-len(notice), nl)

	var b strings.Builder
	b.WriteString(directive)
	b.WriteString(context)
	b.WriteString(touch)
	b.WriteString(doneHeader)
	b.WriteString(criteria)
	b.WriteString(submit)
	// After the obligation, so a reader who stops at the first thing they must
	// do still sees it.
	b.WriteString(notice)
	b.WriteString(stop)
	return b.String()
}

// fitCriteria renders as many criteria as the budget holds, and the line that
// says how many it left out. Both together are guaranteed to fit the budget:
// the notice is reserved before the first criterion is rendered, and returned
// so the caller can charge for it once rather than letting context spend the
// same bytes.
func fitCriteria(list []tasks.Criterion, budget int, ref, nl string) (lines, notice string) {
	if len(list) == 0 {
		return "", ""
	}
	// The worst case is every criterion dropped, which is also the longest the
	// notice can be.
	reserve := len(criteriaNotice(len(list), ref, nl))
	if budget < reserve {
		// Not even the apology fits. Nothing here is safe to print.
		return "", ""
	}
	var b strings.Builder
	used, shown := 0, 0
	for _, c := range list {
		line := "- " + clip(flatten(strings.TrimSpace(c.Text), nl), criterionCap)
		if !c.Required {
			line += " (optional)"
		}
		line += nl
		room := budget - used
		if shown < len(list)-1 {
			room -= reserve
		}
		if len(line) > room {
			break
		}
		b.WriteString(line)
		used += len(line)
		shown++
	}
	if shown == len(list) {
		return b.String(), ""
	}
	return b.String(), criteriaNotice(len(list)-shown, ref, nl)
}

// criteriaNotice is the line a truncated "Done when" block carries, and the
// reservation the budget makes for it.
func criteriaNotice(n int, ref, nl string) string {
	return fmt.Sprintf("%s%d further criteria did not fit here; run `htask get %s` for the full list.%s", nl, n, ref, nl)
}

// fitContext renders the parts a claimer could recover for themselves. They
// share whatever the mandatory text and the criteria left, in proportion to
// what each would take unconstrained, and a share too small to be worth
// reading is dropped rather than printed as an ellipsis.
func fitContext(t *tasks.Task, budget int, nl string) string {
	type section struct {
		open string
		body string
		cap  int
	}
	candidates := []section{
		{nl + "Context: ", flatten(strings.TrimSpace(t.Description), nl), descriptionCap},
		{nl + "This was rejected once. The reviewer said: ", flatten(strings.TrimSpace(t.Feedback), nl), feedbackCap},
		{nl + "The last claimer left off here: ", flatten(strings.TrimSpace(t.ReleaseNote), nl), releaseNoteCap},
	}
	present := make([]section, 0, len(candidates))
	wanted := 0
	for _, s := range candidates {
		if s.body == "" {
			continue
		}
		present = append(present, s)
		wanted += len(s.open) + min(len(s.body), s.cap) + 1
	}
	if len(present) == 0 || budget <= 0 {
		return ""
	}
	var b strings.Builder
	left := budget
	for _, s := range present {
		want := len(s.open) + min(len(s.body), s.cap) + 1
		share := want
		if wanted > budget {
			// Proportional, so one long section cannot starve the others.
			share = budget * want / wanted
		}
		if share > left {
			share = left
		}
		bodyRoom := share - len(s.open) - 1
		// The floor governs a squeeze, not a short section: an opening clause
		// and an ellipsis say less than nothing, but a 30-character note that
		// fits whole is worth printing.
		if bodyRoom < len(s.body) && bodyRoom < sectionFloor {
			continue
		}
		if bodyRoom < 0 {
			continue
		}
		text := s.open + clip(s.body, bodyRoom) + nl
		b.WriteString(text)
		left -= len(text)
	}
	out := b.String()
	if t.Feedback != "" && strings.Contains(out, "The reviewer said") {
		out += "Address that first." + nl
		if len(out) > budget {
			// The closing line pushed it over; the sentence it closes is worth
			// more than the line itself.
			out = strings.TrimSuffix(out, "Address that first."+nl)
		}
	}
	return out
}

// flatten turns text written over several lines into one, so a description in
// paragraphs cannot put a newline back into a rendering that must not carry
// one. It runs before anything is measured: the separator is wider than the
// break it replaces, and a budget spent on the pre-flattened text would be
// spending bytes that are not the ones printed. Blank lines collapse rather
// than becoming two separators with nothing between them.
func flatten(s, nl string) string {
	if nl == "\n" || !strings.ContainsAny(s, "\n\r") {
		return s
	}
	parts := make([]string, 0, 4)
	// A lone carriage return is a line break too, and one left in place would
	// be a byte the argv path refuses just as surely as a newline.
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, nl)
}

// clip cuts to at most max bytes, marker included, on a rune and preferably a
// word boundary, and says that it cut. Returning more than max is the failure
// that let a goal past its own ceiling.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const marker = " […]"
	if max < len(marker) {
		return ""
	}
	cut := trimToRune(s[:max-len(marker)])
	if i := strings.LastIndexAny(cut, " \n"); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \n") + marker
}

// trimToRune drops a trailing partial UTF-8 sequence, so a trim never cuts a
// character in half.
func trimToRune(s string) string {
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
