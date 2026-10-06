// Package capture takes a screenshot of a web page with a headless Chromium of the machine of
// the pod (for the pages the model draws on the board of a conversation).
package capture

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Limits of a capture.
const (
	DefaultWidth  = 1280
	DefaultHeight = 800
	MaxWidth      = 2560
	MaxHeight     = 4000
	timeout       = 20 * time.Second
)

// Chromium finds a Chromium: the system one, else the newest one of the Playwright cache
// (as the e2e tests do).
func Chromium() string {
	for _, p := range []string{"/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	var found []string
	for _, pattern := range []string{"chromium-*/chrome-linux*/chrome", "chromium_headless_shell-*/chrome-linux*/headless_shell", "chromium_headless_shell-*/chrome-*/chrome-headless-shell"} {
		m, _ := filepath.Glob(filepath.Join(home, ".cache", "ms-playwright", pattern))
		found = append(found, m...)
	}
	sort.Strings(found)
	if len(found) > 0 {
		return found[len(found)-1]
	}
	return ""
}

// URL returns the PNG of a page of width×height pixels (http and https only).
func URL(ctx context.Context, page string, width, height int) ([]byte, error) {
	u, err := url.Parse(page)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, i18n.Errorf("only http and https addresses can be captured: %s", page)
	}
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}
	width, height = min(width, MaxWidth), min(height, MaxHeight)
	chrome := Chromium()
	if chrome == "" {
		return nil, i18n.New("no Chromium found: install chromium (or the browser of the e2e tests) on the machine of the pod")
	}
	dir, err := os.MkdirTemp("", "webide-capture-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	shot := filepath.Join(dir, "shot.png")
	sandbox := os.Geteuid() != 0
	run := func(headless string) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		args := []string{headless, "--disable-gpu", "--hide-scrollbars", "--no-first-run", "--user-data-dir=" + filepath.Join(dir, "profile"),
			fmt.Sprintf("--window-size=%d,%d", width, height), "--screenshot=" + shot, "--virtual-time-budget=3000", u.String()}
		if !sandbox {
			args = append([]string{"--no-sandbox"}, args...)
		}
		out, err := exec.CommandContext(ctx, chrome, args...).CombinedOutput()
		if sandbox && strings.Contains(string(out), "No usable sandbox") {
			// Some systems forbid the sandbox of Chromium (Ubuntu 23.10+ without its AppArmor
			// profile): without it, only the pages of this machine or of the local network.
			if !local(u.Hostname()) {
				return i18n.Errorf("Chromium cannot use its sandbox on this machine: only local addresses can be captured (%s)", u.Hostname())
			}
			sandbox = false
			return errRetry
		}
		if ctx.Err() != nil {
			return i18n.Errorf("the capture of %s took more than %d s", page, int(timeout.Seconds()))
		}
		if _, serr := os.Stat(shot); err != nil || serr != nil {
			return fmt.Errorf("chromium: %v %.300s", err, out)
		}
		return nil
	}
	// The new headless mode first; older versions only know the old one.
	err = run("--headless=new")
	if err == errRetry {
		err = run("--headless=new")
	}
	if err != nil && !isI18n(err) {
		if err2 := run("--headless"); err2 == nil {
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	return os.ReadFile(shot)
}

var errRetry = fmt.Errorf("retry without the sandbox")

func isI18n(err error) bool {
	_, ok := err.(*i18n.Error)
	return ok
}

// local tells whether a host is this machine or the local network.
func local(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			return false
		}
	}
	return true
}
