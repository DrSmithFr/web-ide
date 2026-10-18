package relay

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RelayConfig is the "web-ide-pod relay" subcommand configuration.
type RelayConfig struct {
	DataDir       string // where relay.db lives ("" → ~/.web-ide-relay)
	Addr          string // HTTP listen address ("" → 127.0.0.1:4430)
	AdvertiseIP   string // STUN/TURN IPv4 given to clients ("" → auto-detect)
	AdvertiseIPv6 string // STUN/TURN IPv6 given to clients ("" → auto-detect, off without a global address)
	TurnPort      int    // STUN/TURN control port (0 → 3478)
	TurnMinPort   uint16 // first port of the range the TURN relay sockets are bound in (0 → any free port)
	TurnMaxPort   uint16 // last port of that range (inclusive)
	TLSPort       int    // TLS port shared with HTTPS and TURN over TLS (0 → 443)
	CertFile      string // certificate for TLS (a self-signed one is created on first run)
	KeyFile       string
	Domain        string // serve this domain: the certificate comes from Let's Encrypt (autocert)
	ACMEStaging   bool   // ask the Let's Encrypt test CA instead (bring a domain up without spending the production limits)
	Quota         int64  // bytes one account may move through TURN in a month (0 → no limit)
	QuotaBacker   int64  // the quota of a Backer (0 → twice Quota)
	// LevelPoll: how often the relay looks for a level that moved without it, a VIP the CLI
	// added or an end that passed (0 → 30 s; the e2e shortens it).
	LevelPoll time.Duration
	// TurnTTL: the validity of the TURN credentials (0 → 1 h; the e2e shortens it to see a tunnel
	// renew them).
	TurnTTL time.Duration
	// GitHub: the OAuth app and the maintainer token behind the sponsor levels (github.go).
	// GitHubEnv is a file of KEY=VALUE lines (0600) they can come from instead of the process
	// environment: /etc/antennae/env on the hosted relay.
	GitHub    GitHubConfig
	GitHubEnv string
	App       fs.FS  // the front end of the pod: the manifest and the icons of the installable app
	Version   string // what /_relay/status reports (the version of the relay)
	// PushSubject is the contact of the VAPID claims (mailto: or https:); PushTestEndpoint lets
	// the endpoints under this prefix through, for the e2e fake push service.
	PushSubject      string
	PushTestEndpoint string
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
	if cfg.AdvertiseIPv6 == "" {
		cfg.AdvertiseIPv6 = detectIPv6()
	}
	if cfg.TLSPort == 0 {
		cfg.TLSPort = 443
	}
	st, err := OpenStore(filepath.Join(dataDir, "relay.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	// TLS on 443: the same port serves the bootstrap page (https), TURN over TLS (RFC 8489)
	// and the TLS-ALPN-01 checks of Let's Encrypt, demuxed by the ALPN protocol of the
	// ClientHello. A browser that cannot reach the control port still reaches TURN there.
	turnCfg := TurnConfig{
		AdvertiseIP:   cfg.AdvertiseIP,
		AdvertiseIPv6: cfg.AdvertiseIPv6,
		Domain:        cfg.Domain,
		Port:          cfg.TurnPort,
		TLSPort:       cfg.TLSPort,
		MinPort:       cfg.TurnMinPort,
		MaxPort:       cfg.TurnMaxPort,
		Quota:         cfg.Quota,
		QuotaBacker:   cfg.QuotaBacker,
		TTL:           cfg.TurnTTL,
		// The level of an account sets its quota (level.go): its GitHub sponsorship and the VIP
		// list. It needs no GitHub app: a self-hosted relay has VIPs by account id.
		Level: func(key string) AccountLevel { return st.Level(key, time.Now()) },
	}
	var demux *Demux
	tcfg, acme, err := TLSConfig(dataDir, TLSOptions{
		CertFile: cfg.CertFile, KeyFile: cfg.KeyFile, Domain: cfg.Domain,
		ACMEStaging: cfg.ACMEStaging, Email: acmeEmail(cfg.PushSubject),
	})
	if err != nil {
		log.Printf("relay: tls: %v", err)
	} else if lns, err := tlsListeners(cfg); err != nil {
		hint := ""
		if cfg.TLSPort < 1024 {
			hint = " (TURN over TLS is off: a port below 1024 needs root, or use -tls-port)"
		}
		log.Printf("relay: tls on %d: %v%s", cfg.TLSPort, err, hint)
	} else {
		d, err := NewDemux(tcfg, lns[0])
		if err != nil {
			log.Printf("relay: tls demux on %d: %v", cfg.TLSPort, err)
			for _, ln := range lns {
				ln.Close()
			}
		} else {
			for _, ln := range lns[1:] {
				d.Serve(ln)
			}
			demux = d
			turnCfg.TLSListener = d.Turn
			defer d.Close()
		}
	}

	// The GitHub link: an account linked to a GitHub login that sponsors the project gets the
	// level of its tier. Without a GitHub app the relay has no sponsors (a self-hosted one).
	ghCfg := githubConfig(cfg)
	gh := NewGitHub(ghCfg, st)
	log.Print(githubNotice(ghCfg, readEnvFile(cfg.GitHubEnv), cfg.GitHubEnv))

	turn, err := NewTurn(st, turnCfg)
	if err != nil {
		return err
	}
	defer turn.Close()

	srv := New(st, turn)
	srv.App = cfg.App
	srv.Version = cfg.Version
	srv.GitHub = gh
	if gh != nil {
		gh.changed = srv.tellQuota
		go gh.refreshLoop(ctx)
	}
	go srv.watchLevels(ctx, cfg.LevelPoll)
	// Web Push: the VAPID key of the relay, made once in its data folder.
	if push, err := LoadPush(dataDir, cfg.PushSubject); err != nil {
		log.Printf("relay: push notifications are off: %v", err)
	} else {
		push.TestEndpoint = cfg.PushTestEndpoint
		srv.Push = push
	}
	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()
	open := "http://" + cfg.Addr
	if cfg.Domain != "" {
		open = "https://" + cfg.Domain
	}
	fmt.Printf("Web IDE relay\n  data      : %s\n  open      : %s/#pod=<id>&t=<token>\n  STUN/TURN : %s\n", st.DBPath(), open, strings.Join(turn.ICE(), " "))

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	if demux != nil {
		httpsSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { errCh <- httpsSrv.Serve(demux.HTTPS) }()
	}
	// A relay with a domain answers on port 80 too: the ACME http-01 challenges, and a redirect
	// to https for everything else.
	if acme != nil {
		http80 := &http.Server{Addr: net.JoinHostPort("0.0.0.0", "80"), Handler: acme.HTTPHandler(nil), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := http80.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("relay: http on 80: %v", err)
			}
		}()
		defer http80.Close()
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

// tlsListeners opens the TCP listeners of the TLS port: one per address of the machine the
// relay is advertised at, so the page, TURN over TLS and the ACME checks answer on IPv4 and on
// IPv6. With no address to name, the port is opened on every address of the machine.
func tlsListeners(cfg RelayConfig) ([]net.Listener, error) {
	var lns []net.Listener
	addrs := []string{cfg.AdvertiseIP}
	if cfg.AdvertiseIPv6 != "" {
		addrs = append(addrs, cfg.AdvertiseIPv6)
	}
	port := strconv.Itoa(cfg.TLSPort)
	for _, a := range addrs {
		if a == "" {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(a, port))
		if err != nil {
			for _, l := range lns {
				l.Close()
			}
			return nil, err
		}
		lns = append(lns, ln)
	}
	if len(lns) == 0 {
		ln, err := net.Listen("tcp", ":"+port)
		if err != nil {
			return nil, err
		}
		lns = append(lns, ln)
	}
	return lns, nil
}

// githubConfig reads what the GitHub link needs: the flags when they were given, else the
// process environment, else a file of KEY=VALUE lines (-github-env, /etc/antennae/env on the
// hosted relay, 0600: these are secrets and they never go in a command line, where a `ps`
// would read them).
func githubConfig(cfg RelayConfig) GitHubConfig {
	g := cfg.GitHub
	file := readEnvFile(cfg.GitHubEnv)
	pick := func(flagVal, key string) string {
		if flagVal != "" {
			return flagVal
		}
		if v := file[key]; v != "" {
			return v
		}
		return os.Getenv(key)
	}
	g.ClientID = pick(g.ClientID, "WEBIDE_GITHUB_CLIENT")
	g.ClientSecret = pick(g.ClientSecret, "WEBIDE_GITHUB_SECRET")
	g.Token = pick(g.Token, "WEBIDE_GITHUB_TOKEN")
	g.WebhookSecret = pick(g.WebhookSecret, "WEBIDE_GITHUB_WEBHOOK")
	return g
}

// readEnvFile reads a file of KEY=VALUE lines (none when path is empty or unreadable).
func readEnvFile(path string) map[string]string {
	file := map[string]string{}
	if path == "" {
		return file
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("relay: %s: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			file[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return file
}

// githubNotice is the line the relay logs at start about its GitHub link: whether it is on, what
// is missing, and the keys of the env file it does not read (GITHUB_CLIENT_ID… instead of
// WEBIDE_GITHUB_*, which left the hosted relay without a link and its webhook answered by the
// page with a 200).
func githubNotice(g GitHubConfig, file map[string]string, path string) string {
	if g.ClientID == "" || g.ClientSecret == "" {
		msg := "relay: no GitHub link, no supporters (WEBIDE_GITHUB_CLIENT and WEBIDE_GITHUB_SECRET are not set)"
		var stray []string
		for _, k := range []string{"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "GITHUB_TOKEN", "GITHUB_WEBHOOK_SECRET"} {
			if file[k] != "" {
				stray = append(stray, k)
			}
		}
		if len(stray) > 0 {
			msg += fmt.Sprintf(": %s names %s, which the relay does not read — rename them WEBIDE_GITHUB_CLIENT, _SECRET, _TOKEN, _WEBHOOK",
				path, strings.Join(stray, ", "))
		}
		return msg
	}
	msg := "relay: GitHub link on"
	if g.Token == "" {
		msg += "; no WEBIDE_GITHUB_TOKEN: the sponsor list is not read"
	}
	if g.WebhookSecret == "" {
		msg += "; no WEBIDE_GITHUB_WEBHOOK: the sponsorship webhook is off"
	}
	return msg
}

// acmeEmail is the contact Let's Encrypt may write to: the mailto: of -push-subject, when that
// subject is one.
func acmeEmail(subject string) string {
	if mailto := strings.TrimPrefix(subject, "mailto:"); mailto != subject {
		return mailto
	}
	return ""
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

// detectIPv6 returns the global IPv6 of this machine, when it has a route to one: a relay on a
// dual-stack VPS is reached by the IPv6 clients too, and TURN allocates on that address. ""
// when the machine has no IPv6 (the relay then stays IPv4 only, as before).
func detectIPv6() string {
	conn, err := net.Dial("udp", "[2001:4860:4860::8888]:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			ip := addr.IP
			if !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && ip.IsGlobalUnicast() {
				return ip.String()
			}
		}
	}
	return ""
}
