package runtime

import (
	"context"
	"net"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/sshx"
)

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
