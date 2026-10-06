package agent

import (
	"encoding/json"
	"fmt"
)

// Definitions of the tools offered to the model (OpenAI function format). The texts are read
// by the model: English, kept as they were in the page.

type obj = map[string]any

func str(description string) obj     { return obj{"type": "string", "description": description} }
func integer(description string) obj { return obj{"type": "integer", "description": description} }
func boolean(description string) obj { return obj{"type": "boolean", "description": description} }
func strList(description string) obj {
	return obj{"type": "array", "items": obj{"type": "string"}, "description": description}
}
func enum(description string, values ...string) obj {
	o := obj{"type": "string", "enum": values}
	if description != "" {
		o["description"] = description
	}
	return o
}

// Def is a tool definition, marshalled once.
type Def struct {
	Name string
	JSON json.RawMessage
}

func fn(name, description string, props obj, required ...string) Def {
	if required == nil {
		required = []string{}
	}
	data, _ := json.Marshal(obj{"type": "function", "function": obj{"name": name, "description": description, "parameters": obj{"type": "object", "properties": props, "required": required}}})
	return Def{Name: name, JSON: data}
}

var (
	pathArg   = str("Path relative to the project root (or absolute)")
	lineArg   = integer("Line number (starting at 1), as shown by read_file")
	symbolArg = str("The symbol as written on that line (function, variable, type name…)")
)

// WriteTools change files: only in Build mode.
var WriteTools = map[string]bool{"edit_file": true, "write_file": true}

var projectDefs = []Def{
	fn("list_dir", "Lists the content of a project folder.", obj{"path": str(`Folder, relative to the root ("" or "." for the root)`)}),
	fn("find_files", "Finds project files whose path contains the pattern (or matches the * ? glob).", obj{"pattern": str(`Ex. "handler", "*.go", "src/**/*.tsx"`)}, "pattern"),
	fn("read_file", "Reads a project file. Each line is prefixed with its number and a tab (the prefix is not part of the file).",
		obj{"path": pathArg, "start_line": integer("First line (1 by default)"), "end_line": integer("Last line, included")}, "path"),
	fn("search_text", "Searches a text (or an RE2 regular expression) in all the project files.",
		obj{"query": str("Text or expression"), "regex": boolean("query is a regular expression"), "include": str(`File name globs, comma-separated, e.g. "*.go,*.ts"`)}, "query"),
	fn("edit_file", "Replaces an exact passage of a file. old_string must appear exactly once (without the line numbers of read_file): include enough context. To create a file, use write_file.",
		obj{"path": pathArg, "old_string": str("Exact text to replace"), "new_string": str("Replacement text")}, "path", "old_string", "new_string"),
	fn("write_file", "Creates a file or replaces its whole content.", obj{"path": pathArg, "content": str("Full content of the file")}, "path", "content"),
	fn("lsp_symbols", "Structure of a file (classes, functions, methods…) from the language server.", obj{"path": pathArg}, "path"),
	fn("lsp_workspace_symbols", "Finds a symbol by name in the whole project (language server).", obj{"query": str("Name or start of the name"), "path": str("A file of the language (optional: active file by default)")}, "query"),
	fn("lsp_definition", "Finds the definition of the symbol written on that line.", obj{"path": pathArg, "line": lineArg, "symbol": symbolArg}, "path", "line", "symbol"),
	fn("lsp_references", "Finds the usages of the symbol written on that line.", obj{"path": pathArg, "line": lineArg, "symbol": symbolArg}, "path", "line", "symbol"),
	fn("lsp_hover", "Type and documentation of the symbol written on that line.", obj{"path": pathArg, "line": lineArg, "symbol": symbolArg}, "path", "line", "symbol"),
	fn("lsp_diagnostics", "Errors and warnings of the language server, for a file or for the open files.", obj{"path": str("File (optional)")}),
	fn("load_skill", "Loads the full instructions of a skill of the list (and the list of its other files).", obj{"name": str("Skill name")}, "name"),
	fn("read_skill_file", "Reads another file of a skill (path relative to its folder, as listed by load_skill).", obj{"name": str("Skill name"), "file": str("Path of the file in the skill")}, "name", "file"),
	fn("open_file", "Opens a file in the editor of the user to show it, optionally selecting lines.",
		obj{"path": pathArg, "line": integer("First line to show (starting at 1)"), "end_line": integer("Last line to select")}, "path"),
	fn("focus", "Brings an element of the IDE to the front: an open file, a panel (explorer, search, git, kanban, console, problems, docker, database, assistant, structure, conflicts, info), a console or the problems list.",
		obj{
			"target":     enum("Kind of element", "file", "panel", "console", "problems"),
			"path":       str("File (target=file)"),
			"panel":      str("Panel (target=panel)"),
			"console_id": str("Console id (target=console), see list_consoles"),
		}, "target"),
	fn("bash", "Runs a shell command (sh -c, in the project) and returns its output (stdout and stderr) and exit code. Use it for all your operations: tests, builds, git, command-line tools. No terminal and no input: no interactive command.",
		obj{"command": str(`Command, e.g. "go test ./..."`), "cwd": str("Working directory (project root by default)"), "timeout": integer("Timeout in seconds (120 by default, 1800 max)")}, "command"),
	fn("run_command", "Runs a command in a new console of the IDE, visible to the user (development server, watcher, command they want to follow or use). Waits for its end or the timeout, then returns the start of its output; it keeps running afterwards. For your own operations, use bash.",
		obj{"command": str(`Command, e.g. "npm run dev"`), "cwd": str("Working directory (project root by default)"), "timeout": integer("Seconds to wait before returning (20 by default, 600 max)")}, "command"),
	fn("list_consoles", "Lists the open consoles (terminals and commands) with their state.", obj{}),
	fn("read_console", "Reads the end of the output of a console.", obj{"console_id": str("Console id"), "lines": integer("Number of lines (200 by default)")}, "console_id"),
	fn("console_input", "Types text in a console (interactive terminal, program waiting for an answer). A newline is added unless enter=false.",
		obj{"console_id": str("Console id"), "text": str("Text to type"), "enter": boolean("Press Enter after the text (true by default)")}, "console_id", "text"),
}

