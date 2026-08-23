package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/tui"
)

// newTUICmd is `htask tui [<view>]` (§2.1): the human door, opened as a Herdr
// pane (§11.6) or from a terminal. It is the one subcommand that reads a TTY
// (§2.5).
func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui [view]",
		Short: "Open the board, or the notes board, in this terminal",
		Long: "board shows this project's tasks in their state columns with the claim and\n" +
			"its lease on each task; notes shows the notes board and the actions the\n" +
			"policy gate parked. Mouse first, keyboard everywhere the mouse can go.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, argv []string) error {
			arg := ""
			if len(argv) > 0 {
				arg = argv[0]
			}
			view, err := tui.ParseView(arg)
			if err != nil {
				return codes.Errorf(codes.Usage, "%v", err)
			}
			base, err := request("", nil)
			if err != nil {
				return err
			}
			if !isTTY() {
				return codes.New(codes.Unsupported, "htask tui needs a terminal; use `htask task list` from a script")
			}
			return tui.Run(view, base)
		},
	}
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
