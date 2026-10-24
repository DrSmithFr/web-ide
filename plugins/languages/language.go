package languages

import (
	"context"
	"encoding/json"

	"antennae.space/ide/pod/caps"

	"antennae.space/ide/plugins/languages/internal/lsp"
	"antennae.space/ide/pod/runtime"
)

// servers is the part of a runtime holding its language servers: they stop with the project,
// and a while after its last window left.
type servers struct{ *lsp.Manager }

func (s servers) Stop(bool) { s.StopAll() }
func (s servers) Idle()     { s.StopAll() }

// serversOf are the language servers of the project of rt, made on first use.
func serversOf(rt *runtime.Runtime) *lsp.Manager {
	return rt.Part("languages", func() any {
		exists := func(p string) bool { _, err := rt.FS.Stat(p); return err == nil }
		return servers{lsp.NewManager(rt.Runner, rt.Root, rt.Local, rt.Config.LSP, exists, rt.Emit)}
	}).(servers).Manager
}

// language is the language capability (caps.Language) the plugin provides.
type language struct{}

func (language) LangOf(p string) string     { return lsp.LangOf(p) }
func (language) URI(p string) string        { return lsp.URI(p) }
func (language) LanguageID(p string) string { return lsp.LanguageID(p) }

func (language) Notify(rt *runtime.Runtime, lang, method string, params json.RawMessage) error {
	return serversOf(rt).Notify(lang, method, params)
}

func (language) Request(ctx context.Context, rt *runtime.Runtime, lang, method string, params json.RawMessage) (json.RawMessage, error) {
	return serversOf(rt).Request(ctx, lang, method, params)
}

func (language) Diagnostics(rt *runtime.Runtime, uri string) ([]caps.Diagnostic, int) {
	return serversOf(rt).Diagnostics(uri)
}

func (language) AllDiagnostics(rt *runtime.Runtime) map[string][]caps.Diagnostic {
	return serversOf(rt).AllDiagnostics()
}
