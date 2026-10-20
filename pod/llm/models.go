package llm

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Caps are the inputs and features a model accepts.
type Caps struct {
	Vision   bool `json:"vision"`
	Video    bool `json:"video"`
	Audio    bool `json:"audio"`
	Tools    bool `json:"tools"`
	Thinking bool `json:"thinking"`
	// Known is false when the server could not tell (model not loaded): the page then
	// lets the user try.
	Known bool `json:"known"`
}

type Model struct {
	ID      string `json:"id"`
	Size    int64  `json:"size,omitempty"`
	Details string `json:"details,omitempty"`
	// State: "loaded", "unloaded" or "" when the server does not say.
	State   string `json:"state,omitempty"`
	Context int    `json:"context,omitempty"`
	Caps    Caps   `json:"caps"`
}

type ModelList struct {
	Kind   string  `json:"kind"`
	Models []Model `json:"models"`
}

func (m *Manager) Models(ctx context.Context, serverID string) (*ModelList, error) {
	s, err := m.server(serverID)
	if err != nil {
		return nil, err
	}
	kind := m.kind(ctx, s)
	var models []Model
	switch kind {
	case "ollama":
		models, err = m.ollamaModels(ctx, s)
	case "openai":
		models, err = m.openaiModels(ctx, s)
		if err != nil && len(s.Models) > 0 {
			models, err = nil, nil // no /models: the typed ones
		}
	default:
		models, err = m.llamacppModels(ctx, s)
	}
	if err != nil {
		return nil, err
	}
	models = withTyped(models, s.Models)
	sort.SliceStable(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
	return &ModelList{Kind: kind, Models: models}, nil
}

// ---------- llama.cpp (and other OpenAI compatible servers) ----------

type llamaProps struct {
	Role       string `json:"role"`
	Modalities *struct {
		Vision bool `json:"vision"`
		Video  bool `json:"video"`
		Audio  bool `json:"audio"`
	} `json:"modalities"`
	Caps *struct {
		Tools bool `json:"supports_tools"`
	} `json:"chat_template_caps"`
	Settings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
}

func (p *llamaProps) apply(md *Model) {
	if p.Modalities != nil {
		md.Caps.Vision, md.Caps.Video, md.Caps.Audio = p.Modalities.Vision, p.Modalities.Video, p.Modalities.Audio
		md.Caps.Known = true
	}
	// Without the template capabilities (older servers), tools are assumed available.
	md.Caps.Tools = p.Caps == nil || p.Caps.Tools
	md.Caps.Thinking = true // reasoning_content is shown when the model sends it
	if p.Settings.NCtx > 0 {
		md.Context = p.Settings.NCtx
	}
}

func (m *Manager) llamacppModels(ctx context.Context, s Server) ([]Model, error) {
	var list struct {
		Data []struct {
			ID     string `json:"id"`
			Status *struct {
				Value string   `json:"value"`
				Args  []string `json:"args"`
			} `json:"status"`
			Meta *struct {
				NCtx   int   `json:"n_ctx"`
				Params int64 `json:"n_params"`
				Size   int64 `json:"size"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := m.getJSON(ctx, s, "/v1/models", &list); err != nil {
		return nil, err
	}
	var props llamaProps
	hasProps := m.getJSON(ctx, s, "/props", &props) == nil
	router := hasProps && props.Role == "router"

	models := make([]Model, len(list.Data))
	var wg sync.WaitGroup
	for i, d := range list.Data {
		md := Model{ID: d.ID, Caps: Caps{Tools: true, Thinking: true}}
		if d.Meta != nil {
			md.Context, md.Size = d.Meta.NCtx, d.Meta.Size
			if d.Meta.Params > 0 {
				md.Details = humanParams(d.Meta.Params)
			}
		}
		switch {
		case d.Status != nil:
			// Router mode: one entry per preset, loaded on demand. Asking the properties
			// of an unloaded model would load it, so they are guessed from its arguments.
			md.State = d.Status.Value
			if d.Status.Value == "loaded" {
				wg.Add(1)
				go func(i int, md Model) {
					defer wg.Done()
					var p llamaProps
					if m.getJSON(ctx, s, "/props?model="+url.QueryEscape(md.ID), &p) == nil {
						p.apply(&md)
					}
					models[i] = md
				}(i, md)
				continue
			}
			for _, a := range d.Status.Args {
				if a == "--mmproj" || a == "-mm" || a == "--mmproj-url" || a == "-mmu" {
					md.Caps.Vision = true
				}
			}
		case hasProps && !router:
			md.State = "loaded"
			props.apply(&md)
		}
		models[i] = md
	}
	wg.Wait()
	return models, nil
}

func humanParams(n int64) string {
	switch {
	case n >= 1e9:
		return trimFloat(float64(n)/1e9) + "B"
	case n >= 1e6:
		return trimFloat(float64(n)/1e6) + "M"
	}
	return ""
}

func trimFloat(f float64) string {
	prec := 1
	if f >= 100 {
		prec = 0
	}
	return strings.TrimSuffix(strconv.FormatFloat(f, 'f', prec, 64), ".0")
}

// ---------- typed models ----------

// withTyped adds the models typed by the user, whose capabilities win over the listed ones.
func withTyped(models []Model, typed []ModelConf) []Model {
	for _, t := range typed {
		if strings.TrimSpace(t.ID) == "" {
			continue
		}
		md := Model{ID: t.ID, Context: t.Context, Caps: Caps{Tools: t.Tools, Vision: t.Vision, Thinking: t.Thinking, Known: true}}
		found := false
		for i := range models {
			if models[i].ID == t.ID {
				if md.Context == 0 {
					md.Context = models[i].Context
				}
				md.Size, md.Details, md.State = models[i].Size, models[i].Details, models[i].State
				models[i], found = md, true
			}
		}
		if !found {
			models = append(models, md)
		}
	}
	return models
}

// ---------- OpenAI-compatible providers ----------

// DefaultContext of a model of a provider that does not tell it.
const DefaultContext = 128000

func (m *Manager) openaiModels(ctx context.Context, s Server) ([]Model, error) {
	// OpenRouter adds the context size and the input modalities; llama-swap the state.
	var list struct {
		Data []struct {
			ID           string `json:"id"`
			OwnedBy      string `json:"owned_by"`
			Context      int    `json:"context_length"`
			Architecture *struct {
				Input []string `json:"input_modalities"`
			} `json:"architecture"`
			Params []string `json:"supported_parameters"`
			Status *struct {
				Value string `json:"value"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := m.getJSON(ctx, s, "/v1/models", &list); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(list.Data))
	for _, d := range list.Data {
		md := Model{ID: d.ID, Context: d.Context, Caps: Caps{Tools: true}}
		if d.OwnedBy == "llama-swap" {
			// Local servers behind llama-swap: reasoning_content is shown when the model sends it.
			md.Caps.Thinking = true
			m.mu.Lock()
			m.swaps[s.URL] = true
			m.mu.Unlock()
		}
		if d.Status != nil {
			md.State = d.Status.Value
		}
		if md.Context == 0 {
			md.Context = m.upstreamContext(ctx, s, md)
		}
		if md.Context == 0 {
			md.Context = DefaultContext
		}
		if d.Architecture != nil {
			md.Caps.Known = true
			for _, in := range d.Architecture.Input {
				md.Caps.Vision = md.Caps.Vision || in == "image"
			}
		}
		if len(d.Params) > 0 {
			md.Caps.Tools = false
			for _, p := range d.Params {
				md.Caps.Tools = md.Caps.Tools || p == "tools"
				md.Caps.Thinking = md.Caps.Thinking || p == "reasoning"
			}
		}
		models = append(models, md)
	}
	return models, nil
}

// upstreamContext is the context of a model behind llama-swap that does not list it (it reads
// only llama-server's --ctx-size): the /props of the loaded server, kept for when it is
// unloaded, across restarts of the pod. An unloaded model is never asked, that would load
// it. 0 when unknown.
func (m *Manager) upstreamContext(ctx context.Context, s Server, md Model) int {
	key := s.URL + "\n" + md.ID
	if md.State == "loaded" || md.State == "ready" {
		var p llamaProps
		if m.getJSON(ctx, s, "/upstream/"+url.PathEscape(md.ID)+"/props", &p) == nil && p.Settings.NCtx > 0 {
			m.mu.Lock()
			if m.contexts[key] != p.Settings.NCtx {
				m.contexts[key] = p.Settings.NCtx
				m.st.WriteJSON(contextsFile, m.contexts)
			}
			m.mu.Unlock()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.contexts[key]
}

// ---------- Ollama ----------

func (m *Manager) ollamaModels(ctx context.Context, s Server) ([]Model, error) {
	var tags struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Details struct {
				ParameterSize string `json:"parameter_size"`
				Quantization  string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := m.getJSON(ctx, s, "/api/tags", &tags); err != nil {
		return nil, err
	}
	loaded := map[string]bool{}
	var ps struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if m.getJSON(ctx, s, "/api/ps", &ps) == nil {
		for _, p := range ps.Models {
			loaded[p.Name] = true
		}
	}
	models := make([]Model, len(tags.Models))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, t := range tags.Models {
		details := strings.TrimSpace(t.Details.ParameterSize + " " + t.Details.Quantization)
		md := Model{ID: t.Name, Size: t.Size, Details: details, State: "unloaded"}
		if loaded[t.Name] {
			md.State = "loaded"
		}
		wg.Add(1)
		go func(i int, md Model) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var show struct {
				Capabilities []string       `json:"capabilities"`
				ModelInfo    map[string]any `json:"model_info"`
			}
			if m.postJSON(ctx, s, "/api/show", map[string]string{"model": md.ID}, &show) == nil {
				md.Caps.Known = true
				for _, c := range show.Capabilities {
					switch c {
					case "vision":
						md.Caps.Vision = true
					case "tools":
						md.Caps.Tools = true
					case "thinking":
						md.Caps.Thinking = true
					case "audio":
						md.Caps.Audio = true
					}
				}
				for k, v := range show.ModelInfo {
					if strings.HasSuffix(k, ".context_length") {
						if f, ok := v.(float64); ok {
							md.Context = int(f)
						}
					}
				}
			}
			models[i] = md
		}(i, md)
	}
	wg.Wait()
	return models, nil
}
