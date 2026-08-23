package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/husniadil/herdr-tasks/internal/mcpdoor"
)

func newMCPCmd() *cobra.Command {
	var opt mcpdoor.Options
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve MCP over stdio",
		Long: "A thin door over the same daemon calls as the CLI (§7.2). MCP and the CLI\n" +
			"are both first-class surfaces (§7.3).\n\n" +
			"A door standing in a Herdr pane is that pane's agent. A door standing in no\n" +
			"pane has NO principal and is refused the operator's verbs, unless it was\n" +
			"started with --operator (§3.7).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return mcpdoor.Serve(ctx, nil, opt)
		},
	}
	// §7.5: read once, from the server command. It is deliberately NOT a
	// persistent flag — a flag every verb carried would be a per-call
	// declaration, which is the thing this exists instead of.
	cmd.Flags().BoolVar(&opt.Operator, "operator", false,
		"Declare that this door speaks for the operator (§7.5). Set it once, in the client's\n"+
			"server configuration, where a human wrote it deliberately. Without it a door in no\n"+
			"Herdr pane has no principal and operator verbs refuse it (§3.7).")
	return cmd
}
