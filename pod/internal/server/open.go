package server

import (
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// /open?path=<absolute path>&line=<n> opens a file in the windows of its project: the
// links Claude Code writes and the mod of Claude Code (bearer) use it. A browser click
// gets a page saying where the file opened, or the project page when no window has it.

const openPath = "/open"

// pushOpen asks the windows of a project to open a file; it answers how many were asked.
func (s *Server) pushOpen(projectID, path string, line int) int {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if c.sees(projectID) {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.push("ide.open", map[string]any{"path": path, "line": line})
	}
	return len(targets)
}

var openPage = template.Must(template.New("open").Parse(`<!doctype html>
<meta charset="utf-8"><title>Web IDE</title>
<style>body{font:14px system-ui,sans-serif;margin:3em;color:#333}code{background:#eee;padding:2px 4px}</style>
<p>{{.Text}} <code>{{.Path}}</code></p>
<script>window.close()</script>`))

func (s *Server) serveOpen(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	path := filepath.Clean(q.Get("path"))
	line, _ := strconv.Atoi(q.Get("line"))
	if !filepath.IsAbs(path) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}
	p, ok := s.Projects.At(path)
	if !ok {
		http.Error(w, "no project of the IDE holds "+path, http.StatusNotFound)
		return
	}
	n := s.pushOpen(p.ID, path, line)
	target := "/project/" + url.PathEscape(p.ID) + "?open=" + url.QueryEscape(path+":"+strconv.Itoa(line))
	if r.Method == http.MethodPost {
		writeJSON(w, map[string]any{"opened": n > 0, "project": p.ID, "url": s.publicURL() + target})
		return
	}
	if n == 0 {
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	lang := "en"
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "fr") {
		lang = "fr"
	}
	_ = openPage.Execute(w, map[string]string{"Text": i18n.T(lang, "Opened in the IDE:"), "Path": path})
}