var dockerDefs = []Def{
	fn("docker_ps", "State of the Docker Compose services of the project (Compose file at the project root): container, state, health, image, published ports. Without Compose file, lists every container of the host.", obj{}),
	fn("docker_logs", "Last lines of the logs of a Compose service of the project, or of any container (name or id), stdout and stderr together, with timestamps.",
		obj{"service": str("Compose service of the project"), "container": str("Container name or id (instead of service)"), "lines": integer("Number of lines (200 by default, 2000 max)"), "filter": str("Keep only the lines containing this text (case-insensitive)")}),
}

// MaxQuestions asked by one ask_user call; MaxDescription and MaxNote are the limits of the
// kanban (characters).
const (
	MaxQuestions   = 10
	MaxDescription = 1500
	MaxNote        = 1000
)

var askUserDef = fn("ask_user",
	fmt.Sprintf("Asks the user one or more questions (1 to %d) when information is missing or a choice is theirs. Each question has a type that picks its look: choice (the default), idea, compare, rank or scenario. The user can also answer freely, say \"I don't know\" or \"Up to you\", and add a note to any question. Questions can form a small graph: a question with an id is asked only when an option with its id as next is chosen; the others are asked in order. After this call the turn stops until the answers come back (as the tool result). Write the questions in the user's language.", MaxQuestions),
	obj{"questions": obj{
		"type":        "array",
		"description": "The questions, asked one at a time",
		"items": obj{
			"type": "object",
			"properties": obj{
				"question":  str("The full question, ending with a question mark (for idea: the proposal)"),
				"id":        str("Short id, for a question asked only after an option leading to it (next)"),
				"header":    str(`Very short label (12 characters max), e.g. "Format"`),
				"type":      enum("choice: 2 to 6 options; idea: one proposal to validate, no options; compare: two approaches, exactly 2 options; rank: 2 to 8 options to order; scenario: a situation, then 2 to 6 options", QuestionTypes...),
				"situation": str("scenario: the concrete situation (a few sentences)"),
				"top":       integer("rank: only the top N is picked (N below the number of options)"),
				"options": obj{
					"type":        "array",
					"description": `The choices (none for idea); put the recommended option first with "(recommended)"`,
					"items": obj{"type": "object", "properties": obj{
						"label":       str("Choice (1 to 5 words)"),
						"description": str("What this choice implies"),
						"pros":        strList("What goes for it (short points)"),
						"cons":        strList("What goes against it (short points)"),
						"next":        str("id of the question asked next when this option is chosen"),
					}, "required": []string{"label"}},
				},
				"multiple": boolean("choice: several choices allowed"),
				"nextYes":  str("idea: id of the question asked after Yes or Exactly"),
				"nextNo":   str("idea: id of the question asked after No"),
			},
			"required": []string{"question"},
		},
	}}, "questions")

