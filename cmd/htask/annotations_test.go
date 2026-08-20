package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A comment states what the code IS and the constraint it holds, never what it
// replaced: git carries the history, which is why this guard reads tracked
// source and docs and not commit messages. The rule was enforced by a reviewer
// running a grep by hand, and it regressed twice in two days, so it is enforced
// here instead.

// annotationPatterns are matched case-insensitively against each line. Literal
// phrases only. "Former-name framing" is deliberately absent: it is a
// paraphrase rather than a pattern, and no grep separates "the binary is htask"
// from a sentence that legitimately names two things.
var annotationPatterns = []string{"used to", "previously", "formerly", "before this change"}

// annotationException is one line that matches a pattern in a different sense.
// It is keyed by file and by the matched line's trimmed text — never by line
// number, which moves whenever anything above it does.
type annotationException struct {
	File string
	Text string
	Why  string
}

// allowedAnnotations is the written-down list, in the shape untaught already
// uses in docs_test.go: an exception is a listed entry with a reason, and an
// entry that has gone stale fails too, so the list cannot outlive its subject.
var allowedAnnotations = []annotationException{{
	File: "internal/daemon/daemon_test.go",
	Text: "// A deadline, because the failure this is used to catch is a request that",
	Why:  "employed-to-catch, the ordinary present-tense sense: it says what the deadline on the next line is for, and names no prior version",
}}

// guardOwnFile is this file, which necessarily quotes every phrase it hunts.
const guardOwnFile = "cmd/htask/annotations_test.go"

func TestProseCarriesNoHistoryAnnotations(t *testing.T) {
	files := trackedProse(t)
	if len(files) < 20 {
		t.Fatalf("scanned %d tracked source and doc files; the file list is reading nothing", len(files))
	}
	t.Logf("%d tracked source and doc files scanned (.go and .md, excluding %s)", len(files), guardOwnFile)

	allowed := map[string]bool{}
	for _, e := range allowedAnnotations {
		allowed[e.File+"\x00"+e.Text] = true
	}
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			hit := matchedAnnotation(line)
			if hit == "" || allowed[rel+"\x00"+strings.TrimSpace(line)] {
				continue
			}
			t.Errorf("%s:%d says what the code replaced (%q): state what it IS and the constraint it holds; git carries the history. If this is a different sense, add it to allowedAnnotations with the reason.\n\t%s",
				rel, i+1, hit, strings.TrimSpace(line))
		}
	}

	// An excuse that has outlived its line is worse than no excuse: it records
	// a decision about prose that has since been rewritten or removed.
	for _, e := range allowedAnnotations {
		if strings.TrimSpace(e.Why) == "" {
			t.Errorf("allowedAnnotations entry for %s carries no reason", e.File)
			continue
		}
		if matchedAnnotation(e.Text) == "" {
			t.Errorf("allowedAnnotations excuses %s for %q, which matches no pattern; drop the entry", e.File, e.Text)
			continue
		}
		body, err := os.ReadFile(filepath.Join("..", "..", e.File))
		if err != nil {
			t.Errorf("allowedAnnotations names %s, which is not in the repository: %v", e.File, err)
			continue
		}
		if !bytes.Contains(body, []byte(e.Text)) {
			t.Errorf("allowedAnnotations excuses a line %s no longer contains: %q. Drop the entry.", e.File, e.Text)
		}
	}
}

func matchedAnnotation(line string) string {
	lower := strings.ToLower(line)
	for _, p := range annotationPatterns {
		if strings.Contains(lower, p) {
			return p
		}
	}
	return ""
}

// trackedProse is every tracked .go and .md file. Tracked, because an
// untracked scratch file is not prose this repository ships; git rather than a
// directory walk, because the two disagree exactly on the files that are
// ignored on purpose.
func trackedProse(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "--", "*.go", "*.md")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := []string{}
	for _, name := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if name != "" && name != guardOwnFile {
			files = append(files, name)
		}
	}
	return files
}
