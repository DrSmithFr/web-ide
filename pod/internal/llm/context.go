package llm

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Instructions given to the assistant, compatible with Claude Code and the AGENTS.md
// convention: memory files and skills, global (home of the pod machine) and of the project.

type InstructionFile struct {
	Scope   string `json:"scope"` // global | project
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Skill struct {
	Scope       string `json:"scope"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Dir is the folder of the skill (SKILL.md and its other files).
	Dir string `json:"dir"`
}

type Context struct {
	// Prompt templates edited by the user (nil when absent: default template).
	GlobalPrompt  *string `json:"globalPrompt"`
	ProjectPrompt *string `json:"projectPrompt"`
	// Templates of the Plan mode (nil: default plan template).
	GlobalPlanPrompt  *string           `json:"globalPlanPrompt"`
	ProjectPlanPrompt *string           `json:"projectPlanPrompt"`
	Files             []InstructionFile `json:"files"`
	Skills            []Skill           `json:"skills"`
}

// Project is what the context needs of a project: its root and its file system.
type Project struct {
	Root string
	FS   fsx.FS
}

const (
	globalPromptFile      = "system-prompt.md"
	projectPromptFile     = ".ide/system-prompt.md"
	globalPlanPromptFile  = "plan-prompt.md"
	projectPlanPromptFile = ".ide/plan-prompt.md"
	maxInstruction        = 64 * 1024
)

var globalMemory = []string{".claude/CLAUDE.md", ".codex/AGENTS.md"}
var projectMemory = []string{"CLAUDE.md", ".claude/CLAUDE.md", "CLAUDE.local.md", "AGENTS.md"}
var skillDirs = []string{".claude/skills", ".agents/skills"}

// home is where the global instructions are read. WEBIDE_INSTRUCTIONS_HOME replaces the
// home directory (tests).
func home() string {
	if h := os.Getenv("WEBIDE_INSTRUCTIONS_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// LoadContext reads the prompt templates, memory files and skills.
func (m *Manager) LoadContext(p Project) *Context {
	c := &Context{Files: []InstructionFile{}, Skills: []Skill{}}
	if data, err := os.ReadFile(m.st.Path(globalPromptFile)); err == nil {
		s := string(data)
		c.GlobalPrompt = &s
	}
	if data, err := p.FS.Read(path.Join(p.Root, projectPromptFile)); err == nil {
		s := string(data)
		c.ProjectPrompt = &s
	}
	if data, err := os.ReadFile(m.st.Path(globalPlanPromptFile)); err == nil {
		s := string(data)
		c.GlobalPlanPrompt = &s
	}
	if data, err := p.FS.Read(path.Join(p.Root, projectPlanPromptFile)); err == nil {
		s := string(data)
		c.ProjectPlanPrompt = &s
	}
	local := fsx.Local{}
	h := home()
	seen := map[string]bool{}
	for _, rel := range globalMemory {
		c.Files = append(c.Files, readMemory(local, "global", filepath.Join(h, rel), seen, 0)...)
	}
	// The data folder of the IDE (~/.web-ide) can hold its own global instructions.
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		c.Files = append(c.Files, readMemory(local, "global", m.st.Path(name), seen, 0)...)
	}
	for _, rel := range projectMemory {
		c.Files = append(c.Files, readMemory(p.FS, "project", path.Join(p.Root, rel), seen, 0)...)
	}
	names := map[string]bool{}
	// Project skills first: they win over global ones of the same name.
	for _, d := range skillDirs {
		c.Skills = append(c.Skills, listSkills(p.FS, "project", path.Join(p.Root, d), names)...)
	}
	// Global skills: those of the IDE data folder first, then the Claude Code ones.
	c.Skills = append(c.Skills, listSkills(local, "global", m.st.Path("skills"), names)...)
	for _, d := range skillDirs {
		c.Skills = append(c.Skills, listSkills(local, "global", filepath.Join(h, d), names)...)
	}
	sort.SliceStable(c.Skills, func(i, j int) bool { return c.Skills[i].Name < c.Skills[j].Name })
	return c
}

// Imports of Claude Code memory files: "@path" (relative to the file, or ~/).
var importRe = regexp.MustCompile(`(?m)(?:^|\s)@((?:~/|\./|\.\./|/)?[\w.-][\w./-]*)`)

func readMemory(fs fsx.FS, scope, p string, seen map[string]bool, depth int) []InstructionFile {
	if seen[scope+":"+p] {
		return nil
	}
	data, err := fs.Read(p)
	if err != nil {
		return nil
	}
	seen[scope+":"+p] = true
	text := string(data)
	if len(text) > maxInstruction {
		text = text[:maxInstruction] + "\n… (truncated)"
	}
	out := []InstructionFile{{Scope: scope, Path: p, Content: text}}
	if depth >= 4 {
		return out
	}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if inFence {
			continue
		}
		for _, m := range importRe.FindAllStringSubmatch(line, -1) {
			target := strings.TrimRight(m[1], ".,;:)")
			switch {
			case strings.HasPrefix(target, "~/"):
				if scope != "global" {
					continue // the home of a remote host is not the pod's
				}
				target = filepath.Join(home(), target[2:])
			case !path.IsAbs(target):
				target = path.Join(path.Dir(p), target)
			}
			if st, err := fs.Stat(target); err == nil && !st.Dir {
				out = append(out, readMemory(fs, scope, target, seen, depth+1)...)
			}
		}
	}
	return out
}

func listSkills(fs fsx.FS, scope, dir string, names map[string]bool) []Skill {
	entries, err := fs.List(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.Dir && !e.Link {
			continue
		}
		skillDir := path.Join(dir, e.Name)
		data, err := fs.Read(path.Join(skillDir, "SKILL.md"))
		if err != nil {
			continue
		}
		meta, _ := frontMatter(string(data))
		name := meta["name"]
		if name == "" {
			name = e.Name
		}
		if names[name] {
			continue
		}
		names[name] = true
		out = append(out, Skill{Scope: scope, Name: name, Description: meta["description"], Dir: skillDir})
	}
	return out
}

// frontMatter parses the simple YAML header of a SKILL.md (key: value, quoted values,
// folded or literal blocks) and returns it with the body.
func frontMatter(text string) (map[string]string, string) {
	meta := map[string]string{}
	text = strings.TrimPrefix(text, "\ufeff")
	if !strings.HasPrefix(text, "---") {
		return meta, text
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, text
	}
	head := rest[:end]
	body := strings.TrimLeft(rest[end+4:], "\r\n")
	lines := strings.Split(head, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if v == ">" || v == "|" || v == ">-" || v == "|-" {
			var parts []string
			for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t") || strings.TrimSpace(lines[i+1]) == "") {
				i++
				parts = append(parts, strings.TrimSpace(lines[i]))
			}
			sep := " "
			if v[0] == '|' {
				sep = "\n"
			}
			v = strings.TrimSpace(strings.Join(parts, sep))
		} else if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		meta[k] = v
	}
	return meta, body
}

func (m *Manager) skillFS(p Project, scope string) fsx.FS {
	if scope == "global" {
		return fsx.Local{}
	}
	return p.FS
}

func (m *Manager) findSkill(p Project, name string) (Skill, error) {
	for _, s := range m.LoadContext(p).Skills {
		if s.Name == name {
			return s, nil
		}
	}
	return Skill{}, i18n.Errorf("unknown skill: %s", name)
}

// ReadSkill returns the instructions of a skill and the list of its other files.
func (m *Manager) ReadSkill(p Project, name string) (map[string]any, error) {
	s, err := m.findSkill(p, name)
	if err != nil {
		return nil, err
	}
	fs := m.skillFS(p, s.Scope)
	data, err := fs.Read(path.Join(s.Dir, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	_, body := frontMatter(string(data))
	var files []string
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		es, err := fs.List(dir)
		if err != nil || depth > 3 {
			return
		}
		for _, e := range es {
			if len(files) >= 100 || strings.HasPrefix(e.Name, ".") {
				continue
			}
			r := path.Join(rel, e.Name)
			if e.Dir {
				walk(path.Join(dir, e.Name), r, depth+1)
			} else if r != "SKILL.md" {
				files = append(files, r)
			}
		}
	}
	walk(s.Dir, "", 0)
	return map[string]any{"name": s.Name, "scope": s.Scope, "dir": s.Dir, "content": body, "files": files}, nil
}

// ReadSkillFile reads another file of a skill (path relative to its folder).
func (m *Manager) ReadSkillFile(p Project, name, file string) (string, error) {
	s, err := m.findSkill(p, name)
	if err != nil {
		return "", err
	}
	clean := path.Clean("/" + file)[1:]
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", i18n.New("invalid file")
	}
	data, err := m.skillFS(p, s.Scope).Read(path.Join(s.Dir, clean))
	if err != nil {
		return "", err
	}
	if len(data) > 256*1024 {
		data = append(data[:256*1024], []byte("\n… (truncated)")...)
	}
	return string(data), nil
}

// SaveGlobalPrompt writes (or removes, when empty) the global prompt template of a mode
// ("plan" or the default one). The project ones are project files (PromptFile), written
// like any other.
func (m *Manager) SaveGlobalPrompt(kind, content string) error {
	name := globalPromptFile
	if kind == "plan" {
		name = globalPlanPromptFile
	}
	if strings.TrimSpace(content) == "" {
		return m.st.Remove(name)
	}
	return m.st.WriteFile(name, []byte(content))
}

// ProjectPromptFile returns the project template of a mode, relative to the root.
func ProjectPromptFile(kind string) string {
	if kind == "plan" {
		return projectPlanPromptFile
	}
	return projectPromptFile
}
