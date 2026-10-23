//go:build linux

// Package confine starts the backend of an external plugin confined (docs/design/plugins.md,
// *Confined backend*): `web-ide-pod plugin-exec` sets no new privileges, Landlock (its folder
// and the system read, its data folder written, the project as granted, nothing else; TCP only
// with the network grant) and seccomp, then executes the backend, whose children inherit it all.
// A kernel without Landlock refuses: there is no unconfined fallback.
package confine

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Options of a confined start.
type Options struct {
	// Plugin is the folder of the installed version (read and run), Data its own data folder
	// (written: its state, its temporary files).
	Plugin, Data string
	// Project is the folder of the project ("" for none), Write whether it may change it (the
	// grant files:write; files:read reads it only).
	Project string
	Write   bool
	// Net: the grant net (TCP and UDP).
	Net bool
	// Read are more folders read and run (the tests: the folder of their binary).
	Read []string
	// Argv is the backend and its arguments; Env its environment.
	Argv []string
	Env  []string
}

// system are the folders a program of any language reads to start (its loader, its libraries,
// the configuration of the system); /proc/self only, the others' /proc holding their environment.
var system = []string{"/usr", "/lib", "/lib64", "/lib32", "/bin", "/sbin", "/etc", "/proc/self"}

// devices are read and written.
var devices = []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom"}

// Rules are the Landlock rules of a start.
func (o Options) Rules() []Rule {
	var rules []Rule
	for _, p := range append(append([]string{}, system...), o.Read...) {
		rules = append(rules, Rule{Path: p, Access: ReadExec})
	}
	rules = append(rules, Rule{Path: o.Plugin, Access: ReadExec}, Rule{Path: o.Data, Access: ReadWrite})
	for _, d := range devices {
		rules = append(rules, Rule{Path: d, Access: Device})
	}
	if o.Project != "" {
		a := ReadOnly
		if o.Write {
			a = ReadWrite
		}
		rules = append(rules, Rule{Path: o.Project, Access: a})
	}
	return rules
}

// Exec confines this process and executes the backend: it returns only on an error.
func Exec(o Options) error {
	if len(o.Argv) == 0 {
		return errors.New("plugin-exec: no program")
	}
	if !filepath.IsAbs(o.Argv[0]) {
		return fmt.Errorf("plugin-exec: %s is not an absolute path", o.Argv[0])
	}
	// Landlock and seccomp hold for the thread that sets them, and execve runs on it.
	runtime.LockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %v", err)
	}
	if err := restrict(o.Rules(), o.Net); err != nil {
		return err
	}
	if err := filter(o.Net); err != nil {
		return err
	}
	return syscall.Exec(o.Argv[0], o.Argv, o.Env)
}

// Main is `web-ide-pod plugin-exec [flags] -- program args…` (the pod starts the backends so).
func Main(args []string) {
	fs := flag.NewFlagSet("plugin-exec", flag.ExitOnError)
	var o Options
	var read string
	fs.StringVar(&o.Plugin, "plugin", "", "the folder of the plugin (read, run)")
	fs.StringVar(&o.Data, "data", "", "its data folder (written)")
	fs.StringVar(&o.Project, "project", "", "the folder of the project")
	fs.BoolVar(&o.Write, "write", false, "it may change the project")
	fs.BoolVar(&o.Net, "net", false, "it may reach the network")
	fs.StringVar(&read, "read", "", "more folders read and run, separated by ':'")
	_ = fs.Parse(args)
	if read != "" {
		o.Read = strings.Split(read, ":")
	}
	o.Argv = fs.Args()
	o.Env = os.Environ() // the pod gave it a clean one
	if o.Plugin == "" || o.Data == "" {
		fmt.Fprintln(os.Stderr, "plugin-exec: -plugin and -data are needed")
		os.Exit(2)
	}
	err := Exec(o)
	fmt.Fprintln(os.Stderr, "plugin-exec:", err)
	os.Exit(126)
}
