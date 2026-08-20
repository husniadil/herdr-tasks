package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// DoctorReport is §10.3's answer: version, dirs, socket liveness, Herdr
// reachability and the schema it saw, hook and gate configuration, and
// anything degraded. It never fails.
type DoctorReport struct {
	Version  string `json:"version"`
	Contract string `json:"contract"`
	// Fingerprint is the door surface this daemon speaks (§13.3). A door
	// compares it with its own: equal means the two agree about every verb
	// and argument, and anything else means restart the daemon.
	Fingerprint string `json:"fingerprint"`
	// Build is which binary this daemon is running (§13.3), which the
	// fingerprint above cannot say: a changed rule keeps the surface.
	Build         verbs.Build `json:"build"`
	Plugin        string      `json:"plugin"`
	Binary        string      `json:"binary"`
	StateDir      string      `json:"state_dir"`
	ConfigDir     string      `json:"config_dir"`
	ConfigFile    string      `json:"config_file"`
	ConfigPresent bool        `json:"config_present"`
	SocketPath    string      `json:"socket_path"`
	SocketLive    bool        `json:"socket_live"`
	// LockPath is the file whose flock elects the one daemon (§2.3). An
	// operator debugging a daemon that will not start needs to know which file
	// the other one is holding.
	LockPath        string   `json:"lock_path"`
	Project         string   `json:"project"`
	Principal       string   `json:"principal"`
	Harness         string   `json:"harness,omitempty"`
	HerdrBin        string   `json:"herdr_bin"`
	HerdrReachable  bool     `json:"herdr_reachable"`
	HerdrProtocol   int      `json:"herdr_protocol,omitempty"`
	HerdrRequests   []string `json:"herdr_requests,omitempty"`
	HerdrEvents     []string `json:"herdr_events,omitempty"`
	GateConfigured  bool     `json:"gate_configured"`
	GateCommand     []string `json:"gate_command,omitempty"`
	HookConfigured  bool     `json:"hook_configured"`
	HookCommand     []string `json:"hook_command,omitempty"`
	LeaseSeconds    int64    `json:"lease_seconds"`
	SweepSeconds    int64    `json:"sweep_seconds"`
	SchemaVersion   int64    `json:"schema_version"`
	GatedVerbs      []string `json:"gated_verbs"`
	MCPTools        []string `json:"mcp_tools"`
	Degraded        []string `json:"degraded"`
	TrustBoundary   string   `json:"trust_boundary"`
	ParkedWaiting   int      `json:"parked_waiting"`
	TasksInProject  int      `json:"tasks_in_project"`
	LeasesOutstands int      `json:"leases_outstanding"`
}

// Doctor builds the report. Every failure here is a line in Degraded, never an
// error: §10.3 says doctor never fails.
func (d *Daemon) Doctor(req protocol.Request, by tasks.Actor) DoctorReport {
	// One read of each, so the report cannot describe two different configs.
	cfg, policy := d.Cfg(), d.Policy()
	r := DoctorReport{
		Version:       Version,
		Contract:      ContractVersion,
		Fingerprint:   verbs.Fingerprint(),
		Build:         verbs.ThisBuild(),
		Plugin:        "herdr-tasks",
		StateDir:      config.StateDir(),
		ConfigDir:     config.ConfigDir(),
		ConfigFile:    cfg.Path,
		ConfigPresent: cfg.Present,
		SocketPath:    config.SocketPath(),
		LockPath:      config.LockPath(),
		Project:       req.Project,
		Principal:     string(by.Principal),
		Harness:       by.Harness,
		HerdrBin:      d.Herdr.Bin(),
		LeaseSeconds:  cfg.LeaseSeconds,
		SweepSeconds:  cfg.SweepSeconds,
		GatedVerbs:    verbs.GatedVerbs(),
		Degraded:      []string{},
		TrustBoundary: "the local user account: whoever can open the socket is trusted as the user (§3.5)",
	}
	if exe, err := os.Executable(); err == nil {
		r.Binary = exe
	}
	for _, v := range verbs.MCPTools() {
		r.MCPTools = append(r.MCPTools, v.MCP)
	}

	if conn, err := net.DialTimeout("unix", r.SocketPath, 300*time.Millisecond); err == nil {
		conn.Close()
		r.SocketLive = true
	} else {
		r.Degraded = append(r.Degraded, "no daemon is listening on "+r.SocketPath)
	}

	if sc, err := d.Herdr.Schema(); err == nil {
		r.HerdrReachable = true
		r.HerdrRequests, r.HerdrEvents, r.HerdrProtocol = sc.Requests, sc.Events, sc.Protocol
	} else {
		r.Degraded = append(r.Degraded, "herdr is not reachable through "+r.HerdrBin+": "+err.Error())
	}

	r.GateConfigured = policy.Configured()
	r.GateCommand = policy.Command()
	if !r.GateConfigured {
		r.Degraded = append(r.Degraded, "no policy gate is configured, so every gated verb is allowed (§9.2)")
	}
	r.HookConfigured = len(cfg.OnEvent) > 0
	r.HookCommand = cfg.OnEvent

	// A store an older build left behind at Herdr's injected path is named,
	// never touched: the operator decides whether it is theirs to delete.
	for _, dir := range config.OrphanStoreDirs() {
		db := filepath.Join(dir, config.Name+".db")
		if _, err := os.Stat(db); err != nil {
			continue
		}
		r.Degraded = append(r.Degraded, "a second store sits at "+db+
			", which is not the store in use ("+r.StateDir+"); "+orphanRows(db)+
			". Nothing here writes to it — remove it yourself once you are satisfied it is empty")
	}

	if err := d.Store.DB().QueryRow("SELECT schema_version FROM meta").Scan(&r.SchemaVersion); err != nil {
		r.Degraded = append(r.Degraded, "cannot read the schema version: "+err.Error())
	}
	if parked, err := d.Store.ListParked(req.Project); err == nil {
		r.ParkedWaiting = len(parked)
		if r.ParkedWaiting > 0 {
			r.Degraded = append(r.Degraded, "the policy gate parked actions the operator has not resolved")
		}
	}
	if list, err := d.Store.ListTasks(store.TaskFilter{Project: req.Project}); err == nil {
		r.TasksInProject = len(list)
		for _, t := range list {
			if t.ClaimedBy != "" {
				r.LeasesOutstands++
			}
		}
	}
	return r
}

// MarshalJSON is here so the report renders stable field order in --json
// output; the struct tags do the rest.
func (r DoctorReport) MarshalJSON() ([]byte, error) {
	type alias DoctorReport
	return json.Marshal(alias(r))
}

// orphanRows says what an abandoned store still holds, in the words doctor
// puts on the line. It never fails: doctor never fails (§10.3), and a store we
// cannot read is a thing to say, not an error to raise.
func orphanRows(path string) string {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return "it could not be opened, so what it holds is unknown"
	}
	defer db.Close()
	var tasks, notes int
	if err := db.QueryRow("SELECT (SELECT COUNT(*) FROM tasks), (SELECT COUNT(*) FROM notes)").
		Scan(&tasks, &notes); err != nil {
		return "it could not be read, so what it holds is unknown"
	}
	if tasks == 0 && notes == 0 {
		return "it holds no tasks and no notes"
	}
	return fmt.Sprintf("it holds %d task(s) and %d note(s), so read it before removing it", tasks, notes)
}
