package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/husniadil/herdr-tasks/internal/client"
	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/config"
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

func newRootCmd() *cobra.Command { return newRootCmdFrom(verbs.All) }

// newRootCmdFrom assembles the CLI from an arbitrary verb table. newRootCmd
// passes the real registry; a test passes a table with a collision in it,
// which is the only way to reach the refusal below without editing the
// registry itself.
func newRootCmdFrom(list []verbs.Verb) *cobra.Command {
	root := &cobra.Command{
		Use:   "htask",
		Short: "A task backlog and notes board for agents running on Herdr",
		Long: "herdr-tasks: tasks move todo → doing → review → done with claims, leases,\n" +
			"evidence and review; notes are pre-decision ideas whose promotion is the\n" +
			"operator's authority, which an agent exercises after confirming with them.\n" +
			"Conforms to the shared plugin contract.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&g.jsonOut, "json", false, "Print one JSON document on stdout (§6.2)")
	root.PersistentFlags().StringVar(&g.projectPath, "project", "", "The project to act in; defaults to this directory's repository (§4.2)")
	root.PersistentFlags().BoolVar(&g.allProjects, "all-projects", false, "Act across every project rather than this one")
	root.PersistentFlags().StringVar(&g.as, "as", "", "Act as a cron, trigger or plugin principal (§3.2)")

	groups := map[string]*cobra.Command{}
	taken := map[string]string{"daemon": "system", "mcp": "system", "tui": "system", "version": "system"}
	for _, v := range list {
		cmd := buildVerb(v)
		if len(v.CLI) == 1 {
			// §6.1 gives every verb one name on this surface, so a second
			// verb claiming a name already taken is a CLI where one of the
			// two is unreachable. cobra takes both and answers with the
			// first, silently. Refusing here, at startup, is the operator's
			// decision in task 92: no collision exists today, and one that
			// appears must stop the binary rather than shadow a verb.
			if owner, clash := taken[v.CLI[0]]; clash {
				panic("htask: `" + v.CLI[0] + "` is claimed twice on the CLI, by " + owner +
					" and by " + v.Name + "; the task verbs are flat (task 92) and a collision has no winner")
			}
			taken[v.CLI[0]] = v.Name
			root.AddCommand(cmd)
			continue
		}
		parent, ok := groups[v.CLI[0]]
		if !ok {
			// A grouping command has to be Runnable AND refuse arguments.
			// cobra returns help for a command that is not Runnable before it
			// ever validates arguments (command.go:955 precedes :968), so
			// NoArgs alone is unreachable on a parent with no Run: a stray
			// argument reads as "no subcommand given", prints help on stdout
			// and exits 0, where §6.2 and §6.3 promise one document and a
			// failure code. RunE makes the parent Runnable so NoArgs is
			// reached, and NoArgs turns the stray argument into the parse
			// error the door already renders as a USAGE envelope. Zero
			// arguments is not a stray argument, so `htask note` still
			// answers with its help.
			parent = &cobra.Command{
				Use: v.CLI[0], Short: groupShort(v.CLI[0]), Args: cobra.NoArgs,
				RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
				// Being Runnable makes cobra add `htask note [flags]` to the
				// usage block, which is an artifact of the mechanism rather
				// than a fact about the command: a group takes no flags of its
				// own. Suppressed, so the help a reader already knows comes
				// back byte for byte.
				DisableFlagsInUseLine: true,
			}
			if owner, clash := taken[v.CLI[0]]; clash {
				panic("htask: `" + v.CLI[0] + "` is claimed twice on the CLI, by " + owner +
					" and by the " + v.CLI[0] + " group; a collision has no winner")
			}
			taken[v.CLI[0]] = v.CLI[0] + " group"
			groups[v.CLI[0]] = parent
			root.AddCommand(parent)
		}
		parent.AddCommand(cmd)
	}
	root.AddCommand(newTaskAliasCmd(list))
	root.AddCommand(newDaemonCmd(), newMCPCmd(), newTUICmd(), newVersionCmd())
	return root
}

// newTaskAliasCmd rebuilds the task verbs under their old `htask task <verb>`
// path for one transition window. The sibling adapters — herdr-dispatch's
// internal/htask and herdr-sched's action adapter — and an operator's muscle
// memory both still spell it that way, and breaking them in the same commit
// that moves the verbs would make one change into three repos' worth of
// outage. The whole group is hidden: it answers, and --help teaches only the
// flat form, so nothing new learns the old one. Removing it is a follow-up
// task, not part of this one.
//
// Each alias is a SECOND cobra command over the same registry entry rather
// than a shared pointer: a cobra command belongs to one parent, and adding one
// command in two places gives it whichever parent was last to claim it.
func newTaskAliasCmd(list []verbs.Verb) *cobra.Command {
	group := &cobra.Command{
		Use:    "task",
		Short:  "Deprecated: the task verbs are top-level now (`htask claim 12`)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(cmd *cobra.Command, _ []string) error { return cmd.Help() },

		DisableFlagsInUseLine: true,
	}
	for _, v := range list {
		if !strings.HasPrefix(v.Name, "task.") {
			continue
		}
		alias := buildVerb(v)
		alias.Hidden = true
		group.AddCommand(alias)
	}
	return group
}

func groupShort(name string) string {
	switch name {
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
	cmd := &cobra.Command{Use: use, Short: v.Short, Long: v.Help(), Args: cobra.MaximumNArgs(len(positional))}

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
		if err := resolveTargetProject(args); err != nil {
			return err
		}
		req, err := request(v.Name, args)
		if err != nil {
			return err
		}
		if v.Name == "events" && g.follow {
			return runStream(v, req)
		}
		if v.Name == "stop" && !client.Live() {
			return reportNoDaemon()
		}
		return run(v, req)
	}
	return cmd
}

// resolveTargetProject canonicalizes --to-project here, in the door, for the
// reason --project is resolved here: a relative path is relative to the
// CALLER's working directory, and the daemon's is somewhere else entirely. The
// target must also already exist — a promote into a path that is not there is
// a typo, and inventing a board nobody will find fails quietly (§4.2).
func resolveTargetProject(args map[string]any) error {
	raw, ok := args["to-project"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	info, err := os.Stat(raw)
	if err != nil || !info.IsDir() {
		return codes.Errorf(codes.Usage, "cannot resolve the target project %q: not a directory", raw)
	}
	cwd, _ := os.Getwd()
	proj, err := project.Resolve(project.Options{Explicit: raw, Cwd: cwd})
	if err != nil {
		return codes.Errorf(codes.Usage, "cannot resolve the target project %q: %v", raw, err)
	}
	args["to-project"] = proj
	return nil
}

// request fills in everything the door derives rather than the caller
// declaring it: the project (§4.2) and the principal (§3.2).
func request(verb string, args map[string]any) (protocol.Request, error) {
	cwd, _ := os.Getwd()
	proj, err := project.Resolve(project.Options{Explicit: g.projectPath, Cwd: cwd, Warn: os.Stderr})
	if err != nil {
		return protocol.Request{}, codes.Errorf(codes.Usage, "cannot resolve the project: %v", err)
	}
	return protocol.Request{
		Verb:        verb,
		Project:     proj,
		AllProjects: g.allProjects,
		PaneID:      os.Getenv("HERDR_PANE_ID"),
		TabID:       os.Getenv("HERDR_TAB_ID"),
		WorkspaceID: os.Getenv("HERDR_WORKSPACE_ID"),
		As:          g.as,
		// §3.7: a CLI invocation is one process per call, so this argv IS the
		// caller's own act. That is what lets a paneless CLI call be `human`
		// where a paneless server door cannot be.
		Operator:      true,
		BaseUpdatedAt: g.baseUpdatedAt,
		Follow:        g.follow,
		Args:          args,
	}, nil
}

// reportNoDaemon answers `htask stop` when nothing is listening. `stop` asks
// for a state rather than for work, and that state already holds, so this
// exits 0: scripts/stop.sh is run on a machine where the daemon may or may
// not be up, and a non-zero exit there would be a failure report for the
// outcome the caller wanted. It is the door's answer because there is no
// daemon to ask; the MCP door, which has no exit status to carry the
// difference, is told UNAVAILABLE by the client instead.
func reportNoDaemon() error {
	if g.jsonOut {
		out, _ := json.Marshal(daemon.StopResult{Socket: config.SocketPath()})
		fmt.Println(string(out))
		return nil
	}
	fmt.Println("no daemon was running")
	return nil
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
	return renderHuman(v, raw, time.Now().UnixMilli())
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
			fmt.Printf("htask %s (herdr-tasks), shared plugin contract %s\n", daemon.Version, daemon.ContractVersion)
			return nil
		},
	}
}
