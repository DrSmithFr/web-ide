// Package paste fixes the bracketed paste markers of a terminal's input (#61): xterm.js wraps a
// paste in ESC[200~ … ESC[201~ once a program asked for it (ESC[?2004h). A program that handles
// them reads its input raw (readline, vim, Claude Code); one reading in canonical mode (cat, dash,
// a shell after a TUI left the mode on) would echo them as ^[[200~ … ~. So the write to the PTY
// decides, from the terminal's mode at that moment.
package paste

import (
	"bytes"
	"os"

	"golang.org/x/sys/unix"
)

var (
	start = []byte("\x1b[200~")
	end   = []byte("\x1b[201~")
)

// Filter is data as the program of the PTY whose master is f should read it: no marker in
// canonical mode, else the outer pair only (markers inside come from text copied in another
// terminal and would end the paste early). f nil: the mode is unknown (a remote PTY), the outer
// pair stays.
func Filter(data []byte, f *os.File) []byte {
	if !bytes.Contains(data, start) && !bytes.Contains(data, end) {
		return data
	}
	if f != nil && Canonical(f) {
		return strip(data)
	}
	if bytes.HasPrefix(data, start) && bytes.HasSuffix(data, end) && len(data) >= len(start)+len(end) {
		inner := strip(data[len(start) : len(data)-len(end)])
		out := make([]byte, 0, len(inner)+len(start)+len(end))
		return append(append(append(out, start...), inner...), end...)
	}
	return data
}

// Canonical tells whether the program of the PTY reads lines (ICANON); Linux gives the master
// the termios of the pair.
func Canonical(f *os.File) bool {
	t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil && t.Lflag&unix.ICANON != 0
}

func strip(b []byte) []byte {
	return bytes.ReplaceAll(bytes.ReplaceAll(b, start, nil), end, nil)
}
