// Command pod is the local agent of the web IDE: it serves the web app and gives it access
// to the disk, SSH, terminals, language servers and databases over a WebSocket.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
	"github.com/DrSmithFr/web-ide/pod/internal/lan"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/multipod"
	"github.com/DrSmithFr/web-ide/pod/internal/pair"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/push"
	"github.com/DrSmithFr/web-ide/pod/internal/relay"
	"github.com/DrSmithFr/web-ide/pod/internal/relayclient"
	"github.com/DrSmithFr/web-ide/pod/internal/server"
	"github.com/DrSmithFr/web-ide/pod/internal/sessions"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/settingsync"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
	"github.com/DrSmithFr/web-ide/pod/internal/update"
	"github.com/DrSmithFr/web-ide/pod/webdist"
)

// version is set at build time (make build: git describe).
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "keeper" {
		runKeeper(os.Args[2:])
		return
	}
	home, _ := os.UserHomeDir()
	defaultData := filepath.Join(home, ".web-ide")
	dataDir := flag.String("data", defaultData, "folder of the settings, projects and sessions")
	addr := flag.String("addr", "", "listen address (default: config.json, else "+config.DefaultAddr+")")
	workspace := flag.String("workspace", "", "default workspace (default: ~/Apps)")
	allowRemote := flag.Bool("allow-remote", false, "accept connections from other machines (protected by the token only)")
	publicURL := flag.String("public-url", "", "address the IDE is opened at, for the links given to Claude Code (default: config.json, else http://<addr>)")
	static := flag.String("static", "", "serve the front end from this folder instead of the embedded one")
	keeperPath := flag.String("keeper", "", "socket of the keeper that runs the terminals (default: <data>/keeper.sock when it answers; off: the pod runs them)")
	name := flag.String("name", "", "name of this pod, shown to the other devices of the account (default: the host name)")
	mdns := flag.Bool("mdns", true, "find the other pods of the account on the LAN by mDNS (false: the addresses typed in Settings only)")
	lanAddr := flag.String("lan", "", "listen address of the link to the other pods of the account on the LAN (default: config.json, else "+lan.DefaultAddr+"; off: none)")
	relay := flag.String("relay", "", "relay URL the pod registers with (wss:// or ws://), or the word relay to enable")
	preferred := flag.Bool("preferred", false, "make this pod the preferred gateway of its site: the browser opens the other pods through it")
	multipodProtocol := flag.Int("multipod-protocol", multipod.Protocol, "the multi-pod protocol this pod speaks (tests: a pod of another one is listed with its version and never routed to)")
	podVersion := flag.String("pod-version", "", "the version this pod reports (a build of the source tree reports a development version no pod will install: name the release this build stands for)")
	forceTurn := flag.Bool("force-turn", false, "force the browser through TURN (no direct or STUN path)")
	turnPort := flag.Int("turn-port", 0, "relay: the STUN/TURN control port (default 3478)")
	turnIP := flag.String("turn-ip", "", "relay: the STUN/TURN IPv4 given to clients (default: the machine IP)")
	turnIP6 := flag.String("turn-ip6", "", "relay: the STUN/TURN IPv6 given to clients (default: the machine global IPv6, off without one)")
	turnPorts := flag.String("turn-ports", "", "relay: the port range the TURN relay sockets are bound in, 49160-49999 (default: any free port)")
	tlsPort := flag.Int("tls-port", 0, "relay: the TLS port shared with HTTPS and TURN over TLS (default 443; below 1024 needs root)")
	certFile := flag.String("cert", "", "relay: certificate for TLS (default: <data>/cert.pem, a self-signed one made on first run)")
	keyFile := flag.String("key", "", "relay: key of that certificate (default: <data>/key.pem)")
	domain := flag.String("domain", "", "relay: serve this domain: the certificate is issued and renewed by Let's Encrypt (TLS-ALPN-01), the ICE URLs name the domain")
	acmeStaging := flag.Bool("acme-staging", false, "relay: ask the Let's Encrypt test CA instead of the production one")
	quota := flag.String("quota", "", "relay: monthly TURN quota per account, 10GB (default: no limit, what a self-hosted relay wants)")
	githubEnv := flag.String("github-env", "", "relay: file of KEY=VALUE secrets for the GitHub link (/etc/antennae.env): WEBIDE_GITHUB_CLIENT, _SECRET, _TOKEN (sponsor list), _WEBHOOK")
	pushSubject := flag.String("push-subject", "", "relay: the contact of the Web Push (VAPID) claims, mailto: or https:")
	pushTest := flag.String("push-test-endpoint", "", "relay: also allow the push endpoints under this prefix (tests only)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *name != "" {
		podName = *name
	}
	if *podVersion != "" {
		version = *podVersion
	}
	if *showVersion {
		fmt.Println(version)
		return
	}
	// The multi-pod protocol this pod speaks (multipod.Protocol, unless a test asked for another
	// one): what the other pods compare to decide it can be routed to.
	protocol := *multipodProtocol
	if len(flag.Args()) > 0 && flag.Arg(0) == "relay" {
		// The relay has its own data folder (~/.web-ide-relay): pass "" when -data was not
		// set explicitly, else the pod's default would hold relay.db.
		// The relay serves the manifest and the icons of the installable app (PWA) from the
		// front end of the pod: one copy of them.
		var app fs.FS = webdist.FS()
		if *static != "" {
			app = os.DirFS(*static)
		}
		runRelayCmd(flagValue(flag.CommandLine, "data", ""), *addr, *turnPort, *turnIP, *turnIP6, *turnPorts, *tlsPort, *certFile, *keyFile, *domain, *acmeStaging, *quota, *githubEnv, app, *pushSubject, *pushTest)
		return
	}
	if len(flag.Args()) > 0 && flag.Arg(0) == "pair" {
		// Identity of the pod: create, restore, join or status. create, restore and status
		// work without a relay; join needs one.
		st, err := store.Open(config.ExpandHome(*dataDir))
		check(err)
		cfg, err := config.Load(st)
		check(err)
		relayURL := cfg.RelayURL
		if *relay != "" && *relay != "relay" {
			relayURL = strings.TrimSuffix(*relay, "/")
		}
		lanListen := cfg.LAN.Addr
		if *lanAddr != "" {
			lanListen = *lanAddr
		}
		check(pair.Run(&pair.Env{
			Dir:      st.Dir(),
			RelayURL: relayURL,
			LANAddr:  lanListen,
			MDNS:     *mdns,
			Name:     hostname(),
			In:       os.Stdin,
			Out:      os.Stdout,
		}, flag.Args()[1:]))
		return
	}

	st, err := store.Open(config.ExpandHome(*dataDir))
	check(err)
	cfg, err := config.Load(st)
	check(err)
	if *addr != "" {
		cfg.Addr = *addr
	}
	if *workspace != "" {
		cfg.Workspace = config.ExpandHome(*workspace)
	}
	if *publicURL != "" {
		cfg.PublicURL = strings.TrimSuffix(*publicURL, "/")
	}
	if *relay != "" && *relay != "relay" {
		cfg.RelayURL = strings.TrimSuffix(*relay, "/")
	}
	check(cfg.Save(st))
	token, err := config.Token(st)
	check(err)
	reg, err := projects.Load(st)
	check(err)
	sets, err := settings.Load(st)
	check(err)

	srv := &server.Server{
		Cfg:         cfg,
		Store:       st,
		Token:       token,
		Projects:    reg,
		Settings:    sets,
		Sessions:    sessions.New(st),
		LLM:         llm.New(st),
		Models:      hfcache.New(st.Path("models", "hf")),
		Pool:        sshx.NewPool(sshx.NewHostKeys(st.Path("known_hosts"))),
		Static:      webdist.FS(),
		AllowRemote: *allowRemote,
		Version:     version,
		RelayURL:    cfg.RelayURL,
		ForceTurn:   *forceTurn,
	}

	if *static != "" {
		srv.Static = os.DirFS(*static)
	}
	srv.Keeper = dialKeeper(*keeperPath, st.Path("keeper.sock"))
	if srv.Keeper != nil {
		srv.LLM.Relay = llm.KeeperRelay{C: srv.Keeper} // the answers being written survive the pod too
	}
	srv.Init()
	srv.ResumeRuns()

	// Remote access: one outbound link to the relay (no port to open). The pod id and its
	// secret live in relay.json (0600); the token is proved inside the tunnel, never to the
	// relay.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The pod identity: its device key and the account it pinned. An unpaired pod has a key
	// but no account and stays in token mode. The relay link and the LAN link share it.
	podID, err := identity.Open(st.Dir())
	check(err)
	if *lanAddr != "" {
		cfg.LAN.Addr = *lanAddr
	}
	if *preferred {
		cfg.Gateway.Preferred = true
	}
	// A newer revocation list reached over the LAN goes on to the relay.
	var relayCli atomic.Pointer[relayclient.Client]
	pushRevocations := func(r *identity.Revocations) {
		if c := relayCli.Load(); c != nil {
			c.PushRevocations(r)
		}
	}
	// The sync loop: the settings of the pods of one account, the vault when it is switched on, and
	// the project list of this pod. Only a paired pod syncs — it needs an account, a device key to
	// sign its writes, and the wrap an admin made for it. The sources read the files the pod already
	// keeps; the hub sends its messages on the relay link, or on a LAN link when the pod has none.
	var hub *settingsync.Hub
	var dir *multipod.Directory
	var pub *multipod.Debounce
	if podID.Paired() {
		flags := settingsync.NewFlags(sets)
		hub = settingsync.NewHub(st, nil, podID, flags)
		if _, err := hub.AddSource(settingsync.NewSettingsSource(sets, func() bool { return flags.On(identity.KindSettings) },
			func(e settings.Entry) { srv.Broadcast("settings.changed", e) })); err != nil {
			check(err)
		}
		if _, err := hub.AddSource(settingsync.NewVaultSource(st, srv.LLM, func() bool { return flags.On(identity.KindVault) },
			func() { srv.Broadcast("llm.config", srv.LLM.View()) })); err != nil {
			check(err)
		}
		src := multipod.NewProjectsSource(podID.ID(), hostname(), version, func() []multipod.Project { return projectList(st, reg) })
		if _, err := hub.AddSource(src); err != nil {
			check(err)
		}
		srv.Sync = hub
		// The handlers are registered before the hub exists, so the emitter is set here:
		// the page listens to sync.changed, sync.conflict and sync.error.
		hub.SetEmitter(func(st settingsync.Status, name string, payload any) {
			srv.Broadcast("sync."+name, map[string]any{"status": st, "payload": payload})
		})
	}
	// A blob a peer handed over goes on the relay link unchanged: the relay checks the signature of
	// the device that wrote it, so the hop adds no trust.
	forwardBlob := func(b *identity.BlobRecord) {
		if c := relayCli.Load(); c != nil {
			if err := c.PutBlob(b); err != nil {
				log.Printf("lan: forwarding the list of a pod: %v", err)
			}
		}
	}
	// The same for the X25519 key of a peer: an admin of the account wraps the key of the epoch
	// for it once the relay holds it.
	forwardSyncKey := func(k *identity.SyncKeyRecord) {
		if c := relayCli.Load(); c != nil {
			if err := c.PutSyncKey(k); err != nil {
				log.Printf("lan: forwarding the sync key of a pod: %v", err)
			}
		}
	}
	// Reading the cached lists of the other pods: it needs a link (the relay, or a peer that
	// carries the wrap), so it happens when one of them brought the key of the epoch.
	pullLists := func() {
		if dir == nil {
			return
		}
		if err := dir.Pull(); err != nil {
			log.Printf("relay: project lists: %v", err)
		}
	}
	var lanMgr *lan.Manager
	// What a pod does with an update request that reached it, over a LAN link or the relay: the
	// checks are the same whatever carried it.
	upd := update.New(podID)
	// What a change of the LAN links means: the page refreshes, the relay learns what this pod sees
	// (the sites), and a pod with no link of its own publishes its key and its list through a peer.
	onLANChange := func() {
		srv.LANChanged()
		// The blocks of the home page come from the LAN too: a peer that connects or drops has to
		// refresh them, even when the pod has no link to the relay.
		if dir != nil {
			dir.Changed()
		}
		if lanMgr == nil {
			return
		}
		keys := []string{}
		for _, p := range lanMgr.Status().Peers {
			keys = append(keys, p.ID)
		}
		if c := relayCli.Load(); c != nil {
			c.ReportSite(keys)
			return
		}
		if hub != nil && pub != nil && cfg.RelayURL == "" {
			// No relay link of its own: a peer carries the X25519 key of this device to an admin,
			// and the project list once the wrap came back.
			_ = hub.Publish()
			pub.Touch()
		}
	}
	if cfg.LAN.Addr != "off" {
		m := startLAN(ctx, st, cfg, srv, podID, *mdns, protocol, pushRevocations, forwardBlob, forwardSyncKey, pullLists, hub, &onLANChange, remoteGateway(&relayCli, &dir), upd.Run)
		if m != nil {
			srv.LAN = m
			lanMgr = m
		}
	}
	dir = multipod.New(hub, lanView{m: lanMgr})
	if hub != nil {
		hub.SetOther(func(m settingsync.Msg) { dir.HandleBlob(m) })
		// The key of the epoch arrived: this pod can publish its own list, and read the others.
		hub.SetOnKey(func() {
			pullLists()
			if pub != nil {
				pub.Touch()
			}
		})
		pub = multipod.NewDebounce(hub, identity.ProjectsKind(podID.ID()), 2*time.Second)
		srv.ProjectsChanged = pub.Touch
		if cfg.RelayURL == "" && lanMgr != nil {
			// A pod with no link to the relay sends its sync messages through a peer of its site.
			hub.SetTransport(lanTransport{m: lanMgr})
		}
	}
	// A pod with a link of its own dials the pods of another site pod to pod: their page runs
	// under its own origin too (pod/internal/multipod.RemoteGateway).
	dir.SetRemote(cfg.RelayURL != "")
	dir.SetEmitter(srv.PodsChanged)
	srv.Pods = &podsView{dir: dir, cfg: cfg, st: st, relayCli: &relayCli}
	srv.Pods.(*podsView).lan = lanMgr
	onLANChange()
	remote := ""
	if cfg.RelayURL != "" {
		ids, err := config.Relay(st)
		check(err)
		var state atomic.Value
		state.Store("connecting")
		srv.RelayStatus = func() server.RelayInfo {
			info := server.RelayInfo{URL: cfg.RelayURL, PodID: ids.PodID, Link: relayLink(cfg.RelayURL, ids.PodID, token), State: state.Load().(string)}
			if c := relayCli.Load(); c != nil {
				if q := c.Quota(); q != nil {
					info.TurnUsed, info.TurnLimit, info.TurnOver, info.TurnSupporter = q.Used, q.Limit, q.Over, q.Supporter
				}
			}
			return info
		}

		cli := relayclient.New(relayclient.Config{
			RelayURL:  cfg.RelayURL,
			PodID:     ids.PodID,
			Secret:    ids.Secret,
			Name:      hostname(),
			Server:    srv,
			Token:     token,
			Identity:  podID,
			Sync:      srv.Sync,
			Version:   version,
			Protocol:  protocol,
			Preferred: cfg.Gateway.Preferred,
			Route:     podRoute(lanMgr),
			OnUpdate:  upd.Run,
			ForceTurn: *forceTurn,
			OnState: func(s string) {
				state.Store(s)
				srv.Broadcast("relay.changed", srv.RelayStatus())

			},
			OnReady: func() {
				// The link came up and the sync loop published: read the lists the relay cached
				// (a push only reaches a pod that was ready to read it at that moment), and write
				// this pod's own list if it never could.
				pullLists()
				if pub != nil {
					pub.Touch()
				}
			},
			OnPods: func(p []relayclient.PodInfo) {
				if dir != nil {
					dir.RelayPods(p)
				}
			},
			OnGateway: func(site, key string) {
				if dir != nil {
					dir.RelayGateway(site, key)
				}
			},
			OnForeignWrap: func(w *identity.WrapRecord) {
				// The wrap is for a pod of this site that has no link of its own: it comes back the
				// way its key went out.
				if lanMgr != nil {
					lanMgr.PassWrap(w)
				}
			},
			OnPushGone: func(sub string) {
				if srv.Push != nil {
					srv.Push.Gone(sub)
				}
			},
		})
		// Push notifications go through the relay, encrypted by the pod for each browser.
		if subs, err := push.Open(st.Dir()); err != nil {
			log.Printf("push: %v", err)
		} else {
			srv.Push, srv.PushSend = subs, cli.Push
		}
		relayCli.Store(cli)
		go func() { _ = cli.Run(ctx) }()
		remote = relayLink(cfg.RelayURL, ids.PodID, token)
	}

	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Print("stopping the pod…")
		srv.Shutdown()
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(cctx)
	}()

	fmt.Printf("Web IDE pod %s\n  data      : %s\n  workspace : %s\n  open      : http://%s/?token=%s\n", version, st.Dir(), cfg.Workspace, cfg.Addr, token)
	if remote != "" {
		fmt.Printf("  remote    : %s\n", remote)
	}
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// remoteGateway serves the pods of another site under this pod's /_lan/pod/ path, over a
// pod-to-pod WebRTC tunnel. The relay link and the directory of the pods come up later, so the
// dialer reads them when a page asks (Settings > Remote access, relay.md).
func remoteGateway(cli *atomic.Pointer[relayclient.Client], dir **multipod.Directory) http.Handler {
	return multipod.NewRemoteGateway(relayDialer{cli: cli, dir: dir}, func(key string) (string, string, int) {
		if d := *dir; d != nil {
			return d.GapOf(key)
		}
		return "", "", 0
	})
}

