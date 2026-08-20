package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/husniadil/herdr-tasks/internal/mcpdoor"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve MCP over stdio",
		Long: "A thin door over the same daemon calls as the CLI (§7.2). The CLI is the\n" +
			"primary agent surface and carries every verb; these tools exist for\n" +
			"discoverability and namespace grounding.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return mcpdoor.Serve(ctx, nil)
		},
	}
}
