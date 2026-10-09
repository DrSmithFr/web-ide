// Package execx starts processes on the project target: the local machine or an SSH host.
package execx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

// Process is a piped process (language servers, CLI tools).
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Wait() error
	Kill() error
}

// PTY is an interactive terminal.
type PTY interface {
	io.ReadWriter
	Resize(cols, rows int) error
	// Wait returns the exit code.
	Wait() int
	Kill() error
}

type Runner interface {
	Start(argv []string, dir string) (Process, error)
	// StartPTY runs argv, or the user's shell when argv is empty.
	StartPTY(argv []string, dir string, cols, rows int) (PTY, error)
	Output(ctx context.Context, argv []string, dir string) ([]byte, error)
	Has(name string) bool
}

// Quote quotes a word for a POSIX shell.
func Quote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '-' || r == '_' || r == '=' || r == ':' || r == ',' || r == '+' || r == '@' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func Join(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = Quote(a)
	}
	return strings.Join(q, " ")
}

// ---------- local ----------

type Local struct{}

type localProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
}

func (p *localProc) Stdin() io.WriteCloser { return p.stdin }
func (p *localProc) Stdout() io.Reader     { return p.stdout }
func (p *localProc) Wait() error           { return p.cmd.Wait() }
func (p *localProc) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
}

func (Local) Start(argv []string, dir string) (Process, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &localProc{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

type localPTY struct {
	*os.File
	cmd  *exec.Cmd
	once sync.Once
	code int
}

func (p *localPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.File, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *localPTY) Wait() int {
	p.once.Do(func() {
		err := p.cmd.Wait()
		var ee *exec.ExitError
		switch {
		case errors.As(err, &ee):
			p.code = ee.ExitCode()
		case err != nil:
			p.code = -1
		}
	})
	return p.code
}

func (p *localPTY) Kill() error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGHUP)
	}
	return p.File.Close()
}

func shell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// TermEnv is added to the environment of the local terminals.
var TermEnv = []string{"TERM=xterm-256color", "COLORTERM=truecolor"}

// ShellArgv is what a local terminal runs: the user's shell, or argv through it.
func ShellArgv(argv []string) []string {
	if len(argv) == 0 {
		return []string{shell()}
	}
	return []string{shell(), "-lc", Join(argv)}
}

func (Local) StartPTY(argv []string, dir string, cols, rows int) (PTY, error) {
	argv = ShellArgv(argv)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), TermEnv...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &localPTY{File: f, cmd: cmd}, nil
}

func (Local) Output(ctx context.Context, argv []string, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		err = errors.New(strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

func (Local) Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ---------- SSH ----------

type SSH struct{ Client *ssh.Client }

type sshProc struct {
	s      *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
}

func (p *sshProc) Stdin() io.WriteCloser { return p.stdin }
func (p *sshProc) Stdout() io.Reader     { return p.stdout }
func (p *sshProc) Wait() error           { return p.s.Wait() }
func (p *sshProc) Kill() error {
	_ = p.s.Signal(ssh.SIGKILL)
	return p.s.Close()
}

// RemoteShell is the remote command of an SSH terminal: the login shell, in dir.
func RemoteShell(dir string) string {
	return "cd " + Quote(dir) + " 2>/dev/null; exec $SHELL -l"
}

// RemoteCmd is the remote command running argv in dir over SSH.
func RemoteCmd(argv []string, dir string) string { return remoteCmd(argv, dir) }

func remoteCmd(argv []string, dir string) string {
	cmd := "exec " + Join(argv)
	if dir != "" {
		cmd = "cd " + Quote(dir) + " && " + cmd
	}
	// A login shell loads the PATH where language servers are usually installed.
	return "$SHELL -lc " + Quote(cmd)
}

func (r SSH) Start(argv []string, dir string) (Process, error) {
	s, err := r.Client.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := s.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := s.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := s.Start(remoteCmd(argv, dir)); err != nil {
		s.Close()
		return nil, err
	}
	return &sshProc{s: s, stdin: stdin, stdout: stdout}, nil
}

type sshPTY struct {
	s    *ssh.Session
	in   io.WriteCloser
	out  io.Reader
	once sync.Once
	code int
}

func (p *sshPTY) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *sshPTY) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *sshPTY) Resize(cols, rows int) error { return p.s.WindowChange(rows, cols) }
func (p *sshPTY) Kill() error                 { return p.s.Close() }
func (p *sshPTY) Wait() int {
	p.once.Do(func() {
		err := p.s.Wait()
		var ee *ssh.ExitError
		switch {
		case errors.As(err, &ee):
			p.code = ee.ExitStatus()
		case err != nil:
			p.code = -1
		}
	})
	return p.code
}

func (r SSH) StartPTY(argv []string, dir string, cols, rows int) (PTY, error) {
	s, err := r.Client.NewSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 115200, ssh.TTY_OP_OSPEED: 115200}
	if err := s.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		s.Close()
		return nil, err
	}
	in, _ := s.StdinPipe()
	out, _ := s.StdoutPipe()
	s.Stderr = nil
	var cmd string
	if len(argv) == 0 {
		cmd = RemoteShell(dir)
	} else {
		cmd = remoteCmd(argv, dir)
	}
	if err := s.Start(cmd); err != nil {
		s.Close()
		return nil, err
	}
	return &sshPTY{s: s, in: in, out: out}, nil
}

func (r SSH) Output(ctx context.Context, argv []string, dir string) ([]byte, error) {
	s, err := r.Client.NewSession()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Signal(ssh.SIGKILL)
			s.Close()
		case <-done:
		}
	}()
	var stderr bytes.Buffer
	s.Stderr = &stderr
	out, err := s.Output(remoteCmd(argv, dir))
	if err != nil && stderr.Len() > 0 {
		err = errors.New(strings.TrimSpace(stderr.String()))
	}
	return out, err
}

func (r SSH) Has(name string) bool {
	out, err := r.Output(context.Background(), []string{"sh", "-c", "command -v " + Quote(name)}, "")
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// ExitCode returns the exit status carried by the error of Wait (local or SSH), -1 when
// the process did not exit normally, 0 without error.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee interface{ ExitCode() int } // exec.ExitError, a process of the keeper
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	var se *ssh.ExitError
	if errors.As(err, &se) {
		return se.ExitStatus()
	}
	return -1
}
