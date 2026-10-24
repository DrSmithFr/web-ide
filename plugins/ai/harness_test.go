package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	kbp "github.com/DrSmithFr/web-ide/plugins/kanban"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/commands"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/projects"
	"github.com/DrSmithFr/web-ide/pod/runtime"
	"github.com/DrSmithFr/web-ide/pod/settingsync"
	"github.com/DrSmithFr/web-ide/pod/store"
)

// The unit tests of the plugin run on a pod of their own: real stores, no window, the Kanban
// plugin registered beside it. The tests through the RPC of a whole pod are in server.

func init() { assistant.Scratch = "/scratch-of-the-tests" }

type fakePod struct {
	svc *plugins.Services
	kb  *kbp.K
}

func (p *fakePod) Runtime(string) (*runtime.Runtime, error) {
	return nil, errors.New("no runtime in the tests")
}
func (p *fakePod) OpenRuntime(string) *runtime.Runtime               { return nil }
func (p *fakePod) Project(string) (string, string, bool)             { return "", "", false }
func (p *fakePod) Broadcast(string, any)                             {}
func (p *fakePod) Emit(string, string, any)                          {}
func (p *fakePod) Authorized(*http.Request) bool                     { return false }
func (p *fakePod) AdvertiseChanged()                                 {}
func (p *fakePod) EmitFamily(string, string, any, string)            {}
func (p *fakePod) CloseRuntime(string)                               {}
func (p *fakePod) Services() *plugins.Services                       { return p.svc }
func (p *fakePod) CapabilityOn(*runtime.Runtime, string) bool        { return true }
func (p *fakePod) Capability(*runtime.Runtime, string) any           { return nil }
func (p *fakePod) Stopping() bool                                    { return false }
func (p *fakePod) AssistantTools(*runtime.Runtime) []json.RawMessage { return nil }
func (p *fakePod) Notify(plugins.Note)                               {}
func (p *fakePod) QuestionsChanged(string, string)                   {}
func (p *fakePod) Plugin(id string) (plugins.Plugin, bool) {
	if id == "kanban" {
		return p.kb, true
	}
	return nil, false
}
func (p *fakePod) UICall(context.Context, string, string, string, any, bool, time.Duration) (plugins.UIResult, bool, bool) {
	return plugins.UIResult{}, false, false
}
func (p *fakePod) RunAssistantTool(context.Context, *runtime.Runtime, string, json.RawMessage, string, assistant.Options) (assistant.ToolResult, bool, error) {
	return assistant.ToolResult{}, false, nil
}
func (p *fakePod) CommandsText(*runtime.Runtime) (string, int, error) { return "", 0, nil }
func (p *fakePod) StartSaved(*runtime.Runtime, string, bool) (commands.Command, string, string, error) {
	return commands.Command{}, "", "", errors.New("no command in the tests")
}

type fakeHost struct{ pod *fakePod }

func (h fakeHost) Handle(string, plugins.Handler)                  {}
func (h fakeHost) HandleGlobal(string, plugins.Handler)            {}
func (h fakeHost) AssistantTool(plugins.AssistantTool)             {}
func (h fakeHost) MCPTool(plugins.MCPTool)                         {}
func (h fakeHost) Pod() plugins.Pod                                { return h.pod }
func (h fakeHost) OnProjectOpen(func(rt *runtime.Runtime))         {}
func (h fakeHost) OnWindows(func(n int))                           {}
func (h fakeHost) OnStart(func())                                  {}
func (h fakeHost) OnStop(func())                                   {}
func (h fakeHost) HandlePreview(func(string) (http.Handler, bool)) {}
func (h fakeHost) HandlePeer(http.Handler)                         {}
func (h fakeHost) Advertise(string, func() any)                    {}
func (h fakeHost) Provide(string, any)                             {}
func (h fakeHost) Questions(plugins.Questions)                     {}
func (h fakeHost) Vault(settingsync.VaultMap)                      {}

// newTestA is the plugin on a pod of its own, with a local project at dir.
func newTestA(t *testing.T, dir string) (*A, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := projects.Load(st)
	p, err := reg.Create(projects.Project{Type: "local", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	pod := &fakePod{svc: &plugins.Services{Store: st, Projects: reg}}
	pod.kb = kbp.New()
	pod.kb.Register(fakeHost{pod})
	s := New()
	s.Register(fakeHost{pod})
	return s, p.ID
}
