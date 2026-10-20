package runtime

import (
	"context"
	"encoding/json"
	"net"
	"path"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/forwards"
	"github.com/DrSmithFr/web-ide/pod/idedir"
	"github.com/DrSmithFr/web-ide/pod/sshx"
)

// Port forwards of an SSH project, kept in .ide/forwards.json; the server opens them (see package
// forwards).

type forwardFile struct {
	Forwards []forwards.Spec `json:"forwards"`
}

// legacyForwardFile is .ide/tunnels.json, the name before the glossary (port forward, not tunnel).
type legacyForwardFile struct {
	Tunnels []forwards.Spec `json:"tunnels"`
}

func (r *Runtime) forwardsPath() string       { return path.Join(r.IdeDir, "forwards.json") }
func (r *Runtime) legacyForwardsPath() string { return path.Join(r.IdeDir, "tunnels.json") }

// ForwardSpecs reads the saved port forwards. A project that only has the old .ide/tunnels.json
// is migrated: its list is moved to forwards.json.
func (r *Runtime) ForwardSpecs() []forwards.Spec {
	var f forwardFile
	if data, err := r.FS.Read(r.forwardsPath()); err == nil {
		_ = json.Unmarshal(data, &f)
	} else if old, err := r.FS.Read(r.legacyForwardsPath()); err == nil {
		var l legacyForwardFile
		_ = json.Unmarshal(old, &l)
		f.Forwards = l.Tunnels
		if r.SaveForwardSpecs(f.Forwards) == nil {
			_ = r.FS.Remove(r.legacyForwardsPath())
		}
	}
	if f.Forwards == nil {
		f.Forwards = []forwards.Spec{}
	}
	return f.Forwards
}

func (r *Runtime) SaveForwardSpecs(list []forwards.Spec) error {
	data, _ := json.MarshalIndent(forwardFile{Forwards: list}, "", "  ")
	if err := idedir.Ensure(r.FS, r.IdeDir); err != nil {
		return err
	}
	return r.FS.Write(r.forwardsPath(), append(data, '\n'))
}

// Dial reaches an address from the SSH host, reconnecting when the connection was lost.
func (r *Runtime) Dial(ctx context.Context, addr string) (net.Conn, error) {
	if r.Local {
		return nil, i18n.New("port forwards are for SSH projects")
	}
	client, err := r.pool.Get(r.target, sshx.Creds{})
	if err != nil {
		return nil, err
	}
	return client.DialContext(ctx, "tcp", addr)
}
