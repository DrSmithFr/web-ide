// Package power is what a pod does with its machine for a device of its account (relay.md,
// *Power*): it lists the network cards a magic packet can wake, sends that packet for a machine
// that sleeps on its network, and shuts down, reboots or restarts its own unit once a signed
// request asks it to.
package power

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
)

// Target is one card of a machine that a magic packet can wake: its MAC address and the IPv4
// network it sits on (CIDR), which tells an awake pod whether it shares that network.
type Target struct {
	MAC string `json:"mac"`
	Net string `json:"net"`
}

// WakeIfacesEnv stands for the cards of this machine in the browser tests: mac@cidr, comma
// separated. WakePortEnv stands for port 9, so the suite listens on a port of its own.
const (
	WakeIfacesEnv = "WEBIDE_WAKE_IFACES"
	WakePortEnv   = "WEBIDE_WAKE_PORT"
)

// skipped are the virtual cards no magic packet reaches: containers, bridges, VPNs.
var skipped = []string{"docker", "br-", "veth", "virbr", "tailscale", "tun", "wg", "zt"}

// Interfaces lists the cards of this machine that a magic packet could wake: up, not the loopback,
// with a MAC, not a virtual one, with an IPv4 network. Under WSL there is none: the cards the pod
// sees are the ones of its virtual machine.
func Interfaces() []Target {
	if env := os.Getenv(WakeIfacesEnv); env != "" {
		return parseTargets(env)
	}
	if inWSL() {
		return []Target{}
	}
	ifs, err := netInterfaces()
	if err != nil {
		return nil
	}
	out := []Target{}
	for _, i := range ifs {
		if !wakeable(i) {
			continue
		}
		if n := ipv4Net(i); n != "" {
			out = append(out, Target{MAC: i.HardwareAddr.String(), Net: n})
		}
	}
	return out
}

var netInterfaces = net.Interfaces

// wakeable is the filter of Interfaces, on what the interface says of itself.
func wakeable(i net.Interface) bool {
	if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || len(i.HardwareAddr) != 6 {
		return false
	}
	for _, p := range skipped {
		if strings.HasPrefix(i.Name, p) {
			return false
		}
	}
	return true
}

// ipv4Net is the first IPv4 network of an interface, as a CIDR of the network (not of the host).
func ipv4Net(i net.Interface) string {
	addrs, err := i.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return (&net.IPNet{IP: n.IP.Mask(n.Mask).To4(), Mask: n.Mask}).String()
		}
	}
	return ""
}

func parseTargets(s string) []Target {
	out := []Target{}
	for _, e := range strings.Split(s, ",") {
		mac, cidr, ok := strings.Cut(strings.TrimSpace(e), "@")
		if ok {
			out = append(out, Target{MAC: mac, Net: cidr})
		}
	}
	return out
}

// MagicPacket is the frame that wakes a card: six 0xFF, then sixteen times its MAC address.
func MagicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return nil, errors.New("not a MAC address: " + mac)
	}
	p := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		p = append(p, 0xff)
	}
	for i := 0; i < 16; i++ {
		p = append(p, hw...)
	}
	return p, nil
}

// localNets are the IPv4 networks of this machine, the loopback included: the networks it can send
// a broadcast on. Under WSL, the ones of Windows too.
func localNets() []*net.IPNet {
	var out []*net.IPNet
	if inWSL() {
		out = windowsNets()
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out = append(out, n)
			}
		}
	}
	return out
}

// Reachable keeps the targets on a network of this machine: the ones its packet can reach.
func Reachable(targets []Target) []Target {
	return reachableFrom(targets, localNets())
}

func reachableFrom(targets []Target, nets []*net.IPNet) []Target {
	out := []Target{}
	for _, t := range targets {
		_, tn, err := net.ParseCIDR(t.Net)
		if err != nil {
			continue
		}
		for _, n := range nets {
			if tn.Contains(n.IP) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// broadcast is the broadcast address of a network.
func broadcast(n *net.IPNet) net.IP {
	ip := n.IP.Mask(n.Mask).To4()
	b := make(net.IP, 4)
	for i := range b {
		b[i] = ip[i] | ^n.Mask[len(n.Mask)-4+i]
	}
	return b
}

// port is port 9 (discard), the one Wake-on-LAN tools use, unless a test names another one.
func port() int {
	if p, err := strconv.Atoi(os.Getenv(WakePortEnv)); err == nil && p > 0 {
		return p
	}
	return 9
}

// Send sends the magic packet of each target to the broadcast address of its network and to
// 255.255.255.255. It reports an error when no packet left at all. Under WSL, Windows sends them.
func Send(targets []Target) error {
	if inWSL() {
		return sendWindows(targets)
	}
	sent := 0
	var last error
	for _, t := range targets {
		pkt, err := MagicPacket(t.MAC)
		if err != nil {
			last = err
			continue
		}
		_, n, err := net.ParseCIDR(t.Net)
		if err != nil {
			last = err
			continue
		}
		for _, to := range []net.IP{broadcast(n), net.IPv4bcast} {
			c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: to, Port: port()})
			if err != nil {
				last = err
				continue
			}
			if _, err := c.Write(pkt); err != nil {
				last = err
			} else {
				sent++
			}
			c.Close()
		}
	}
	if sent == 0 {
		if last == nil {
			last = errors.New("no card to wake")
		}
		return last
	}
	return nil
}
