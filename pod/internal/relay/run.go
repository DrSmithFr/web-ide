package relay

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// RelayConfig is the "web-ide-pod relay" subcommand configuration.
type RelayConfig struct {
	DataDir     string // where relay.db lives ("" → ~/.web-ide-relay)
	Addr        string // HTTP listen address ("" → 127.0.0.1:4430)
	AdvertiseIP string // STUN/TURN IP given to clients ("" → auto-detect)
	TurnPort    int    // STUN/TURN control port (0 → 3478)
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
	st, err := OpenStore(filepath.Join(dataDir, "relay.db"))
	if err != nil {
		return err
	}
	turn, err := NewTurn(st, TurnConfig{AdvertiseIP: cfg.AdvertiseIP, Port: cfg.TurnPort})
	if err != nil {
		st.Close()
		return err
	}
	srv := New(st, turn)
	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()
	fmt.Printf("Web IDE relay\n  data      : %s\n  open      : http://%s/?pod=<id>&t=<token>\n  STUN/TURN : %s\n", st.DBPath(), cfg.Addr, turn.ICE())
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	select {
	case err := <-errCh:
		turn.Close()
		st.Close()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	turn.Close()
	st.Close()
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
