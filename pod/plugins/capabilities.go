package plugins

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// The capabilities on the pod side: what a plugin gives the others and the core (Host.Provide),
// reached through Pod.Capability on a project where the provider is enabled. Each one is an
// interface of this file, so no plugin imports the code of another (the page side is
// web/src/plugins/capabilities.ts).

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
