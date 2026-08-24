package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// notAVerb is the written-down list of cobra subcommands that are deliberately
// not verbs of the registry, each with the reason it is not — the shape
// `enforcesNoContractSection` in substrate_test.go already uses: an exception
// is a listed entry with a reason, never a silent skip.
//
// Everything here has the same shape: it is how this PROCESS is run or what it
// says about itself, not an operation on the board. §7.3's parity is over
// board verbs, and a door has nothing to serve for "start the daemon" or "open
// the terminal pane" — the MCP door IS the harness's door, so a tool for `mcp`
// would be the door offering to start itself.
var notAVerb = map[string]string{
	"daemon":  "starts this binary's daemon; a harness that can call the door already has one running (§2.2)",
	"mcp":     "IS the MCP door; serving a tool that starts the door is the door offering to start itself",
	"tui":     "opens the §11.6 terminal pane, which needs a terminal a door does not have",
	"version": "says what this BINARY is, and `doctor` — which is a verb, on both doors — already carries the version and the contract revision",
	// cobra adds these itself once help or completion is first requested, and
	// they are the CLI's own affordances rather than anything this plugin
	// declares.
	"help":       "cobra's own, added when help is first requested",
	"completion": "cobra's own shell completion, added when it is first requested",
}

// §7.3: "A plugin MUST pin the totality with a test that fails when any verb
// its CLI serves is absent from its door." TestEveryCLIVerbIsServedByTheDoor
// answers that for the registry, but it walks `verbs.All` — so it can only see
// what the registry declares, and a cobra subcommand added straight to
// newRootCmd is invisible to it in exactly the way §7.3 cares about. `version`
// was already such a subcommand, with `--json` and no MCP tool, and every
// parity test stayed green.
//
// This walks the CLI as cobra assembles it and requires every command on it to
// be a registry verb, a grouping parent of one, or a written-down exemption.
func TestEveryCLISubcommandIsAVerbOrAWrittenDownException(t *testing.T) {
	// Every path the registry puts on the CLI, plus the parent of each
	// grouped verb, which newRootCmd synthesises rather than declaring.
	fromRegistry := map[string]bool{}
	for _, v := range verbs.All {
		for i := range v.CLI {
			fromRegistry[strings.Join(v.CLI[:i+1], " ")] = true
		}
	}
	if len(fromRegistry) == 0 {
		t.Fatal("the verb registry is empty; this test would pass on an empty CLI")
	}

	seen := 0
	var walk func(cmd *cobra.Command, prefix []string)
	walk = func(cmd *cobra.Command, prefix []string) {
		for _, sub := range cmd.Commands() {
			path := append(append([]string{}, prefix...), sub.Name())
			joined := strings.Join(path, " ")
			seen++
			switch {
			case fromRegistry[joined]:
			case len(path) == 1 && notAVerb[sub.Name()] != "":
			default:
				t.Errorf("`htask %s` is on the CLI, is not a verb of the registry, and is not "+
					"written down in notAVerb. §7.3 admits no CLI-only verb: put it in "+
					"internal/verbs so both doors serve it, or record here why it is not a "+
					"board operation at all", joined)
			}
			walk(sub, path)
		}
	}
	walk(newRootCmd(), nil)
	if seen < len(fromRegistry) {
		t.Fatalf("walked %d subcommands for %d registry paths; the walk is not reaching the tree", seen, len(fromRegistry))
	}
}

// An exemption nobody can check is worse than none: a name written down here
// that the CLI no longer carries is a reason kept for a command that has gone.
// The two cobra adds lazily are exempt from THIS half — they are absent until
// help or completion is first asked for, and that absence is the normal state.
func TestEveryWrittenDownExemptionIsStillOnTheCLI(t *testing.T) {
	lazy := map[string]bool{"help": true, "completion": true}
	on := map[string]bool{}
	for _, sub := range newRootCmd().Commands() {
		on[sub.Name()] = true
	}
	for name, why := range notAVerb {
		if why == "" {
			t.Errorf("%q is exempted with no reason", name)
		}
		if !lazy[name] && !on[name] {
			t.Errorf("%q is written down as not a verb and is not on the CLI either; drop its entry", name)
		}
	}
}
