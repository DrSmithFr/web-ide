package power

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/update"
)

// Answer is what the pod says back to the one that carried the request: the answer of an update,
// with sent for a wake.
type Answer = update.Answer

// What a power request is answered: running (the action starts in a second), sent (the magic
// packet left), not_service (a pod started by hand cannot restart itself), refused (with why).
const (
	StatusRunning    = update.StatusRunning
	StatusSent       = "sent"
	StatusNotService = update.StatusNotService
	StatusRefused    = update.StatusRefused
)

// PowerCmdEnv replaces the command of an action in the browser tests (WEBIDE_POWER_ACTION names
// it), and AllowedEnv stands for the polkit rule.
const (
	PowerCmdEnv = "WEBIDE_POWER_CMD"
	ActionEnv   = "WEBIDE_POWER_ACTION"
	AllowedEnv  = "WEBIDE_POWER_ALLOWED"
)

// Allowed reports whether this machine lets the pod power it off and reboot it: the rule of
// `web-ide-pod allow-shutdown` is installed.
func Allowed() bool {
	if os.Getenv(AllowedEnv) == "1" {
		return true
	}
	_, err := os.Stat(RulePath)
	return err == nil
}

// Handler runs the power requests that reach one pod.
type Handler struct {
	Identity *identity.PodIdentity
	Own      string

	// The machine, as fields so the tests need neither systemd nor a network card: whether the pod
	// runs as the service, whether the rule is installed, how a command starts detached, the cards
	// of another pod, and how a packet leaves.
	Service func() bool
	Allowed func() bool
	Start   func(cmd, env []string) error
	Targets func(pod string) []Target
	Send    func([]Target) error
	// Delay leaves the answer the time to reach the page before the machine goes down.
	Delay time.Duration

	mu   sync.Mutex
	seen map[string]bool
}

// New makes the handler of a pod; targets gives the cards another pod of the account published.
func New(id *identity.PodIdentity, targets func(pod string) []Target) *Handler {
	return &Handler{Identity: id, Own: id.ID(), Service: update.AsService, Allowed: Allowed, Start: detached,
		Targets: targets, Send: Send, Delay: time.Second, seen: map[string]bool{}}
}

// CanWake reports whether this pod shares a network with a card of the pod: the only case where
// its packet can reach it.
func (h *Handler) CanWake(pod string) bool {
	return h.Targets != nil && len(Reachable(h.Targets(pod))) > 0
}

// Run checks a request and acts on it. A wake is for another pod, which sleeps: this pod sends the
// packet. Every other action is for this pod, and starts after the answer went out.
func (h *Handler) Run(req *identity.PowerRequest) Answer {
	account, _, revoked := h.Identity.State()
	if err := identity.CheckPower(req, account, revoked, time.Now()); err != nil {
		return refused(err)
	}
	if req.Action == identity.PowerWake {
		if req.Pod == h.Own {
			return refused(i18n.New("this pod is awake"))
		}
	} else if req.Pod != h.Own {
		return refused(i18n.New("the request is not for this pod"))
	}
	h.mu.Lock()
	if h.seen[req.Sig] {
		h.mu.Unlock()
		return refused(i18n.New("this request was already run"))
	}
	h.seen[req.Sig] = true
	h.mu.Unlock()

	if req.Action == identity.PowerWake {
		return h.wake(req.Pod)
	}
	if (req.Action == identity.PowerShutdown || req.Action == identity.PowerReboot) && !h.Allowed() {
		a := refused(i18n.New("this machine does not let the pod power it off: run the command on it"))
		a.Command = AllowCommand()
		return a
	}
	cmd := h.command(req.Action)
	if cmd == nil {
		return Answer{Status: StatusNotService, Message: i18n.Translate("en", i18n.New("this pod does not run as the service"))}
	}
	env := []string{ActionEnv + "=" + req.Action}
	time.AfterFunc(h.Delay, func() {
		if err := h.Start(cmd, env); err != nil {
			log.Printf("power: %s: %v", req.Action, err)
		}
	})
	return Answer{Status: StatusRunning}
}

func (h *Handler) wake(pod string) Answer {
	var targets []Target
	if h.Targets != nil {
		targets = Reachable(h.Targets(pod))
	}
	if len(targets) == 0 {
		return refused(i18n.New("this pod is not on the network of that machine"))
	}
	if err := h.Send(targets); err != nil {
		return refused(i18n.Errorf("the magic packet could not be sent: %w", err))
	}
	return Answer{Status: StatusSent}
}

// command is what runs an action: nil for a restart of a pod that is not the service. The restart
// goes through systemd-run, in a unit of its own, so the restart of the pod does not kill the
// command that asked for it.
func (h *Handler) command(action string) []string {
	if hook := os.Getenv(PowerCmdEnv); hook != "" {
		return []string{"/bin/sh", "-c", hook}
	}
	switch action {
	case identity.PowerShutdown:
		return []string{"systemctl", "poweroff", "-i"}
	case identity.PowerReboot:
		return []string{"systemctl", "reboot", "-i"}
	}
	if !h.Service() {
		return nil
	}
	return []string{"systemd-run", "--user", "--unit=web-ide-restart-" + update.Suffix(), "systemctl", "--user", "restart", "web-ide-pod.service"}
}

func refused(err error) Answer {
	return Answer{Status: StatusRefused, Message: i18n.Translate("en", err)}
}

// detached is update.Detached with the variables of the action.
func detached(cmd, env []string) error {
	return update.DetachedEnv(cmd, env)
}
