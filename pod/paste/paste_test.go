package paste

import (
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestFilter(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skip("no pty:", err)
	}
	defer master.Close()
	defer slave.Close()

	wrapped := []byte("\x1b[200~a\nb\x1b[201~")
	if got := string(Filter([]byte("ls\r"), master)); got != "ls\r" {
		t.Fatalf("plain input changed: %q", got)
	}
	// A new pty reads lines, as cat does: no marker reaches it.
	if got := string(Filter(wrapped, master)); got != "a\nb" {
		t.Fatalf("canonical: %q", got)
	}

	// Raw, as readline: the outer pair stays, a marker inside goes.
	tio, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	tio.Lflag &^= unix.ICANON | unix.ECHO
	if err := unix.IoctlSetTermios(int(slave.Fd()), unix.TCSETS, tio); err != nil {
		t.Fatal(err)
	}
	if got := string(Filter(wrapped, master)); got != string(wrapped) {
		t.Fatalf("raw: %q", got)
	}
	if got := string(Filter([]byte("\x1b[200~x\x1b[201~y\x1b[201~"), master)); got != "\x1b[200~xy\x1b[201~" {
		t.Fatalf("raw, a marker inside: %q", got)
	}

	// A remote pty: the mode is unknown, the outer pair stays.
	if got := string(Filter(wrapped, nil)); got != string(wrapped) {
		t.Fatalf("unknown mode: %q", got)
	}
}
