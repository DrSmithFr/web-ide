// Command pod is the local agent of the web IDE: it serves the web app and gives it access
// to the disk, SSH, terminals, language servers and databases over a WebSocket.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"
	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/server"
	"github.com/DrSmithFr/web-ide/pod/internal/sessions"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
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
	dataDir := flag.String("data", filepath.Join(home, ".web-ide"), "folder of the settings, projects and sessions")
	addr := flag.String("addr", "", "listen address (default: config.json, else "+config.DefaultAddr+")")
	workspace := flag.String("workspace", "", "default workspace (default: ~/Apps)")
	allowRemote := flag.Bool("allow-remote", false, "accept connections from other machines (protected by the token only)")
	publicURL := flag.String("public-url", "", "address the IDE is opened at, for the links given to Claude Code (default: config.json, else http://<addr>)")
	static := flag.String("static", "", "serve the front end from this folder instead of the embedded one")
	keeperPath := flag.String("keeper", "", "socket of the keeper that runs the terminals (default: <data>/keeper.sock when it answers; off: the pod runs them)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
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

	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Print("stopping the pod…")
		srv.Shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	fmt.Printf("Web IDE pod %s\n  data      : %s\n  workspace : %s\n  open      : http://%s/?token=%s\n", version, st.Dir(), cfg.Workspace, cfg.Addr, token)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
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