// relayDialer dials a pod of the account the relay reports online, by its device key.
type relayDialer struct {
	cli *atomic.Pointer[relayclient.Client]
	dir **multipod.Directory
}

func (d relayDialer) Dial(ctx context.Context, key string) (multipod.RemotePod, error) {
	cli, dir := d.cli.Load(), *d.dir
	if cli == nil || dir == nil {
		return nil, errors.New("this pod has no link to the relay")
	}
	// A pod of another multi-pod protocol is never routed to, pod to pod either.
	if !dir.Routeable(key) {
		return nil, fmt.Errorf("%s runs another multi-pod protocol: update it", key)
	}
	podID := dir.PodID(key)
	if podID == "" {
		return nil, fmt.Errorf("no pod online at the relay for %s", key)
	}
	return cli.Dial(ctx, podID, key, nil)
}

// podRoute is how this pod reaches the pods of its site that have no link of their own: a
// browser tunneled to it gets their channels through the LAN link.
func podRoute(m *lan.Manager) *multipod.Route {
	if m == nil {
		return multipod.NewRoute(nil)
	}
	return multipod.NewRoute(m)
}

// startLAN runs the link to the other pods of the account on the local network: the LAN
// port, mDNS, and the page of each pod found, served under /_lan/pod/<key>/.
func startLAN(ctx context.Context, st *store.Store, cfg *config.Config, srv *server.Server, podID *identity.PodIdentity, mdns bool, protocol int,
	onRevocations func(*identity.Revocations), onBlob func(*identity.BlobRecord), onSync func(*identity.SyncKeyRecord),
	onKeyReady func(), hub *settingsync.Hub, onChange *func(), remote http.Handler, onUpdate func(*identity.UpdateRequest) update.Answer) *lan.Manager {
	m, err := lan.New(lan.Config{
		Dir:         st.Dir(),
		Addr:        cfg.LAN.Addr,
		Identity:    podID,
		Name:        hostname(),
		Version:     version,
		Protocol:    protocol,
		Handler:     http.HandlerFunc(srv.ServeTrusted),
		ServeTunnel: srv.ServeTunnel,
		Remote:      remote,
		Peers:       cfg.LAN.Peers,
		SavePeers: func(peers []string) error {
			cfg.LAN.Peers = peers
			return cfg.Save(st)
		},
		OnChange:      func() { (*onChange)() },
		OnRevocations: onRevocations,
		OnBlob:        onBlob,
		OnSync:        onSync,
		OnUpdate:      onUpdate,
		OnWrap: func(w *identity.WrapRecord) {
			// The wrap the relay pushed for this pod, carried back by a peer of its site: it is the
			// only way a pod with no link of its own gets the key of the epoch.
			if hub == nil {
				return
			}
			if err := hub.SetWrap(w); err != nil {
				log.Printf("lan: wrap: %v", err)
			} else if onKeyReady != nil {
				onKeyReady()
			}
		},
	})
	if err != nil {
		log.Printf("lan: %v", err)
		return nil
	}
	if mdns {
		m.SetResolver(lan.MDNS{})
	}
	m.Start(ctx)
	return m
}

