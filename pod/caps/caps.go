// Package caps holds the capabilities on the pod side: what a plugin gives the others and the
// core (Host.Provide of pod/plugins), reached through Pod.Capability on a project where the
// provider is enabled. Each one is an interface of this package, so no plugin imports the code
// of another, and the core reaches a plugin's work only through it (the page side is
// web/src/plugins/capabilities.ts). It imports the tool kit of the assistant (pod/assistant),
// never the other way. The consoles and the saved commands are not here: they are core
// (`pod/console`, `pod/commands`, decision 6 of plugins.md).
package caps

import (
	"context"
	"encoding/json"

	"antennae.space/ide/pod/assistant"
	"antennae.space/ide/pod/runtime"
)

// Language is the language capability (the Languages plugin): the language servers of a
// project, as the lsp_* tools of the assistant use them.
type Language interface {
	// LangOf is the language of a file ("" when no server knows it), URI its URI and LanguageID
	// its languageId for the servers.
	LangOf(path string) string
	URI(path string) string
	LanguageID(path string) string
	Notify(rt *runtime.Runtime, lang, method string, params json.RawMessage) error
	Request(ctx context.Context, rt *runtime.Runtime, lang, method string, params json.RawMessage) (json.RawMessage, error)
	// Diagnostics are those of a file and their sequence number, which changes when its server
	// publishes them again; AllDiagnostics those of every file, by URI.
	Diagnostics(rt *runtime.Runtime, uri string) ([]Diagnostic, int)
	AllDiagnostics(rt *runtime.Runtime) map[string][]Diagnostic
}

// Diagnostic is a diagnostic of a language server (LSP publishDiagnostics).
type Diagnostic struct {
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

// Search is the search capability (the Search plugin): the text search of a project, as the
// search_text tool of the assistant uses it.
type Search interface {
	Grep(ctx context.Context, rt *runtime.Runtime, o SearchOptions) (*SearchResult, error)
}

type SearchOptions struct {
	Query   string `json:"query"`
	Regex   bool   `json:"regex"`
	Case    bool   `json:"caseSensitive"`
	Word    bool   `json:"wholeWord"`
	Include string `json:"include"` // comma separated globs on the file name, e.g. "*.go,*.ts"
	Max     int    `json:"max"`
	// Exclude lists absolute folders left out (folders marked as excluded).
	Exclude []string `json:"-"`
}

type SearchMatch struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Col    int      `json:"col"` // UTF-16 column of the first match in the whole line
	Text   string   `json:"text"`
	Ranges [][2]int `json:"ranges"` // UTF-16 offsets in Text, as JavaScript indexes strings
}

type SearchResult struct {
	Matches   []SearchMatch `json:"matches"`
	Truncated bool          `json:"truncated"`
	Files     int           `json:"files"`
}

// Previews is the previews capability (the Previews plugin): the app previews of the pod, as the
// share_preview and list_previews tools of the assistant and the MCP use them.
type Previews interface {
	// Share resolves the app of share_preview in project: the card of the preview is the
	// Preview of the result, shown to the user, who starts it.
	Share(project string, rt *runtime.Runtime, a assistant.Args) (assistant.ToolResult, error)
	// List are the previews running, of every project.
	List() []PreviewInfo
}

// PreviewInfo is a preview running, as the tools list it.
type PreviewInfo struct {
	Project, Title, Command string
	AppPort                 int
	URL, PublicURL          string
}

// Agents is the agents capability (the AI plugin), as the Kanban uses it: the sessions of the
// CLI agents developing the tickets (kanban setting devAgent), and the time of the answers of
// the conversations linked to a ticket.
type Agents interface {
	// Develop gives a ticket to agent in the project of its worktree: the prompt of its role in
	// the session of the ticket, else in a new session (created).
	Develop(project, agent string, t TicketBrief) (session AgentSession, created bool, err error)
	// TicketSession finds the session of a ticket in the files of the Agents tool of a project,
	// open or not.
	TicketSession(project string, ticket int64) (AgentSession, bool)
	// Purge ends the sessions of a project removed (a worktree), also when no window had it
	// open: their processes in the keeper, their files.
	Purge(project string)
	// ChatTimes are the times of the answers of the conversations of a project, by id.
	ChatTimes(project string) (map[string]ChatTime, error)
}

// TicketBrief is what the prompt of a development agent says of its ticket: its role
// (developer, reviewer…), the feedback it answers (0 for none), the ticket as Markdown and the
// words of the user (Text).
type TicketBrief struct {
	ID, Parent, Feedback int64
	Role, Title, Branch  string
	Markdown, Text       string
}

// AgentSession is a session of the Agents tool.
type AgentSession struct {
	ID, Name, Title, State string
}

// ChatTime is the time of the answers of a conversation, its thinking time, and the
// conversation it is a sub-agent of ("" for none).
type ChatTime struct {
	Parent      string
	Ms, ThinkMs int64
}
