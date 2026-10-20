package ai

import (
	"path"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/llm"
)

// nestedFolder is the folder a tool call enters: the one of the file it reads or writes, or
// the folder it lists ("" for the other tools).
func nestedFolder(root, name string, a assistant.Args) string {
	switch name {
	case "read_file", "edit_file", "write_file":
		if a.Str("path") != "" {
			return path.Dir(assistant.AbsPath(root, a.Str("path")))
		}
	case "list_dir":
		return assistant.AbsPath(root, a.Str("path"))
	}
	return ""
}

// addNested appends to the result of a tool call the instructions of the folders it entered
// for the first time in this conversation (AGENTS.md, else CLAUDE.md), and remembers them.
func (s *A) addNested(r *assistantRun, ref *runtimeRef, name string, a assistant.Args, res *assistant.ToolResult) {
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
			r.chat.Nested = append(r.chat.Nested, assistant.NestedFile{Path: f.Path, At: len(r.chat.Messages)})
		}
	}
	r.mu.Unlock()
}
