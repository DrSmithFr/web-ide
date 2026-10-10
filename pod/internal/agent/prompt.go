package agent

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// DefaultTemplates are the templates of the modes when the user wrote none.
var DefaultTemplates = map[string]string{Build: defaultTemplate, Plan: defaultPlanTemplate, Briefing: defaultBriefingTemplate, Orchestrator: defaultOrchestratorTemplate}

var toolsTexts = map[string]string{Build: toolsText, Plan: planToolsText, Briefing: briefingToolsText, Orchestrator: orchestratorToolsText}

// Template returns the template of a mode: the one of the project, else the global one,
// else the default.
func Template(c *llm.Context, mode string) string {
	if c != nil {
		global, project := c.GlobalPrompt, c.ProjectPrompt
		switch mode {
		case Plan:
			global, project = c.GlobalPlanPrompt, c.ProjectPlanPrompt
		case Briefing:
			global, project = c.GlobalBriefingPrompt, c.ProjectBriefingPrompt
		}
		if project != nil && strings.TrimSpace(*project) != "" {
			return *project
		}
		if global != nil && strings.TrimSpace(*global) != "" {
			return *global
		}
	}
	if t, ok := DefaultTemplates[mode]; ok {
		return t
	}
	return defaultTemplate
}

// PromptVars are the values of the placeholders of a template.
type PromptVars struct {
	Project string
	Root    string
	// Host: the SSH host of a remote project ("" for a local one).
	Host string
	// ActiveFile: relative path of the file shown in the editor ("" when none).
	ActiveFile string
}

var placeholder = regexp.MustCompile(`\{\{(\w+)\}\}`)
var blankLines = regexp.MustCompile(`\n{3,}`)

// SystemPrompt is the final system prompt: the template of the mode with its placeholders,
// the instruction files, the ticket linked to the conversation, and the skills.
func SystemPrompt(c *llm.Context, v PromptVars, tools bool, mode, ticket string) string {
	vars := map[string]string{
		"project":    v.Project,
		"root":       v.Root,
		"host":       "",
		"activeFile": "",
		"date":       time.Now().Format("Monday, January 2, 2006"),
		"tools":      "",
	}
	if v.Host != "" {
		vars["host"] = " on the SSH host " + v.Host
	}
	if v.ActiveFile != "" {
		vars["activeFile"] = "Active file in the editor: " + v.ActiveFile + "."
	}
	if tools {
		vars["tools"] = toolsTexts[mode]
		if vars["tools"] == "" {
			vars["tools"] = toolsText
		}
	}
	text := placeholder.ReplaceAllStringFunc(Template(c, mode), func(m string) string {
		if val, ok := vars[m[2:len(m)-2]]; ok {
			return val
		}
		return m
	})
	text = strings.TrimSpace(blankLines.ReplaceAllString(text, "\n\n"))
	if c != nil && len(c.Files) > 0 {
		text += "\n\n# Instructions"
		text += "\nInstructions of the user (global) and of the project. They come before your habits; the project ones come before the global ones."
		for _, f := range c.Files {
			scope := "Project"
			p := f.Path
			if f.Scope == "global" {
				scope = "Global"
				p = homePath.ReplaceAllString(p, "~")
			} else {
				p = strings.TrimPrefix(strings.TrimPrefix(p, v.Root), "/")
			}
			text += "\n\n## " + scope + " · " + p + "\n" + strings.TrimSpace(f.Content)
		}
	}
	if ticket != "" {
		text += "\n\n" + ticket
	}
	if c != nil && len(c.Skills) > 0 && tools {
		text += "\n\n# Skills"
		text += "\nAvailable skills. When a request matches one of them, load its instructions with load_skill(name) before acting; read_skill_file reads its other files."
		for _, s := range c.Skills {
			d := s.Description
			if d == "" {
				d = "(no description)"
			}
			text += "\n- " + s.Name + ": " + d
		}
	}
	return text
}

var homePath = regexp.MustCompile(`^/home/[^/]+`)

// TicketPrompt is the part of the system prompt about the ticket linked to a conversation:
// what its role must do, then the ticket as the model reads it.
func TicketPrompt(id int64, role, branch string, parent, feedback int64, markdown string) string {
	b := ""
	if branch != "" {
		b = " on the branch " + branch + ", in its worktree (the root of the open project)"
		if parent != 0 {
			b += ", shared with its lineage: this ticket is a step of #" + itoa(parent) + ", develop only this step, on top of the previous ones"
		}
	}
	fb := "the open feedback (not checked yet)"
	if feedback != 0 {
		fb = "the feedback with id " + itoa(feedback)
	}
	r := strings.NewReplacer("{{branch}}", b, "{{feedback}}", fb, "{{id}}", itoa(id)).Replace(RoleInstructions[role])
	return "# Ticket linked to this conversation\nThis conversation works on ticket #" + itoa(id) +
		" of the kanban of the project. The tools kanban_update, kanban_add_note, kanban_set_plan, kanban_goal, kanban_feedback and kanban_move act on this ticket.\n\n" +
		r + "\n\n" + markdown
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