// MaxPageElements and MaxStrokePoints bound a board_draw call.
const (
	MaxPageElements = 200
	MaxStrokePoints = 500
)

var (
	point     = obj{"type": "array", "items": obj{"type": "number"}, "description": "[x, y]"}
	endOfLine = obj{"description": "An element id, or [x, y]"}
	zoneTree  = obj{"type": "object", "description": `Zone: {"name"?, "split"?: {"dir": "rows"|"cols", "sizes": [fractions], "children": [zones]}}`}
)

var boardDoodleDef = fn("board_draw_doodle",
	"Draws a new page on the board shared with the user (next to the conversation): a layout, a flow, a sketch, or annotations on a clone of a page (clone: the number of an image, a doodle of the user or one of your pages). Pages never change: to fix one, draw on a clone. Returns the description of the page and its image.",
	obj{
		"title": str("Short name of the page"),
		"size":  str(`Frame of a blank page: "16:9" (1280x720, default), "mobile" (390x844), "square" (800x800), or "WIDTHxHEIGHT" like "1000x600"`),
		"clone": integer("Number of a page of the board to clone: the elements are drawn on top of it (it keeps its size). Without clone, the page is blank"),
		"elements": obj{
			"type":        "array",
			"description": fmt.Sprintf("At most %d, in px from the top left corner of the frame. Colors: ink, red, blue, green (marker: yellow, lime, pink, cyan)", MaxPageElements),
			"items": obj{
				"type": "object",
				"properties": obj{
					"type":   enum("", "rect", "ellipse", "line", "arrow", "text", "layout", "stroke"),
					"id":     str("To tie lines and arrows to this element"),
					"x":      integer("rect, ellipse, text, layout"),
					"y":      integer("rect, ellipse, text, layout"),
					"w":      integer("rect, ellipse, layout"),
					"h":      integer("rect, ellipse, layout"),
					"color":  str("ink (default), red, blue, green"),
					"fill":   boolean("rect, ellipse: a light tint inside"),
					"label":  str("rect, ellipse: text centred inside"),
					"from":   endOfLine,
					"to":     endOfLine,
					"text":   str("text: lines separated by \\n"),
					"size":   enum("text: s, m (default), l", "s", "m", "l"),
					"root":   zoneTree,
					"points": obj{"type": "array", "items": point, "description": fmt.Sprintf("stroke: [[x, y]…], %d at most", MaxStrokePoints)},
					"marker": boolean("stroke: a highlighter"),
				},
				"required": []string{"type"},
			},
		},
	}, "title", "elements")

var boardImageDef = fn("board_draw_image",
	"Puts an image on the board as a new page, its size the size of the image: an SVG you write, an image file of the project, or a capture of the screen of the user (they choose it; the turn waits for them). To annotate it, draw on a clone of that page with board_draw_doodle (clone).",
	obj{
		"title": str("Short name of the page"),
		"image": str(`SVG markup ("<svg …>…</svg>", with a viewBox or a width and height; no script nor external reference), the path of an image of the project (png, jpg, webp, gif, svg), or "screen"`),
	}, "title", "image")

