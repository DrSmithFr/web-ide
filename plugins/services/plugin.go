// Package services is the plugin of the shared services (docs/design/relay.md, *Services*): an
// HTTP service of this machine (Ollama, a web UI) the other pods of the account reach. Pod-wide,
// like the previews: the services are the pod's (services.json), its methods are global and its
// part of the page is a section of the home page. The core lets only the pods of the account
// reach its handler (Host.HandlePeer, /_peer/services/), and publishes the services that are on
// in the project list and the LAN $info (Host.Advertise).
package services

import (
	"context"
	_ "embed"
	"encoding/json"
	"log"

	"github.com/DrSmithFr/web-ide/pod/plugins"
)

//go:embed plugin.json
var manifest []byte

type Plugin struct{ m plugins.Manifest }

func New() *Plugin {
	m, err := plugins.Parse(manifest)
	if err != nil {
		panic(err)
	}
	return &Plugin{m: m}
}

func (p *Plugin) Manifest() plugins.Manifest { return p.m }

func bind[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

func (p *Plugin) Register(h plugins.Host) {
	pod := h.Pod()
	var m *Manager
	m, err := Load(pod.Services().Store, func() {
		pod.Broadcast("services.changed", m.List())
		pod.AdvertiseChanged()
	})
	if err != nil {
		log.Printf("services: %s: %v", File, err)
	}
	h.HandlePeer(m.Handler())
	h.Advertise("services", func() any { return m.Published() })

	h.HandleGlobal("services.list", func(ctx context.Context, c plugins.Call) (any, error) {
		return m.List(), nil
	})
	h.HandleGlobal("services.save", func(ctx context.Context, c plugins.Call) (any, error) {
		s, err := bind[Service](c.Params)
		if err != nil {
			return nil, err
		}
		return m.Save(s)
	})
	h.HandleGlobal("services.delete", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		return nil, m.Delete(a.ID)
	})
	// Cut detaches a pod: its next request is refused (#79 tells it, its link shows cut).
	h.HandleGlobal("services.cut", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct {
			ID  string `json:"id"`
			Pod string `json:"pod"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		m.Detach(a.ID, a.Pod)
		return m.List(), nil
	})
}
