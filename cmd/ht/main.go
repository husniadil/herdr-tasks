// Command ht is herdr-tasks: the daemon, the CLI, and the MCP server in one
// binary (§2.1). It conforms to the shared plugin contract, v0.
package main

import (
	"fmt"
	"os"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra's own errors (an unknown flag, a missing argument) are caller
		// input errors, which the contract fixes at USAGE / exit 2.
		code := codes.Usage
		if ce, ok := err.(*codes.Error); ok {
			code = ce.Code
		}
		if f, ok := err.(interface{ Code() string }); ok {
			code = f.Code()
		}
		fmt.Fprintln(os.Stderr, "ht: "+err.Error())
		os.Exit(codes.Exit(code))
	}
}
