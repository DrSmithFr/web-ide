package power

import (
	"strings"
	"testing"
	"time"
)

// playWSL makes the test run as a pod under WSL whose Windows answers with answer.
func playWSL(t *testing.T, answer func(script string) ([]byte, error)) {
	wsl, ps := inWSL, powershell
	inWSL, powershell = func() bool { return true }, answer
	winMu.Lock()
	winRead = time.Time{}
	winMu.Unlock()
	t.Cleanup(func() {
		inWSL, powershell = wsl, ps
		winMu.Lock()
		winRead = time.Time{}
		winMu.Unlock()
	})
}

// TestParseWindowsNets: the LAN addresses of Windows are kept with their mask, the loopback, the
// link-local ones and the noise are not.
func TestParseWindowsNets(t *testing.T) {
	got := parseWindowsNets("192.168.1.20/24\r\n127.0.0.1/8\r\n169.254.3.4/16\r\nfe80::1/64\r\n\r\n172.29.144.1/20\r\n")
	if len(got) != 2 || got[0].String() != "192.168.1.20/24" || got[1].String() != "172.29.144.1/20" {
		t.Fatalf("nets: %v", got)
	}
}

// TestWSLReachable: under WSL, a card on the LAN of Windows is reachable, and the pod publishes no
// card of its virtual machine.
func TestWSLReachable(t *testing.T) {
	calls := 0
	playWSL(t, func(script string) ([]byte, error) {
		calls++
		if script != windowsNetsScript {
			t.Fatalf("script: %s", script)
		}
		return []byte("192.168.1.20/24\r\n"), nil
	})
	got := Reachable([]Target{{MAC: "a", Net: "192.168.1.0/24"}, {MAC: "b", Net: "10.9.0.0/16"}})
	if len(got) != 1 || got[0].MAC != "a" {
		t.Fatalf("reachable: %+v", got)
	}
	Reachable([]Target{{MAC: "a", Net: "192.168.1.0/24"}})
	if calls != 1 {
		t.Fatalf("Windows is asked once a minute: %d", calls)
	}
	if ifs := Interfaces(); len(ifs) != 0 {
		t.Fatalf("no card under WSL: %+v", ifs)
	}
}

// TestWSLSend: under WSL the packet leaves through powershell.exe, to the broadcast address of the
// network and to 255.255.255.255; a bad MAC alone sends nothing.
func TestWSLSend(t *testing.T) {
	var got string
	playWSL(t, func(script string) ([]byte, error) { got = script; return nil, nil })
	if err := Send([]Target{{MAC: "02:00:00:00:00:0b", Net: "192.168.1.0/24"}}); err != nil {
		t.Fatal(err)
	}
	want := "$h = 'ffffffffffff" + strings.Repeat("02000000000b", 16) + "'"
	for _, w := range []string{want, "EnableBroadcast = $true", "'192.168.1.255', 9)", "'255.255.255.255', 9)", "$c.Close()"} {
		if !strings.Contains(got, w) {
			t.Fatalf("script without %q: %s", w, got)
		}
	}
	got = ""
	if err := Send([]Target{{MAC: "bad", Net: "192.168.1.0/24"}}); err == nil || got != "" {
		t.Fatalf("a bad MAC: %v, %q", err, got)
	}
}
