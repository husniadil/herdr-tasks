package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/herdrclient"
	"github.com/husniadil/herdr-tasks/internal/store"
)

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the daemon: the socket server and the only writer of the store",
		Long: "One daemon per user (§2.3). It owns the SQLite file; the CLI and the MCP\n" +
			"server reach it over the socket. A CLI call starts it on demand, so running\n" +
			"this by hand is for watching it work or for the manifest's startup command.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, cleanup, err := openDaemon()
			if err != nil {
				return err
			}
			defer cleanup()
			ln, err := daemon.Listen(config.SocketPath())
			if err != nil {
				return err
			}
			defer ln.Close()

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			go reloadOnHUP(ctx, d)

			fmt.Fprintf(os.Stderr, "tasks: listening on %s\n", config.SocketPath())
			// The reconciliation sweep §8.4 allows at start: leases whose panes
			// died while the daemon was down are released before anything else.
			d.Sweep()
			return d.Serve(ctx, ln)
		},
	}
}

// openDaemon wires the store, the config and the herdr client together.
func openDaemon() (*daemon.Daemon, func(), error) {
	if err := config.EnsureStateDir(); err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	s, err := store.Open(config.DBPath())
	if err != nil {
		return nil, nil, err
	}
	return daemon.New(s, cfg, herdrclient.FromEnv()), func() { s.Close() }, nil
}

// reloadOnHUP re-reads the config on SIGHUP (§10.1).
func reloadOnHUP(ctx context.Context, d *daemon.Daemon) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tasks: reload: %v\n", err)
				continue
			}
			d.Reload(cfg)
			fmt.Fprintln(os.Stderr, "tasks: config reloaded")
		}
	}
}
