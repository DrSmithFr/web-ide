package relay

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// RelayConfig is the "web-ide-pod relay" subcommand configuration.
type RelayConfig struct {
	DataDir     string // where relay.db lives ("" → ~/.web-ide-relay)
	Addr        string // HTTP listen address ("" → 127.0.0.1:4430)
	AdvertiseIP string // STUN/TURN IP given to clients ("" → auto-detect)
	TurnPort    int    // STUN/TURN control port (0 → 3478)
	TLSPort     int    // TLS port shared with HTTPS and TURN over TLS (0 → 443)
	CertFile    string // certificate for TLS (a self-signed one is created on first run)
	KeyFile     string
}

// Run starts the relay server (the "relay" subcommand) until ctx is cancelled.
func Run(ctx context.Context, cfg RelayConfig) error {
	dataDir := cfg.DataDir
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".web-ide-relay")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:4430"
	}
	if cfg.AdvertiseIP == "" {
		cfg.AdvertiseIP = detectIP()
	}
	if cfg.TLSPort == 0 {
		cfg.TLSPort = 443
	}
	st, err := OpenStore(filepath.Join(dataDir, "relay.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	// TLS on 443: the same port serves the bootstrap page (https) and TURN over TLS
	// (RFC 8489), demuxed by the ALPN protocol of the ClientHello. A browser that cannot
	// reach the control port still reaches TURN there.
	turnCfg := TurnConfig{AdvertiseIP: cfg.AdvertiseIP, Port: cfg.TurnPort}
	var demux *Demux
	if tcfg, err := TLSConfig(dataDir, cfg.CertFile, cfg.KeyFile); err == nil {
		ln, err := net.Listen("tcp", net.JoinHostPort(cfg.AdvertiseIP, strconv.Itoa(cfg.TLSPort)))
		if err != nil {
			hint := ""
			if cfg.TLSPort < 1024 {
				hint = " (TURN over TLS is off: a port below 1024 needs root, or use -tls-port)"
			}
			log.Printf("relay: tls on %d: %v%s", cfg.TLSPort, err, hint)
		} else if d, err := NewDemux(tcfg, ln); err != nil {
			log.Printf("relay: tls demux on %d: %v", cfg.TLSPort, err)
			ln.Close()
		} else {
			demux = d
			turnCfg.TLSListener = d.Turn
			turnCfg.TLSURL = "turn:" + net.JoinHostPort(cfg.AdvertiseIP, strconv.Itoa(cfg.TLSPort)) + "?transport=tcp"
			defer d.Close()
		}
	}

	turn, err := NewTurn(st, turnCfg)
	if err != nil {
		return err
	}
	defer turn.Close()

	srv := New(st, turn)
	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()
	fmt.Printf("Web IDE relay\n  data      : %s\n  open      : http://%s/#pod=<id>&t=<token>\n  STUN/TURN : %s\n", st.DBPath(), cfg.Addr, turn.ICE())

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	if demux != nil {
		httpsSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { errCh <- httpsSrv.Serve(demux.HTTPS) }()
	}

	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	log.Print("stopping the relay…")
	return nil
}

// detectIP returns the first non-loopback IPv4 of this machine (the clients reach TURN
// and STUN here). On a machine with none, 127.0.0.1 is returned (loopback only).
func detectIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && !addr.IP.IsLoopback() {
			return addr.IP.String()
		}
	}
	return "127.0.0.1"
}
