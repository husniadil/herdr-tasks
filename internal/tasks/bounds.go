package tasks

import (
	"fmt"
	"unicode/utf8"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// §5.9: a verb that accepts free text enforces a documented length bound with
// USAGE at write time. These are the documented numbers; the README carries
// the same table for callers who never read this file.
//
// They are chosen to be generous for real use and to stop pathological input,
// not to be tight. When they were set, the longest values this plugin had
// actually stored were an 86-character title, a 747-character description, a
// 2,225-character report, a 720-character evidence entry, a 257-character
// criterion, and a 7,902-character verdict reason. Every bound is a wide
// multiple of what real use looks like, so hitting one means something went
// wrong rather than something got long.
//
// Lengths are counted in characters, not bytes, so a note written in a
// language that does not fit ASCII gets the same allowance as one that does.
const (
	// MaxTitle bounds a one-line field. §16.2 turns a title into the
	// directive of a /goal condition, where a paragraph would not belong.
	MaxTitle = 200
	// MaxText bounds a prose field: description, report, feedback, a note
	// body, a release note, a reason, a question.
	MaxText = 20_000
	// MaxItem bounds one entry in a repeatable field: an acceptance
	// criterion, a piece of evidence.
	MaxItem = 4_000
	// MaxItems bounds how many entries a repeatable field may carry, because
	// a bound on each of a million entries bounds nothing.
	MaxItems = 200
)

// bound refuses free text longer than max, naming the field and the limit so
// the caller can fix the call without reading this file.
func bound(field, s string, max int) error {
	if n := utf8.RuneCountInString(s); n > max {
		return codes.Errorf(codes.Usage,
			"%s is %d characters, over the %d allowed (§5.9)", field, n, max)
	}
	return nil
}

// boundList refuses a repeatable field with too many entries, or any one
// entry that is too long. The index is named because "one of them is too
// long" is not something a caller can act on.
func boundList(field string, items []string, max, maxItems int) error {
	if len(items) > maxItems {
		return codes.Errorf(codes.Usage,
			"%s has %d entries, over the %d allowed (§5.9)", field, len(items), maxItems)
	}
	for i, s := range items {
		if err := bound(fmt.Sprintf("%s[%d]", field, i), s, max); err != nil {
			return err
		}
	}
	return nil
}