// relayLink is the address the user opens from another network: the relay, the pod, the
// token. The token is in the hash so it is not sent to the relay.
func relayLink(relayURL, podID, token string) string {
	u, err := url.Parse(relayURL)
	if err != nil {
		return relayURL + "/?pod=" + podID + "&t=" + token
	}
	u.Path = "/"
	return u.String() + "#pod=" + podID + "&t=" + token
}

// podName is the name of the pod (-name), the host name by default.
var podName string

func hostname() string {
	if podName != "" {
		return podName
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// dialKeeper connects to the keeper, or returns nil: the pod then runs the terminals itself.
func dialKeeper(path, def string) *keeper.Client {
	if path == "off" {
		log.Print("keeper: off, the pod runs the terminals")
		return nil
	}
	explicit := path != ""
	if !explicit {
		path = def
		if _, err := os.Stat(path); err != nil {
			log.Print("keeper: none, the pod runs the terminals (they end with it)")
			return nil
		}
	}
	c, h, err := keeper.Dial(path)
	if err != nil {
		log.Printf("keeper: %s does not answer (%v), the pod runs the terminals (they end with it)", path, err)
		return nil
	}
	log.Printf("keeper: %s (pid %d, %s): the terminals survive the updates of the pod", path, h.Pid, h.Version)
	return c
}

// runKeeper is the keeper service: web-ide-pod keeper [-data DIR] [-protocol].
func runKeeper(args []string) {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("keeper", flag.ExitOnError)
	dataDir := fs.String("data", filepath.Join(home, ".web-ide"), "folder of the settings (the socket is keeper.sock in it)")
	socket := fs.String("socket", "", "socket path (default: <data>/keeper.sock)")
	protocol := fs.Bool("protocol", false, "print the protocol of the keeper and exit")
	stateVersion := fs.Bool("state-version", false, "print the version of the state file it reads (updates by re-exec) and exit")
	upgrade := fs.Bool("upgrade", false, "update the running keeper: it re-executes its binary, keeping its processes")
	binary := fs.String("binary", "", "with -upgrade: the binary to re-execute (default: the keeper's own)")
	force := fs.Bool("force", false, "with -upgrade: cancel what cannot cross the update instead of waiting for it")
	wait := fs.Duration("wait", 10*time.Minute, "with -upgrade: how long to wait for what cannot cross the update")
	_ = fs.Parse(args)
	switch {
	case *protocol:
		fmt.Println(keeper.Protocol)
		return
	case *stateVersion:
		fmt.Println(keeper.StateVersion)
		return
	}
	path := *socket
	if path == "" {
		path = filepath.Join(config.ExpandHome(*dataDir), "keeper.sock")
	}
	if *upgrade {
		upgradeKeeper(path, keeper.Upgrade{Path: *binary, Force: *force, Wait: *wait})
		return
	}
	var srv *keeper.Server
	var ln net.Listener
	var err error
	if state := os.Getenv(keeper.StateEnv); state != "" {
		srv, ln, err = keeper.Restore(state, version)
	} else if ln, err = keeper.Listen(path); err == nil {
		srv = keeper.NewServer(version)
	}
	check(err)
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		for s := range sig {
			if s == syscall.SIGHUP {
				if err := srv.UpgradeSelf(); err != nil {
					log.Printf("keeper: not updated: %v", err)
				}
				continue
			}
			log.Print("keeper: stopping, its processes end")
			srv.Close()
			return
		}
	}()
	log.Printf("keeper %s (protocol %d) on %s", version, keeper.Protocol, path)
	check(srv.Serve(ln))
}

// upgradeKeeper asks the keeper at path to re-execute itself, shows its progress and checks it
// came back with the same pid. Exit code 2: the keeper cannot update itself (restart it).
func upgradeKeeper(path string, u keeper.Upgrade) {
	c, before, err := keeper.DialAny(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keeper not reachable:", err)
		os.Exit(2)
	}
	err = c.Upgrade(u, func(line string) { fmt.Println("keeper:", line) })
	c.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "keeper not updated:", err)
		os.Exit(2)
	}
	for i := 0; i < 100; i++ {
		time.Sleep(100 * time.Millisecond)
		c, after, err := keeper.DialAny(path)
		if err != nil {
			continue
		}
		c.Close()
		if after.Pid != before.Pid {
			fmt.Fprintf(os.Stderr, "keeper restarted (pid %d → %d): its processes are lost\n", before.Pid, after.Pid)
			os.Exit(1)
		}
		fmt.Printf("keeper updated in place (pid %d): %s, protocol %d → %s, protocol %d\n", after.Pid, before.Version, before.Protocol, after.Version, after.Protocol)
		return
	}
	fmt.Fprintln(os.Stderr, "the keeper does not answer after its update")
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// flagValue returns the value of a flag only when it was set on the command line
// (else def): the relay keeps its own default data folder.
func flagValue(fs *flag.FlagSet, name, def string) string {
	v := def
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			v = f.Value.String()
		}
	})
	return v
}

