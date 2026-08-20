package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/husniadil/herdr-tasks/internal/client"
	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/project"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// global flags every verb accepts.
type globals struct {
	jsonOut       bool
	projectPath   string
	allProjects   bool
	as            string
	baseUpdatedAt int64
	follow        bool
}

var g globals

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ht",
		Short: "A task backlog and notes board for agents running on Herdr",
		Long: "herdr-tasks: tasks move todo → doing → review → done with claims, leases,\n" +
			"evidence and review; notes are pre-decision ideas a human promotes or drops.\n" +
			"Conforms to the shared plugin contract, v0.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&g.jsonOut, "json", false, "Print one JSON document on stdout (§6.2)")
	root.PersistentFlags().StringVar(&g.projectPath, "project", "", "The project to act in; defaults to this directory's repository (§4.2)")
	root.PersistentFlags().BoolVar(&g.allProjects, "all-projects", false, "Act across every project rather than this one")
	root.PersistentFlags().StringVar(&g.as, "as", "", "Act as a cron, trigger or plugin principal (§3.2)")

	groups := map[string]*cobra.Command{}
	for _, v := range verbs.All {
		cmd := buildVerb(v)
		if len(v.CLI) == 1 {
			root.AddCommand(cmd)
			continue
		}
		parent, ok := groups[v.CLI[0]]
		if !ok {
			parent = &cobra.Command{Use: v.CLI[0], Short: groupShort(v.CLI[0])}
			groups[v.CLI[0]] = parent
			root.AddCommand(parent)
		}
		parent.AddCommand(cmd)
	}
	root.AddCommand(newDaemonCmd(), newMCPCmd(), newTUICmd(), newVersionCmd())
	return root
}

func groupShort(name string) string {
	switch name {
	case "task":
		return "Work with tasks"
	case "note":
		return "Work with notes on the board"
	case "parked":
		return "Work with actions the policy gate deferred"
	}
	return name
}

// buildVerb turns one registry entry into a cobra command, so the CLI and the
// MCP door cannot drift (§6.1).
func buildVerb(v verbs.Verb) *cobra.Command {
	name := v.CLI[len(v.CLI)-1]
	positional := []verbs.Arg{}
	for _, a := range v.Args {
		if a.Positional {
			positional = append(positional, a)
		}
	}
	use := name
	for _, a := range positional {
		if a.Required {
			use += " <" + a.Name + ">"
		} else {
			use += " [" + a.Name + "]"
		}
	}
	cmd := &cobra.Command{Use: use, Short: v.Short, Long: v.Long, Args: cobra.MaximumNArgs(len(positional))}

	strs := map[string]*string{}
	ints := map[string]*int64{}
	bools := map[string]*bool{}
	lists := map[string]*[]string{}
	for _, a := range v.Args {
		if a.Positional {
			continue
		}
		switch a.Type {
		case verbs.String:
			strs[a.Name] = cmd.Flags().String(a.Name, "", a.Desc)
		case verbs.Int:
			ints[a.Name] = cmd.Flags().Int64(a.Name, 0, a.Desc)
		case verbs.Bool:
			bools[a.Name] = cmd.Flags().Bool(a.Name, false, a.Desc)
		case verbs.Strings:
			lists[a.Name] = cmd.Flags().StringArray(a.Name, nil, a.Desc)
		}
	}
	if v.Mutates {
		cmd.Flags().Int64Var(&g.baseUpdatedAt, "base-updated-at", 0,
			"Fail with CONFLICT if the row moved since this updated_at (§5.6)")
	}
	if v.Name == "events" {
		cmd.Flags().BoolVar(&g.follow, "follow", false, "Keep streaming as new events land (§8.2)")
	}

	cmd.RunE = func(cmd *cobra.Command, argv []string) error {
		args := map[string]any{}
		for i, a := range positional {
			if i < len(argv) {
				args[a.Name] = argv[i]
			} else if a.Required {
				return codes.Errorf(codes.Usage, "%s needs a %s", name, a.Name)
			}
		}
		for n, p := range strs {
			if cmd.Flags().Changed(n) {
				args[n] = *p
			}
		}
		for n, p := range ints {
			if cmd.Flags().Changed(n) {
				args[n] = *p
			}
		}
		for n, p := range bools {
			if cmd.Flags().Changed(n) {
				args[n] = *p
			}
		}
		for n, p := range lists {
			if cmd.Flags().Changed(n) {
				args[n] = *p
			}
		}
		req, err := request(v.Name, args)
		if err != nil {
			return err
		}
		if v.Name == "events" && g.follow {
			return runStream(v, req)
		}
		return run(v, req)
	}
	return cmd
}

