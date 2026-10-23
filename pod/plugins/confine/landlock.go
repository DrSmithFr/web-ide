//go:build linux

package confine

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock (docs/design/plugins.md, *Confined backend*): the files a backend reaches, and TCP.
// Each right the kernel knows is handled, so what no rule grants is refused.

const (
	fsExec     = unix.LANDLOCK_ACCESS_FS_EXECUTE
	fsWrite    = unix.LANDLOCK_ACCESS_FS_WRITE_FILE
	fsRead     = unix.LANDLOCK_ACCESS_FS_READ_FILE
	fsReadDir  = unix.LANDLOCK_ACCESS_FS_READ_DIR
	fsTruncate = unix.LANDLOCK_ACCESS_FS_TRUNCATE
	fsIoctlDev = unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	fsRefer    = unix.LANDLOCK_ACCESS_FS_REFER

	// The rights of version 1, then those each version adds.
	fsV1 = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM

	netBind    = unix.LANDLOCK_ACCESS_NET_BIND_TCP
	netConnect = unix.LANDLOCK_ACCESS_NET_CONNECT_TCP

	scopeAbstract = unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET
	scopeSignal   = unix.LANDLOCK_SCOPE_SIGNAL
)

// ErrNoLandlock: the kernel has no Landlock (or it is off): a backend does not start.
var ErrNoLandlock = errors.New("this kernel has no Landlock: an external plugin's backend cannot run confined")

// ABI is the version of Landlock of the kernel (0: none).
func ABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(v)
}

// handled are the file rights of an ABI.
func handled(abi int) uint64 {
	fs := uint64(fsV1)
	if abi >= 2 {
		fs |= fsRefer
	}
	if abi >= 3 {
		fs |= fsTruncate
	}
	if abi >= 5 {
		fs |= fsIoctlDev
	}
	return fs
}

// Access of a rule: what a path beneath gives.
type Access int

const (
	// ReadExec: read the files and folders, run the programs (the plugin, the system).
	ReadExec Access = iota
	// ReadOnly: read, no program run from it (the project with files:read).
	ReadOnly
	// ReadWrite: every right (its data folder, the project with files:write).
	ReadWrite
	// Device: read and write a device file (/dev/null).
	Device
)

// Rule gives a path and what is beneath it.
type Rule struct {
	Path   string
	Access Access
}

func (r Rule) rights(abi int, dir bool) uint64 {
	var a uint64
	switch r.Access {
	case ReadExec:
		a = fsExec | fsRead | fsReadDir
	case ReadOnly:
		a = fsRead | fsReadDir
	case ReadWrite:
		a = handled(abi)
	case Device:
		a = fsRead | fsWrite
		if abi >= 3 {
			a |= fsTruncate
		}
	}
	if !dir {
		// A file takes only the rights of a file.
		a &= fsExec | fsWrite | fsRead | fsTruncate | fsIoctlDev
	}
	return a & handled(abi)
}

// restrict confines the calling thread (and what it executes next): the rules, TCP refused
// unless net (ABI 4; before it, seccomp refuses the sockets of the network), the abstract unix
// sockets and the signals out of the domain scoped (ABI 6).
func restrict(rules []Rule, net bool) error {
	abi := ABI()
	if abi < 1 {
		return ErrNoLandlock
	}
	attr := unix.LandlockRulesetAttr{Access_fs: handled(abi)}
	size := unsafe.Sizeof(attr)
	if abi >= 4 && !net {
		attr.Access_net = netBind | netConnect
	}
	if abi >= 6 {
		attr.Scoped = scopeAbstract | scopeSignal
	}
	switch {
	case abi < 4:
		size = unsafe.Offsetof(attr.Access_net)
	case abi < 6:
		size = unsafe.Offsetof(attr.Scoped)
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), size, 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %v", errno)
	}
	defer unix.Close(int(fd))
	for _, r := range rules {
		st, err := os.Stat(r.Path)
		if err != nil {
			continue // a folder this system lacks (/lib64)
		}
		pfd, err := unix.Open(r.Path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("%s: %v", r.Path, err)
		}
		pb := unix.LandlockPathBeneathAttr{Allowed_access: r.rights(abi, st.IsDir()), Parent_fd: int32(pfd)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&pb)), 0, 0, 0)
		unix.Close(pfd)
		if errno != 0 {
			return fmt.Errorf("landlock_add_rule %s: %v", r.Path, errno)
		}
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %v", errno)
	}
	return nil
}