// projectList is what this pod publishes for its own projects: the titles the pod knows, never the
// content of the projects. The icons travel with them (they are shown by the home page of a browser
// that never reaches this pod), and are dropped first when the whole blob grows too big.
func projectList(st *store.Store, reg *projects.Registry) []multipod.Project {
	out := []multipod.Project{}
	for _, v := range reg.List() {
		p := multipod.Project{ID: v.ID, Title: v.Name(), Description: v.Description, Type: v.Type, Path: v.Path}
		if svg, err := st.ReadFile("icons/" + v.ID + ".svg"); err == nil && len(svg) <= multipod.MaxIconSize {
			p.Icon = string(svg)
		}
		out = append(out, p)
	}
	return multipod.Fit(out)
}

// lanView is what the directory of the pods sees of the LAN: the peers this pod is linked to.
type lanView struct{ m *lan.Manager }

func (v lanView) Status() lan.Status {
	if v.m == nil {
		return lan.Status{}
	}
	return v.m.Status()
}

// lanTransport sends the sync messages of a pod that has no link to the relay: a peer of its site
// carries them on its own link. Reads cannot be relayed, so a peer answers them for its site.
type lanTransport struct{ m *lan.Manager }

func (t lanTransport) Send(msg settingsync.Msg) error {
	switch msg.Type {
	case "blobPut":
		return t.m.PublishBlob(msg.Blob)
	case "syncKey":
		return t.m.PublishSyncKey(msg.SyncKey)
	}
	return nil
}

