package power

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DrSmithFr/web-ide/lib/i18n"
)

// RulePath is the polkit rule `web-ide-pod allow-shutdown` writes: the pod runs as the user, and
// logind asks polkit whether that user may power the machine off.
const RulePath = "/etc/polkit-1/rules.d/50-web-ide.rules"

// MarkerPath names the user the rule is for, readable by everyone: the folder of the rules is
// polkit's only, so the pod reads this one to know whether to offer Shut down.
const MarkerPath = "/etc/web-ide/allow-shutdown"

// actions are the logind actions the rule allows: power off and reboot, also with other sessions
// open and with an inhibitor taken (the pod runs systemctl -i: the user asked from the page).
var actions = []string{
	"org.freedesktop.login1.power-off",
	"org.freedesktop.login1.power-off-multiple-sessions",
	"org.freedesktop.login1.power-off-ignore-inhibit",
	"org.freedesktop.login1.reboot",
	"org.freedesktop.login1.reboot-multiple-sessions",
	"org.freedesktop.login1.reboot-ignore-inhibit",
}

// AllowCommand is what the user runs on a machine whose pod may not power it off: the path of this
// binary, since sudo does not search ~/.local/bin.
func AllowCommand() string {
	exe, err := os.Executable()
	if err != nil {
		exe = "web-ide-pod"
	}
	return "sudo " + exe + " allow-shutdown"
}

var userName = regexp.MustCompile(`^[a-z_][a-z0-9_.-]*\$?$`)

// Rule is the text of the rule for one user.
func Rule(user string) (string, error) {
	if !userName.MatchString(user) {
		return "", i18n.Errorf("not a user name: %q", user)
	}
	quoted := make([]string, len(actions))
	for i, a := range actions {
		quoted[i] = `"` + a + `"`
	}
	return fmt.Sprintf(`// Written by web-ide-pod allow-shutdown: the Web IDE pod of %[1]s may power this machine off
// and reboot it, when a device of its account asks (web-ide-pod allow-shutdown -remove undoes it).
polkit.addRule(function(action, subject) {
    if (subject.user == "%[1]s" && [%[2]s].indexOf(action.id) >= 0) {
        return polkit.Result.YES;
    }
});
`, user, strings.Join(quoted, ", ")), nil
}

// AllowShutdown is the subcommand: write the rule for the user (or remove it), then tell what
// Wake-on-LAN looks like on the wired cards.
func AllowShutdown(user string, remove bool, out io.Writer) error {
	if os.Geteuid() != 0 {
		return i18n.New("run it as root: sudo web-ide-pod allow-shutdown")
	}
	if remove {
		for _, p := range []string{RulePath, MarkerPath} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		fmt.Fprintf(out, "Removed %s: the pod can no longer power this machine off.\n", RulePath)
		return nil
	}
	if user == "" || user == "root" {
		return i18n.New("name the user the pod runs as: -user, or run it with sudo from that user")
	}
	rule, err := Rule(user)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(RulePath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(RulePath, []byte(rule), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(MarkerPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(MarkerPath, []byte(user+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s: the pod of %s may shut down and reboot this machine.\n", RulePath, user)
	WakeReport(out)
	return nil
}

// WakeReport tells, for each wired card, whether it wakes on a magic packet, and how to turn it on
// so that it stays on. The BIOS (or UEFI) setting is the user's.
func WakeReport(out io.Writer) {
	fmt.Fprintln(out, "\nWake-on-LAN, so that another pod of the network can wake this machine:")
	any := false
	for _, t := range Interfaces() {
		name := ifaceOf(t.MAC)
		if name == "" || isWireless(name) {
			continue
		}
		any = true
		state := "unknown (install ethtool to see it)"
		if _, err := exec.LookPath("ethtool"); err == nil {
			state = wakeState(name)
		}
		fmt.Fprintf(out, "  %s (%s, %s): %s\n", name, t.MAC, t.Net, state)
		if !strings.HasPrefix(state, "on") {
			if conn := nmConnection(name); conn != "" {
				fmt.Fprintf(out, "    turn it on: nmcli connection modify %q 802-3-ethernet.wake-on-lan magic\n", conn)
			} else {
				fmt.Fprintf(out, "    turn it on: ethtool -s %s wol g (lost at reboot without NetworkManager or a udev rule)\n", name)
			}
		}
	}
	if !any {
		fmt.Fprintln(out, "  no wired card: a Wi-Fi card rarely wakes on a packet.")
	}
	fmt.Fprintln(out, "  Wake-on-LAN must also be enabled in the BIOS (or UEFI) of the machine.")
}

func ifaceOf(mac string) string {
	ifs, _ := netInterfaces()
	for _, i := range ifs {
		if i.HardwareAddr.String() == mac {
			return i.Name
		}
	}
	return ""
}

func isWireless(name string) bool {
	_, err := os.Stat(filepath.Join("/sys/class/net", name, "wireless"))
	return err == nil
}

// wakeState reads the Wake-on line of ethtool: g is the magic packet.
func wakeState(name string) string {
	b, err := exec.Command("ethtool", name).Output()
	if err != nil {
		return "unknown (ethtool: " + err.Error() + ")"
	}
	return parseWakeOn(string(b))
}

func parseWakeOn(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "Wake-on:"); ok {
			v = strings.TrimSpace(v)
			if strings.Contains(v, "g") {
				return "on (" + v + ")"
			}
			return "off (" + v + ")"
		}
	}
	return "not supported"
}

// nmConnection is the NetworkManager connection of a device ("" without NetworkManager): the
// setting written there survives a reboot.
func nmConnection(dev string) string {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return ""
	}
	b, err := exec.Command("nmcli", "-t", "-f", "GENERAL.CONNECTION", "device", "show", dev).Output()
	if err != nil {
		return ""
	}
	_, v, _ := strings.Cut(strings.TrimSpace(string(b)), ":")
	if v == "--" {
		return ""
	}
	return v
}
