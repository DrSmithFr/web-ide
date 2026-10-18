// Package update is what a pod does when a device of its account asks it to update itself
// (relay.md, *Multi-pod*): it checks the signed request it was sent, then runs the installer
// detached, so the restart of the pod it triggers does not kill the install.
package update

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// What the pod answers to the one that carried the request. The page shows the command for
// not_service (the name of the wire, from before the units): a pod that does not run as its
// systemd unit cannot restart itself.
const (
	StatusRunning    = "running"
	StatusNotService = "not_service"
	StatusRefused    = "refused"
)

// UpdateCmdEnv replaces the install command: the hook of the browser tests, which has to restart a
// pod that does not run as a unit.
const UpdateCmdEnv = "WEBIDE_UPDATE_CMD"

// InstalledScript is the copy of scripts/install.sh the installer leaves next to the binary it
// installed: the pod runs that one, the file the user already ran once.
func InstalledScript() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "lib", "web-ide", "install.sh")
}

// Answer is what the pod says back, before and after it restarts.
type Answer struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	// Command is what the user runs by hand when this pod cannot run it itself.
	Command string `json:"command,omitempty"`
}

// Handler runs the update requests that reach one pod.
type Handler struct {
	Identity *identity.PodIdentity
	Own      string // the device key of this pod: a request must name this pod

	// Unit reports whether this pod runs as its systemd user unit, the only case where it can
	// restart itself. Start runs the command detached. Both are fields so the tests do not need
	// systemd, and a pod that cannot run the command says so instead of failing quietly.
	Unit  func() bool
	Start func(cmd []string) error
	// Script is the installer this pod may run (InstalledScript by default).
	Script string

	mu   sync.Mutex
	seen map[string]bool // the signatures already run: one request, one update
}

// New makes the handler of a pod.
func New(id *identity.PodIdentity) *Handler {
	return &Handler{Identity: id, Own: id.ID(), Unit: AsUnit, Start: Detached, Script: InstalledScript(), seen: map[string]bool{}}
}

// Run checks a request and, when the pod can trust it, starts the install. The answer goes out
// before the pod restarts, so the page knows what happened and waits for the pod to come back.
func (h *Handler) Run(req *identity.UpdateRequest) Answer {
	account, _, revoked := h.Identity.State()
	if err := identity.CheckUpdate(req, account, revoked, time.Now()); err != nil {
		return refused(err)
	}
	if req.Pod != h.Own {
		return refused(i18n.New("the request is not for this pod"))
	}
	if !identity.IsReleaseTag(req.Version) {
		return refused(i18n.Errorf("%s is not a release tag", req.Version))
	}
	h.mu.Lock()
	if h.seen[req.Sig] {
		h.mu.Unlock()
		return refused(i18n.New("this request was already run"))
	}
	h.seen[req.Sig] = true
	h.mu.Unlock()

	cmd, err := h.command(req.Version)
	if err != nil {
		return Answer{Status: StatusNotService, Message: i18n.Translate("en", err), Command: Command(req.Version)}
	}
	if err := h.Start(cmd); err != nil {
		return refused(i18n.Errorf("the update could not start: %w", err))
	}
	return Answer{Status: StatusRunning}
}

// refused is the answer of a request the pod does not trust: the reason reaches the page.
func refused(err error) Answer {
	return Answer{Status: StatusRefused, Message: i18n.Translate("en", err)}
}

// Command is what the user runs by hand, from the repository or from a release: what the page
// shows when this pod cannot run the update itself, and what a pod older than #60 never answered.
func Command(version string) string {
	return "scripts/install.sh " + version
}

// command builds the command that installs the tag. As the unit it goes through systemd-run, in
// a unit of its own: the installer restarts web-ide-pod, and a child of the pod would be killed by
// that restart before it finished.
func (h *Handler) command(version string) ([]string, error) {
	if hook := os.Getenv(UpdateCmdEnv); hook != "" {
		return []string{"/bin/sh", "-c", hook}, nil
	}
	if !h.Unit() {
		return nil, i18n.New("this pod does not run as its systemd unit")
	}
	script := h.Script
	if script == "" {
		script = InstalledScript()
	}
	if _, err := os.Stat(script); err != nil {
		return nil, i18n.Errorf("the installer is not installed: %s", script)
	}
	return []string{"systemd-run", "--user", "--unit=web-ide-update-" + Suffix(), script, version}, nil
}

// Suffix keeps each transient unit of an update (or of a restart) under its own name: a unit that
// already ran is not reused, and two of them do not fight for one unit.
func Suffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().Unix())
	}
	return hex.EncodeToString(b)
}

// AsUnit reports whether this process is the web-ide-pod systemd user unit: systemd sets
// INVOCATION_ID for every unit, and the cgroup names the unit. A pod started by hand, in a terminal
// or by the browser tests, is not.
func AsUnit() bool {
	if os.Getenv("INVOCATION_ID") == "" {
		return false
	}
	b, err := os.ReadFile("/proc/self/cgroup")
	return err == nil && strings.Contains(string(b), "web-ide-pod.service")
}

// Detached runs a command outside this pod's process group (setsid), so the pod can stop while the
// install still runs. The command is not waited for: the pod restarts, and the answer already went
// out.
func Detached(cmd []string) error { return DetachedEnv(cmd, nil) }

// DetachedEnv is Detached with variables added to the environment of the command.
func DetachedEnv(cmd, env []string) error {
	if len(cmd) == 0 {
		return i18n.New("no command")
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	if len(env) > 0 {
		c.Env = append(os.Environ(), env...)
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Start()
}