var sharePreviewDef = fn("share_preview",
	"Offers the user to try the app: shows a card in the conversation that, on their click, starts the command in a console (unless it runs already), waits for the port, then opens the app on a temporary URL they can reach from any device (phone included). Nothing runs before the click. Use it for a development server rather than run_command when the user should see the app.",
	obj{
		"title":   str("Short name of what to try, e.g. \"Login page\""),
		"command": str("Command that starts the app (development server), e.g. \"npm run dev\""),
		"port":    integer("Port the app listens on, on the machine of the project"),
		"cwd":     str("Folder to run it in, relative to the project root (default: the root)"),
	}, "title", "command", "port")

var (
	statuses      = []string{"new", "todo", "in_progress", "review", "done", "abandoned"}
	priorities    = []string{"low", "normal", "high", "critical"}
	sizes         = []string{"s", "m", "l", "xl"}
	sizeProp      = enum("Estimated effort of the whole ticket: s (a few files, an hour of agent work), m, l, xl (many files across the pod and the page, several days)", sizes...)
	lineageParent = integer("Parent ticket: this one becomes the next step of its lineage, developed in the parent's worktree after it (0 takes it out). Only before its development starts")
	lineageDeps   = obj{"type": "array", "items": obj{"type": "integer"}, "description": "Tickets of other lineages this one waits for: it starts once they are merged or done (replaces the list)"}
)

var kanbanReadDefs = []Def{
	fn("kanban_list", "Lists the tickets of the kanban of the project (number, status, priority, title, goals, open feedback).", obj{"status": enum("Status", statuses...), "query": str("Filter on the title (optional)")}),
	fn("kanban_get", "Reads a whole ticket: description, notes, plan, goals and test feedback (with their ids), linked files, conversations, branch.", obj{"id": integer("Ticket number")}, "id"),
	fn("kanban_create", "Creates a ticket in the backlog (status New). Use it when the user asks for it or agrees to note a task for later.",
		obj{
			"title":       str("Short title"),
			"description": str(fmt.Sprintf("Description in Markdown, %d characters max: context, need, acceptance criteria", MaxDescription)),
			"priority":    enum("", priorities...),
			"files":       strList("Paths of the files concerned (relative to the root)"),
			"parent":      lineageParent,
			"depends_on":  lineageDeps,
		}, "title"),
}

