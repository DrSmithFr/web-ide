package power

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Under WSL 2 the pod runs in a virtual machine: in the default NAT mode its networks are not the
// ones of the LAN, and a broadcast never leaves the virtual switch (in mirrored mode it is not sure
// to either). Windows is on the LAN, so the pod reads the networks of Windows and sends the magic
// packet through powershell.exe (the interop of WSL).

// inWSL reports whether the pod runs under WSL; a variable so the tests play Windows.
var inWSL = func() bool {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// powershell runs a script in Windows PowerShell; a variable so the tests need no Windows.
var powershell = func(script string) ([]byte, error) {
	exe, err := powershellPath()
	if err != nil {
		return nil, err
	}
	return exec.Command(exe, "-NoProfile", "-NonInteractive", "-Command", script).Output()
}

// powershellPath finds powershell.exe: in the PATH of a login shell, else at its place on the C:
// drive (a systemd unit does not get the Windows PATH).
func powershellPath() (string, error) {
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p, nil
	}
	const p = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
	if _, err := os.Stat(p); err != nil {
		return "", errors.New("powershell.exe not found: is the WSL interop on?")
	}
	return p, nil
}

// windowsNetsScript lists the IPv4 addresses of Windows as address/prefix, one per line.
const windowsNetsScript = `Get-NetIPAddress -AddressFamily IPv4 | ForEach-Object { $_.IPAddress + '/' + $_.PrefixLength }`

// The networks of Windows, read once a minute at most: powershell.exe takes a second to start.
var (
	winMu   sync.Mutex
	winNets []*net.IPNet
	winRead time.Time
)

func windowsNets() []*net.IPNet {
	winMu.Lock()
	defer winMu.Unlock()
	if time.Since(winRead) < time.Minute {
		return winNets
	}
	winRead = time.Now()
	out, err := powershell(windowsNetsScript)
	if err != nil {
		winNets = nil
		return nil
	}
	winNets = parseWindowsNets(string(out))
	return winNets
}

// parseWindowsNets reads the lines of windowsNetsScript, the loopback and link-local ones left out.
func parseWindowsNets(s string) []*net.IPNet {
	var out []*net.IPNet
	for _, l := range strings.Split(s, "\n") {
		ip, n, err := net.ParseCIDR(strings.TrimSpace(l))
		if err != nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, &net.IPNet{IP: ip.To4(), Mask: n.Mask})
	}
	return out
}

// sendScript is the PowerShell that sends each packet to the broadcast address of its network and
// to 255.255.255.255.
func sendScript(targets []Target) (string, error) {
	var b strings.Builder
	b.WriteString("$c = New-Object System.Net.Sockets.UdpClient; $c.EnableBroadcast = $true; ")
	sends := 0
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
		fmt.Fprintf(&b, "$h = '%s'; $p = New-Object byte[] %d; ", hex.EncodeToString(pkt), len(pkt))
		b.WriteString("for ($i = 0; $i -lt $p.Length; $i++) { $p[$i] = [Convert]::ToByte($h.Substring(2 * $i, 2), 16) }; ")
		for _, to := range []net.IP{broadcast(n), net.IPv4bcast} {
			fmt.Fprintf(&b, "[void] $c.Send($p, $p.Length, '%s', %d); ", to, port())
			sends++
		}
	}
	if sends == 0 {
		if last == nil {
			last = errors.New("no card to wake")
		}
		return "", last
	}
	b.WriteString("$c.Close()")
	return b.String(), nil
}

// sendWindows is Send under WSL: Windows sends the packets.
func sendWindows(targets []Target) error {
	script, err := sendScript(targets)
	if err != nil {
		return err
	}
	if out, err := powershell(script); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return fmt.Errorf("powershell.exe: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return fmt.Errorf("powershell.exe: %w %s", err, out)
	}
	return nil
}
