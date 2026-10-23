//go:build linux

package confine

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// seccomp closes what Landlock does not see: the sockets other than TCP (UDP, unix, netlink,
// raw), and the calls that reach out of the process (ptrace, mounts, namespaces, modules, keys,
// io_uring, which runs calls past the filter). A deny list: Landlock is the main fence.

// denied answer EPERM.
var denied = []uintptr{
	unix.SYS_PTRACE, unix.SYS_PROCESS_VM_READV, unix.SYS_PROCESS_VM_WRITEV,
	unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT, unix.SYS_FSOPEN, unix.SYS_FSCONFIG, unix.SYS_FSMOUNT,
	unix.SYS_FSPICK, unix.SYS_OPEN_TREE, unix.SYS_MOVE_MOUNT,
	unix.SYS_UNSHARE, unix.SYS_SETNS,
	unix.SYS_BPF, unix.SYS_PERF_EVENT_OPEN, unix.SYS_USERFAULTFD,
	unix.SYS_KEXEC_LOAD, unix.SYS_KEXEC_FILE_LOAD, unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE,
	unix.SYS_KEYCTL, unix.SYS_ADD_KEY, unix.SYS_REQUEST_KEY,
	unix.SYS_IO_URING_SETUP, unix.SYS_IO_URING_ENTER, unix.SYS_IO_URING_REGISTER,
}

// cloneNS are the flags of clone that make a namespace.
const cloneNS = unix.CLONE_NEWNS | unix.CLONE_NEWCGROUP | unix.CLONE_NEWUTS | unix.CLONE_NEWIPC | unix.CLONE_NEWUSER | unix.CLONE_NEWPID | unix.CLONE_NEWNET

// The BPF of seccomp: what an instruction does.
const (
	ldAbs  = unix.BPF_LD | unix.BPF_W | unix.BPF_ABS
	jeq    = unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K
	jge    = unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K
	jset   = unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K
	ret    = unix.BPF_RET | unix.BPF_K
	allow  = unix.SECCOMP_RET_ALLOW
	kill   = unix.SECCOMP_RET_KILL_PROCESS
	eperm  = unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)
	enosys = unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)

	// The offsets of struct seccomp_data: nr, arch, then the arguments (their low 32 bits on a
	// little endian machine).
	offNR   = 0
	offArch = 4
	offArg0 = 16
)

func stmt(code uint16, k uint32) unix.SockFilter { return unix.SockFilter{Code: code, K: k} }
func jump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}

func auditArch() (uint32, error) {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, nil
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, nil
	}
	return 0, fmt.Errorf("seccomp: no filter for %s", runtime.GOARCH)
}

// program is the filter: a socket of the network only with net.
func program(net bool) ([]unix.SockFilter, error) {
	arch, err := auditArch()
	if err != nil {
		return nil, err
	}
	p := []unix.SockFilter{
		stmt(ldAbs, offArch),
		jump(jeq, arch, 1, 0),
		stmt(ret, kill),
		stmt(ldAbs, offNR),
	}
	if runtime.GOARCH == "amd64" {
		// The x32 calls (nr | 0x40000000) would pass by the numbers below.
		p = append(p, jump(jge, 0x40000000, 0, 1), stmt(ret, kill))
	}
	for _, nr := range denied {
		p = append(p, jump(jeq, uint32(nr), 0, 1), stmt(ret, eperm))
	}
	// clone3 hides its flags in memory: ENOSYS, the C library falls back to clone.
	p = append(p, jump(jeq, unix.SYS_CLONE3, 0, 1), stmt(ret, enosys))
	// clone: no new namespace.
	p = append(p, jump(jeq, unix.SYS_CLONE, 0, 4), stmt(ldAbs, offArg0), jump(jset, cloneNS, 0, 1), stmt(ret, eperm), stmt(ret, allow))
	// socket: no unix, netlink nor raw packet socket; the internet only with net.
	domains := []uint32{unix.AF_UNIX, unix.AF_NETLINK, unix.AF_PACKET}
	if !net {
		domains = append(domains, unix.AF_INET, unix.AF_INET6)
	}
	block := []unix.SockFilter{stmt(ldAbs, offArg0)}
	for _, d := range domains {
		block = append(block, jump(jeq, d, 0, 1), stmt(ret, eperm))
	}
	block = append(block, stmt(ret, allow))
	p = append(p, jump(jeq, unix.SYS_SOCKET, 0, uint8(len(block))))
	p = append(p, block...)
	return append(p, stmt(ret, allow)), nil
}

// filter installs the program on the calling thread (no new privileges first: required, and
// what it executes cannot gain any).
func filter(net bool) error {
	p, err := program(net)
	if err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %v", err)
	}
	prog := unix.SockFprog{Len: uint16(len(p)), Filter: &p[0]}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&prog)), 0, 0); err != nil {
		return fmt.Errorf("seccomp: %v", err)
	}
	return nil
}