var kanbanWriteDefs = []Def{
	fn("kanban_update", "Changes the ticket linked to this conversation (only the given fields).",
		obj{
			"title":        str("New title"),
			"description":  str(fmt.Sprintf("New full description (Markdown, %d characters max)", MaxDescription)),
			"priority":     enum("", priorities...),
			"test_summary": str("How to test the ticket (Markdown): steps, commands, expected results"),
			"add_files":    strList("Files to link"),
			"remove_files": strList("Files to unlink"),
			"size":         sizeProp,
			"parent":       lineageParent,
			"depends_on":   lineageDeps,
		}),
	fn("kanban_add_note", fmt.Sprintf("Adds a short note to the linked ticket (%d characters max), linked to this conversation: a decision, a fact found, an answer of the user worth keeping. Not for progress logs, restatements of the ticket or corrections of earlier notes.", MaxNote),
		obj{"text": str(fmt.Sprintf("Note in Markdown, %d characters max", MaxNote))}, "text"),
	fn("kanban_set_plan", `Writes the implementation plan of the linked ticket and its goals (verifiable objectives, checked during development). Replaces the plan and the goals of a previous plan (the goals of the user stay). A New ticket moves to "To do".`,
		obj{
			"plan": str("Plan in Markdown: approach, files, steps, risks, tests"),
			"goals": obj{
				"type":        "array",
				"description": "Goals, each one verifiable",
				"items":       obj{"type": "object", "properties": obj{"title": str("Short title, one sentence"), "description": str("How to check it (optional, a few lines)")}, "required": []string{"title"}},
			},
			"size": sizeProp,
		}, "plan", "goals", "size"),
	fn("kanban_goal", "Checks, unchecks or adds a goal of the linked ticket. Check each goal as soon as it is reached and verified.",
		obj{
			"action":      enum("", "check", "uncheck", "add"),
			"id":          integer("Goal id (check / uncheck), see kanban_get"),
			"title":       str("Title of the goal (add)"),
			"description": str("How to check it (add, optional)"),
		}, "action"),
	fn("kanban_feedback", "Marks a test feedback of the linked ticket as handled (done) once fixed and verified, or as open again (reopen).",
		obj{"action": enum("", "done", "reopen"), "id": integer("Feedback id, see kanban_get")}, "action", "id"),
	fn("kanban_move", "Moves the linked ticket from In progress to To test (status review), with test_summary: how to test it. Other changes belong to the user.",
		obj{
			"status":       enum("", "review"),
			"test_summary": str("For review: what to test and how (steps, commands, expected results), in Markdown"),
			"comment":      str("Comment for the history (optional)"),
		}, "status"),
	fn("kanban_link_commit", "Links a commit to the linked ticket (after a git commit).", obj{"hash": str("Commit hash (short or full)")}, "hash"),
}

var (
	exitPlanDef = fn("exit_plan_mode", "Presents the finished plan to the user (Plan mode). They can accept it, which switches to Build mode to carry it out, or ask for changes. After this call, wait for their answer.",
		obj{"plan": str("The full plan in Markdown: goal, files, numbered steps, tests")}, "plan")
	compactDef = fn("compact_conversation", "Summarizes the older messages of the conversation to free context (the last exchange is kept). Use it when a task is done or the conversation gets long.",
		obj{"instructions": str("What the summary must keep first (optional)")})
)

// KanbanTools are handled by the kanban side of the agent; KanbanWrite act on the linked ticket.
var (
	KanbanTools = names(kanbanReadDefs, kanbanWriteDefs)
	KanbanWrite = names(kanbanWriteDefs)
	DockerTools = names(dockerDefs)
)

func names(lists ...[]Def) map[string]bool {
	out := map[string]bool{}
	for _, l := range lists {
		for _, d := range l {
			out[d.Name] = true
		}
	}
	return out
}

// ToolsFor returns the tools offered in a mode: no file change in Plan and Briefing,
// exit_plan_mode only in Plan (not for the briefing or the plan of a ticket, which end in
// the ticket), the tools that change a ticket only with a linked ticket. A sub-agent (sub)
// asks its parent instead of the user and reports instead of presenting a plan.
func ToolsFor(mode string, ticket *TicketLink, sub bool) []json.RawMessage {
	var out []json.RawMessage
	for _, d := range projectDefs {
		if mode != Build && WriteTools[d.Name] {
			continue
		}
		out = append(out, d.JSON)
	}
	for _, d := range dockerDefs {
		out = append(out, d.JSON)
	}
	for _, d := range kanbanReadDefs {
		out = append(out, d.JSON)
	}
	if ticket != nil {
		for _, d := range kanbanWriteDefs {
			out = append(out, d.JSON)
		}
	}
	out = append(out, boardDoodleDef.JSON, boardImageDef.JSON, sharePreviewDef.JSON)
	if sub {
		for _, d := range childAgentDefs {
			out = append(out, d.JSON)
		}
		return append(out, compactDef.JSON)
	}
	out = append(out, askUserDef.JSON)
	for _, d := range parentAgentDefs {
		out = append(out, d.JSON)
	}
	if mode == Plan && (ticket == nil || ticket.Role != "briefing" && ticket.Role != "plan") {
		out = append(out, exitPlanDef.JSON)
	}
	return append(out, compactDef.JSON)
}
