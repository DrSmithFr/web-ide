package llm

import (
	"path"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/fsx"
)

// Instructions of the folders the assistant works in: like Claude Code, it reads the
// AGENTS.md (else the CLAUDE.md) of a folder the first time it touches a file there.

var nestedNames = []string{"AGENTS.md", "CLAUDE.md"}

// NestedInstructions returns the instruction files of the folders from the root (excluded,
// its instructions are in the system prompt) down to dir that loaded does not know yet: the
// first of AGENTS.md and CLAUDE.md of each folder, the outermost folder first. Paths are
// relative to the root.
func NestedInstructions(fs fsx.FS, root, dir string, loaded func(rel string) bool) []InstructionFile {
	root, dir = path.Clean(root), path.Clean(dir)
	if dir == root || !fsx.Within(root, dir) {
		return nil
	}
	var dirs []string
	for d := dir; d != root && d != "/" && d != "."; d = path.Dir(d) {
		dirs = append([]string{d}, dirs...)
	}
	var out []InstructionFile
	for _, d := range dirs {
		for _, name := range nestedNames {
			rel := strings.TrimPrefix(path.Join(d, name), strings.TrimSuffix(root, "/")+"/")
			if loaded(rel) {
				break // this folder was already read
			}
			if f, ok := ReadNested(fs, root, rel); ok {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// ReadNested reads an instruction file of a subfolder, from its path relative to the root.
func ReadNested(fs fsx.FS, root, rel string) (InstructionFile, bool) {
	data, err := fs.Read(path.Join(root, rel))
	if err != nil {
		return InstructionFile{}, false
	}
	text := string(data)
	if len(text) > maxInstruction {
		text = text[:maxInstruction] + "\n… (truncated)"
	}
	return InstructionFile{Scope: "project", Path: rel, Content: text}, true
}
