//go:build !linux

// Package confine starts the backend of an external plugin confined: Linux only (Landlock).
package confine

import (
	"errors"
	"fmt"
	"os"
)

// ErrNoLandlock: no Landlock off Linux: a backend does not start.
var ErrNoLandlock = errors.New("an external plugin's backend runs confined on Linux only")

// ABI is 0: no Landlock.
func ABI() int { return 0 }

// Main refuses: there is no unconfined fallback.
func Main(args []string) {
	fmt.Fprintln(os.Stderr, "plugin-exec:", ErrNoLandlock)
	os.Exit(126)
}
