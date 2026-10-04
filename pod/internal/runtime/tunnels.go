package runtime

import (
	"context"
	"encoding/json"
	"net"
	"path"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnels"
)

// Tunnels of an SSH project, kept in .ide/tunnels.json; the server opens them (see package tunnels).

type tunnelFile struct {
	Tunnels []tunnels.Spec `json:"tunnels"`
}

func (r *Runtime) tunnelsPath() string { return path.Join(r.Root, ".ide", "tunnels.json") }

func (r *Runtime) TunnelSpecs() []tunnels.Spec {
	var f tunnelFile
	if data, err := r.FS.Read(r.tunnelsPath()); err == nil {
		_ = json.Unmarshal(data, &f)
	}
	if f.Tunnels == nil {
		f.Tunnels = []tunnels.Spec{}
	}
	return f.Tunnels
}

func (r *Runtime) SaveTunnelSpecs(list []tunnels.Spec) error {
	data, _ := json.MarshalIndent(tunnelFile{Tunnels: list}, "", "  ")
	return r.FS.Write(r.tunnelsPath(), append(data, '\n'))
}

// Dial reaches an address from the SSH host, reconnecting when the connection was lost.
func (r *Runtime) Dial(ctx context.Context, addr string) (net.Conn, error) {
	if r.Local {
		return nil, i18n.New("tunnels are for SSH projects")
	}
	client, err := r.pool.Get(r.target, sshx.Creds{})
	if err != nil {
		return nil, err
	}
	return client.DialContext(ctx, "tcp", addr)
}