// request fills in everything the door derives rather than the caller
// declaring it: the project (§4.2) and the principal (§3.2).
func request(verb string, args map[string]any) (protocol.Request, error) {
	cwd, _ := os.Getwd()
	proj, err := project.Resolve(project.Options{Explicit: g.projectPath, Cwd: cwd})
	if err != nil {
		return protocol.Request{}, codes.Errorf(codes.Usage, "cannot resolve the project: %v", err)
	}
	return protocol.Request{
		Verb:          verb,
		Project:       proj,
		AllProjects:   g.allProjects,
		PaneID:        os.Getenv("HERDR_PANE_ID"),
		TabID:         os.Getenv("HERDR_TAB_ID"),
		WorkspaceID:   os.Getenv("HERDR_WORKSPACE_ID"),
		As:            g.as,
		BaseUpdatedAt: g.baseUpdatedAt,
		Follow:        g.follow,
		Args:          args,
	}, nil
}

func run(v verbs.Verb, req protocol.Request) error {
	raw, err := client.Call(req)
	if err != nil {
		return err
	}
	if g.jsonOut {
		os.Stdout.Write(compact(raw))
		fmt.Fprintln(os.Stdout)
		return nil
	}
	return renderHuman(v, raw)
}

func runStream(v verbs.Verb, req protocol.Request) error {
	return client.Stream(req, func(raw json.RawMessage) error {
		if g.jsonOut {
			os.Stdout.Write(compact(raw))
			fmt.Fprintln(os.Stdout)
			return nil
		}
		return renderEvent(raw)
	})
}

// printErrorEnvelope writes the §6.2 error document on stdout. It is called
// from ONE place, main, so that every failure — the daemon's, the door's, and
// cobra's — answers in the same shape on the same stream. Before this, only
// the failures that reached the daemon did; the rest exited with the right
// status and an empty stdout, and a machine caller could not tell which it
// was going to get.
func printErrorEnvelope(err error, code string) {
	body := map[string]any{"code": code, "message": err.Error()}
	if f, ok := err.(*client.Failure); ok {
		body["code"], body["message"] = f.Body.Code, f.Body.Message
		if f.Body.ParkedID != "" {
			body["parked_id"] = f.Body.ParkedID
		}
	} else if ce, ok := err.(*codes.Error); ok {
		body["code"], body["message"] = ce.Code, ce.Message
	}
	out, _ := json.Marshal(map[string]any{"error": body})
	fmt.Fprintln(os.Stdout, string(out))
}

// compact keeps §6.2's "exactly one JSON document" literally one line.
func compact(raw json.RawMessage) []byte {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return raw
	}
	return out.Bytes()
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the plugin version and the contract it satisfies",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.jsonOut {
				out, _ := json.Marshal(map[string]string{
					"version": daemon.Version, "contract": daemon.ContractVersion, "plugin": "herdr-tasks",
				})
				fmt.Println(string(out))
				return nil
			}
			fmt.Printf("ht %s (herdr-tasks), shared plugin contract %s\n", daemon.Version, daemon.ContractVersion)
			return nil
		},
	}
}
