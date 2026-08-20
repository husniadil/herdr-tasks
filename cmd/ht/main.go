// Command ht is herdr-tasks: the daemon, the CLI, and the MCP server in one
// binary (§2.1). It conforms to the shared plugin contract, v0.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

func main() {
	root := newRootCmd()
	// --json has to be known BEFORE cobra parses, because cobra's own parse
	// failures are among the failures that must answer with one document
	// (§6.2). At that moment the flag exists only in argv.
	g.jsonOut = wantsJSON(os.Args[1:])

	err := root.Execute()
	if err == nil {
		return
	}
	// Cobra's own errors (an unknown flag, an unknown subcommand) are caller
	// input errors, which the contract fixes at USAGE / exit 2.
	code := codes.Usage
	if ce, ok := err.(*codes.Error); ok {
		code = ce.Code
	}
	if f, ok := err.(interface{ Code() string }); ok {
		code = f.Code()
	}
	// One failure, one report, one stream. In --json the envelope IS the
	// report and it goes to stdout — last, after any documents a stream
	// already wrote, because nothing can retract those. Otherwise a sentence
	// goes to stderr and stdout stays empty. Never both: a caller reading two
	// reports has no way to tell one failure from two.
	if g.jsonOut {
		printErrorEnvelope(err, code)
	} else {
		fmt.Fprintln(os.Stderr, "ht: "+err.Error())
	}
	os.Exit(codes.Exit(code))
}

// wantsJSON reads --json out of the raw argv. A value that is not an explicit
// false counts as asking for a document: cobra will refuse the bad value
// itself, and a machine caller that asked for JSON should be told so in JSON.
func wantsJSON(argv []string) bool {
	on := false
	for _, a := range argv {
		switch {
		case a == "--":
			return on
		case a == "--json":
			on = true
		case strings.HasPrefix(a, "--json="):
			v, err := strconv.ParseBool(strings.TrimPrefix(a, "--json="))
			on = err != nil || v
		}
	}
	return on
}