// podsView is the home page's view of the other pods: the merged directory, and the gateway settings.
type podsView struct {
	dir      *multipod.Directory
	cfg      *config.Config
	st       *store.Store
	lan      *lan.Manager
	relayCli *atomic.Pointer[relayclient.Client]
}

func (v *podsView) List() any { return v.dir.List() }
func (v *podsView) Gateway() string {
	return v.dir.Gateway()
}
func (v *podsView) Site() string    { return v.dir.Site() }
func (v *podsView) Preferred() bool { return v.cfg.Gateway.Preferred }

func (v *podsView) SetPreferred(on bool) error {
	v.cfg.Gateway.Preferred = on
	if err := v.cfg.Save(v.st); err != nil {
		return err
	}
	if c := v.relayCli.Load(); c != nil {
		c.SetPreferred(on)
	}
	return nil
}

// CanUpdate: a pod offers another pod the version it runs itself, and only a release version is
// worth installing.
func (v *podsView) CanUpdate() bool { return identity.IsReleaseTag(version) }

// Update carries a signed update request to the pod it names: over the LAN link when it is a peer
// of the site, else through the relay. The answer is what that pod said — this pod adds the route,
// not the trust.
func (v *podsView) Update(ctx context.Context, req *identity.UpdateRequest) (any, error) {
	if v.lan != nil {
		if ans, err := v.lan.SendUpdate(ctx, req.Pod, req); err == nil {
			return ans, nil
		}
	}
	if c := v.relayCli.Load(); c != nil {
		return c.SendUpdate(ctx, req.Pod, req)
	}
	return nil, i18n.New("this pod reaches no other pod")
}

