package server

import (
	"path"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// nestedFolder is the folder a tool call enters: the one of the file it reads or writes, or
// the folder it lists ("" for the other tools).
func nestedFolder(root, name string, a toolArgs) string {
	switch name {
	case "read_file", "edit_file", "write_file":
		if a.str("path") != "" {
			return path.Dir(absPath(root, a.str("path")))
		}
	case "list_dir":
		return absPath(root, a.str("path"))
	}
	return ""
}

// addNested appends to the result of a tool call the instructions of the folders it entered
// for the first time in this conversation (AGENTS.md, else CLAUDE.md), and remembers them.
func (s *Server) addNested(r *agentRun, ref *runtimeRef, name string, a toolArgs, res *toolResult) {
	dir := nestedFolder(ref.rt.Root, name, a)
	if dir == "" || res.Status == "error" {
		return
	}
	r.mu.Lock()
	known := map[string]bool{}
	for _, n := range r.chat.Nested {
		known[n.Path] = true
	}
	r.mu.Unlock()
	files := llm.NestedInstructions(ref.rt.FS, ref.rt.Root, dir, func(rel string) bool { return known[rel] })
	if len(files) == 0 {
		return
	}
	res.Content += "\n"
	for _, f := range files {
		res.Content += "\n<instructions path=\"" + f.Path + "\">\n" + strings.TrimSpace(f.Content) + "\n</instructions>"
	}
	res.Content += "\n(Instructions of the folder, added once: follow them for the files there.)"
	r.mu.Lock()
	for _, f := range files {
		if !known[f.Path] {
			r.chat.Nested = append(r.chat.Nested, agent.NestedFile{Path: f.Path, At: len(r.chat.Messages)})
		}
	}
	r.mu.Unlock()
}
