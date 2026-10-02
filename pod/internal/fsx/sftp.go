package fsx

import (
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SFTP is the file system of an SSH host.
type SFTP struct {
	c    *sftp.Client
	home string
}

func NewSFTP(client *ssh.Client) (*SFTP, error) {
	c, err := sftp.NewClient(client)
	if err != nil {
		return nil, err
	}
	home, err := c.Getwd()
	if err != nil {
		home = "/"
	}
	return &SFTP{c: c, home: home}, nil
}

func (s *SFTP) List(p string) ([]Entry, error) {
	fis, err := s.c.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(fis))
	for _, fi := range fis {
		full := path.Join(p, fi.Name())
		e := entryOf(full, fi)
		if e.Link {
			if target, err := s.c.Stat(full); err == nil {
				e.Dir = target.IsDir()
				e.Size = target.Size()
			}
		}
		out = append(out, e)
	}
	SortEntries(out)
	return out, nil
}

func (s *SFTP) Read(p string) ([]byte, error) {
	f, err := s.c.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (s *SFTP) Write(p string, data []byte) error {
	_ = s.c.MkdirAll(path.Dir(p))
	f, err := s.c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *SFTP) Stat(p string) (Entry, error) {
	fi, err := s.c.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	return entryOf(p, fi), nil
}

func (s *SFTP) Mkdir(p string) error { return s.c.MkdirAll(p) }

func (s *SFTP) Remove(p string) error {
	fi, err := s.c.Lstat(p)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return s.c.Remove(p)
	}
	return s.c.RemoveAll(p)
}

func (s *SFTP) Rename(from, to string) error { return s.c.PosixRename(from, to) }

func (s *SFTP) Abs(p string) (string, error) {
	switch {
	case p == "" || p == "." || p == "~":
		return s.home, nil
	case strings.HasPrefix(p, "~/"):
		return path.Join(s.home, p[2:]), nil
	case !path.IsAbs(p):
		return path.Join(s.home, p), nil
	}
	return path.Clean(p), nil
}

func (s *SFTP) NewWatcher() (Watcher, error) { return newPollWatcher(s, 2*time.Second), nil }
func (s *SFTP) Close() error                 { return s.c.Close() }