// runRelayCmd runs the relay server subcommand until interrupted.
func runRelayCmd(dataDir string, addr string, turnPort int, turnIP, turnIP6, turnPorts string, tlsPort int, certFile, keyFile, domain string, acmeStaging bool, quota, githubEnv string, app fs.FS, pushSubject, pushTest string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	minPort, maxPort := turnPortRange(turnPorts)
	bytes, err := parseSize(quota)
	if err != nil {
		log.Printf("relay: -quota %q: %v (no limit)", quota, err)
	}
	check(relay.Run(ctx, relay.RelayConfig{
		DataDir: dataDir, Addr: addr, TurnPort: turnPort, AdvertiseIP: turnIP, AdvertiseIPv6: turnIP6,
		TurnMinPort: minPort, TurnMaxPort: maxPort, TLSPort: tlsPort,
		CertFile: certFile, KeyFile: keyFile, Domain: domain, ACMEStaging: acmeStaging, Quota: bytes,
		GitHubEnv: githubEnv,
		App:       app, Version: version, PushSubject: pushSubject, PushTestEndpoint: pushTest,
	}))
}

// parseSize reads a byte count with a unit suffix ("10GB", "1MB", "500k"); empty is no limit.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	units := map[string]int64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}
	mult := int64(1)
	if last := s[len(s)-1]; last >= 'a' && last <= 'z' {
		key := strings.ToUpper(string(last))
		u, ok := units[key]
		if !ok {
			return 0, fmt.Errorf("unknown unit %q", string(last))
		}
		mult = u
		s = strings.TrimSpace(s[:len(s)-1])
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not a number")
	}
	return int64(n * float64(mult)), nil
}

// turnPortRange reads the -turn-ports range ("49160-49999"): the ports the TURN relay sockets
// are bound in, so a firewall can name a range instead of the whole 1024-65535. Empty or
// malformed: no range, any free port (the default).
func turnPortRange(s string) (uint16, uint16) {
	lo, hi, ok := strings.Cut(s, "-")
	if s == "" || !ok {
		return 0, 0
	}
	a, err1 := strconv.Atoi(strings.TrimSpace(lo))
	b, err2 := strconv.Atoi(strings.TrimSpace(hi))
	if err1 != nil || err2 != nil || a <= 0 || b < a || a > 65535 || b > 65535 {
		return 0, 0
	}
	return uint16(a), uint16(b)
}
