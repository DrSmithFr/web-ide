package previews

import (
	"encoding/json"
	"path"

	"antennae.space/ide/plugins/previews/internal/forwards"
	"antennae.space/ide/pod/idedir"
	"antennae.space/ide/pod/runtime"
)

// The port forwards of an SSH project, kept in .ide/forwards.json (of the main project for a
// worktree); the plugin opens them (internal/forwards).

type forwardFile struct {
	Forwards []forwards.Spec `json:"forwards"`
}

// legacyForwardFile is .ide/tunnels.json, the name before the glossary (port forward, not tunnel).
type legacyForwardFile struct {
	Tunnels []forwards.Spec `json:"tunnels"`
}

func forwardsPath(rt *runtime.Runtime) string       { return path.Join(rt.IdeDir, "forwards.json") }
func legacyForwardsPath(rt *runtime.Runtime) string { return path.Join(rt.IdeDir, "tunnels.json") }

// specsOf reads the saved port forwards. A project that only has the old .ide/tunnels.json
// is migrated: its list is moved to forwards.json.
func specsOf(rt *runtime.Runtime) []forwards.Spec {
	var f forwardFile
	if data, err := rt.FS.Read(forwardsPath(rt)); err == nil {
		_ = json.Unmarshal(data, &f)
	} else if old, err := rt.FS.Read(legacyForwardsPath(rt)); err == nil {
		var l legacyForwardFile
		_ = json.Unmarshal(old, &l)
		f.Forwards = l.Tunnels
		if saveSpecs(rt, f.Forwards) == nil {
			_ = rt.FS.Remove(legacyForwardsPath(rt))
		}
	}
	if f.Forwards == nil {
		f.Forwards = []forwards.Spec{}
	}
	return f.Forwards
}

func saveSpecs(rt *runtime.Runtime, list []forwards.Spec) error {
	data, _ := json.MarshalIndent(forwardFile{Forwards: list}, "", "  ")
	if err := idedir.Ensure(rt.FS, rt.IdeDir); err != nil {
		return err
	}
	return rt.FS.Write(forwardsPath(rt), append(data, '\n'))
}
