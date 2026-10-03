// Command pod is the local agent of the web IDE: it serves the web app and gives it access
// to the disk, SSH, terminals, language servers and databases over a WebSocket.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/server"
	"github.com/DrSmithFr/web-ide/pod/internal/sessions"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
	"github.com/DrSmithFr/web-ide/pod/webdist"
)

func main() {
	home, _ := os.UserHomeDir()
	dataDir := flag.String("data", filepath.Join(home, ".web-ide"), "folder of the settings, projects and sessions")
	addr := flag.String("addr", "", "listen address (default: config.json, else "+config.DefaultAddr+")")
	workspace := flag.String("workspace", "", "default workspace (default: ~/Apps)")
	allowRemote := flag.Bool("allow-remote", false, "accept connections from other machines (protected by the token only)")
	static := flag.String("static", "", "serve the front end from this folder instead of the embedded one")
	flag.Parse()

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
	}
	if *static != "" {
		srv.Static = os.DirFS(*static)
	}
	srv.Init()

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

	fmt.Printf("Web IDE pod\n  data      : %s\n  workspace : %s\n  open      : http://%s/?token=%s\n", st.Dir(), cfg.Workspace, cfg.Addr, token)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
