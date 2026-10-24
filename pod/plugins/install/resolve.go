package install

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/plugins"
)

// Resolution like Composer: the tags of the repository of a plugin (vX.Y.Z), the highest one
// matching the constraint whose plugin.json is compatible with this IDE, its API and the
// installed plugins.

// Resolver resolves the versions of the external plugins for this pod.
type Resolver struct {
	// IDE is the version of the pod ("dev" or a git describe: its base version counts, a
	// development build being compatible with any).
	IDE string
	// Installed are the plugins of the pod and their versions (built-ins and externals).
	Installed map[string]string
	// Builtin tells whether an id is the one of a built-in plugin.
	Builtin func(id string) bool
	// AllowHTTP allows http:// to 127.0.0.1 (the browser tests serve their assets so).
	AllowHTTP bool
}

// Candidate is a version of a plugin that fits: its tag and the plugin.json at that tag.
type Candidate struct {
	URL      string
	Tag      string
	Version  Version
	Manifest plugins.Manifest
}

// gitTimeout bounds every git call: the hosts are out of our hands.
const gitTimeout = 60 * time.Second

// CheckURL accepts the address of a repository: https, ssh (ssh:// or git@host:path) or a local
// folder; http only to 127.0.0.1 in the tests.
func (r Resolver) CheckURL(raw string) error {
	switch {
	case raw == "":
		return i18n.New("the address of the repository is missing")
	case strings.HasPrefix(raw, "git@") && strings.Contains(raw, ":"):
		return nil
	case filepath.IsAbs(raw):
		if st, err := os.Stat(raw); err != nil || !st.IsDir() {
			return i18n.Errorf("%s is not a folder", raw)
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return i18n.Errorf("invalid repository address: %s", raw)
	}
	switch u.Scheme {
	case "https", "ssh":
		return nil
	case "http":
		if r.AllowHTTP && local(u.Hostname()) {
			return nil
		}
	}
	return i18n.Errorf("a repository is reached by https, ssh or a local path: %s", raw)
}

func local(host string) bool { return host == "127.0.0.1" || host == "localhost" || host == "::1" }

// git runs git without prompts (a private repository asks nothing: it fails).
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, i18n.Errorf("git %s: %s", args[0], msg)
	}
	return out.Bytes(), nil
}

// Tags are the release tags of a repository (vX.Y.Z, pre-releases too), the highest first.
func Tags(ctx context.Context, repo string) ([]Candidate, error) {
	out, err := git(ctx, "", "ls-remote", "--tags", "--refs", repo)
	if err != nil {
		return nil, err
	}
	var list []Candidate
	for _, line := range strings.Split(string(out), "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		tag, isTag := strings.CutPrefix(ref, "refs/tags/")
		if !ok || !isTag || !strings.HasPrefix(tag, "v") {
			continue
		}
		if v, err := ParseVersion(tag); err == nil {
			list = append(list, Candidate{URL: repo, Tag: tag, Version: v})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Version.Compare(list[j].Version) > 0 })
	return list, nil
}

// manifestAt reads the plugin.json of a tag (a shallow clone without checkout).
func manifestAt(ctx context.Context, repo, tag string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "web-ide-plugin-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if _, err := git(ctx, "", "clone", "--quiet", "--depth", "1", "--no-checkout", "--branch", tag, repo, dir); err != nil {
		return nil, err
	}
	data, err := git(ctx, dir, "show", "HEAD:plugin.json")
	if err != nil {
		return nil, i18n.Errorf("%s has no plugin.json at %s", repo, tag)
	}
	return data, nil
}

// Resolve picks the version of a plugin to install: the highest tag matching the constraint
// whose plugin.json fits this pod; else the reason the highest matching one does not.
func (r Resolver) Resolve(ctx context.Context, repo, constraint string) (Candidate, error) {
	if err := r.CheckURL(repo); err != nil {
		return Candidate{}, err
	}
	c, err := ParseConstraint(constraint)
	if err != nil {
		return Candidate{}, i18n.Errorf("invalid version constraint: %s", constraint)
	}
	tags, err := Tags(ctx, repo)
	if err != nil {
		return Candidate{}, err
	}
	var reason error
	for _, cand := range tags {
		if !c.Allows(cand.Version) {
			continue
		}
		data, err := manifestAt(ctx, repo, cand.Tag)
		if err == nil {
			cand.Manifest, err = r.fits(data, cand.Version)
		}
		if err == nil {
			return cand, nil
		}
		if reason == nil {
			reason = i18n.Errorf("%s: %w", cand.Tag, err)
		}
	}
	if reason != nil {
		return Candidate{}, i18n.Errorf("no version fits this IDE: %w", reason)
	}
	return Candidate{}, i18n.Errorf("no version of %s matches %s", repo, c.String())
}

// fits checks the plugin.json of a version against this pod.
func (r Resolver) fits(data []byte, v Version) (plugins.Manifest, error) {
	m, err := plugins.Parse(data)
	if err != nil {
		return m, err
	}
	if err := CheckExternal(m, r.Builtin); err != nil {
		return m, err
	}
	if mv, _ := ParseVersion(m.Version); mv.Compare(v) != 0 {
		return m, i18n.Errorf("plugin.json says version %s", m.Version)
	}
	if ide, err := ParseVersion(baseVersion(r.IDE)); err == nil && m.IDE != "" {
		if c, _ := ParseConstraint(m.IDE); !c.Allows(ide) {
			return m, i18n.Errorf("needs the IDE %s, this one is %s", m.IDE, ide.String())
		}
	}
	for id, constraint := range m.Plugins {
		have, ok := r.Installed[id]
		if !ok {
			return m, i18n.Errorf("needs the %s plugin, not installed", id)
		}
		hv, err := ParseVersion(have)
		c, _ := ParseConstraint(constraint)
		if err != nil || !c.Allows(hv) {
			return m, i18n.Errorf("needs %s %s, this pod has %s", id, constraint, have)
		}
	}
	return m, nil
}

// baseVersion is the release a build stands for: v1.23.0-51-g2370845-dirty is 1.23.0.
func baseVersion(s string) string {
	s = strings.TrimPrefix(s, "v")
	base, _, _ := strings.Cut(s, "-")
	return base
}
