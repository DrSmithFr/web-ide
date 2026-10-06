package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/capture"
	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// board_draw: the model draws a new page on the board of the conversation. Building a page
// needs the browser (text measured on a canvas, the description and the PNG of the doodle
// code), so a window of the project draws it (web/src/llm/board/build.ts) and the pod keeps
// it on the tool message. Pages never change: a fix is a new page, possibly a copy.

func (s *Server) boardDraw(r *agentRun, rt *runtime.Runtime, a toolArgs) (toolResult, error) {
	var elements []map[string]json.RawMessage
	if !a.has("elements") {
		elements = []map[string]json.RawMessage{}
	} else if err := json.Unmarshal(a["elements"], &elements); err != nil {
		return toolResult{}, fmt.Errorf("elements must be a list of elements")
	}
	if len(elements) > agent.MaxPageElements {
		return toolResult{}, fmt.Errorf("%d elements: %d at most", len(elements), agent.MaxPageElements)
	}
	for i, el := range elements {
		var pts []json.RawMessage
		if json.Unmarshal(el["points"], &pts) == nil && len(pts) > agent.MaxStrokePoints {
			return toolResult{}, fmt.Errorf("element %d: %d points, %d at most", i+1, len(pts), agent.MaxStrokePoints)
		}
	}
	if a.has("from") && (a.has("background") || a.has("svg")) {
		return toolResult{}, fmt.Errorf("background and from cannot go together: a copy keeps the background of its page")
	}
	r.mu.Lock()
	pages := agent.Pages(r.chat.Messages)
	r.mu.Unlock()
	args := map[string]any{"title": a.str("title"), "preset": a.str("preset"), "size": a["size"], "elements": elements, "number": len(pages) + 1}
	if a.has("from") {
		n := a.num("from")
		if n < 1 || n > len(pages) {
			if len(pages) == 0 {
				return toolResult{}, fmt.Errorf("page %d does not exist: the board has no page yet", n)
			}
			return toolResult{}, fmt.Errorf("page %d does not exist: the board has pages 1 to %d", n, len(pages))
		}
		// The document goes with the call: any window of the project can draw the copy.
		args["from"], args["fromDoc"] = n, pages[n-1].Doc
	}
	// svg at the first level: the simplest way to send an SVG (models forget a nested field).
	if a.has("svg") {
		if a.has("background") {
			return toolResult{}, fmt.Errorf("svg and background cannot go together: svg is the background")
		}
		a["background"], _ = json.Marshal(map[string]json.RawMessage{"svg": a["svg"]})
	}
	if a.has("background") {
		bg, err := s.background(r, rt, a["background"], pages)
		if err != nil {
			return toolResult{}, err
		}
		args["background"] = bg
	}
	res, window, answered := s.uiCall(r, "board_draw", args, true, 30*time.Second)
	switch {
	case !window:
		return toolResult{}, fmt.Errorf("drawing needs an IDE window open on the project: describe it in text instead")
	case !answered:
		return toolResult{}, fmt.Errorf("no IDE window drew the page in time: describe it in text instead")
	case res.Status == "error" || res.Page == nil:
		return toolResult{}, fmt.Errorf("%s", strings.TrimPrefix(res.Content, "Error: "))
	}
	return toolResult{Content: res.Content, Summary: res.Summary, Status: "ok", Page: res.Page}, nil
}

// Background of a page: an image the window puts under the elements ({src, origin}), or the
// SVG written by the model ({svg}). A capture of the screen of the user ({ide}) is not here:
// it needs their click (agent_loop.go, agent.capture).
type bgArgs struct {
	File       string `json:"file"`
	Attachment string `json:"attachment"`
	URL        string `json:"url"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	SVG        string `json:"svg"`
	IDE        bool   `json:"ide"`
}

const maxBackground = 10 << 20

var (
	unsafeSVG = regexp.MustCompile(`(?i)<script|<foreignobject|\bon[a-z]+\s*=|javascript:|(href|src)\s*=\s*["'](https?:|//)`)
	imageExt  = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif", ".svg": "image/svg+xml"}
)

func (s *Server) background(r *agentRun, rt *runtime.Runtime, raw json.RawMessage, pages []agent.PageRef) (map[string]any, error) {
	var b bgArgs
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("background must be an object (file, attachment, url, svg or ide)")
	}
	n := 0
	for _, set := range []bool{b.File != "", b.Attachment != "", b.URL != "", b.SVG != "", b.IDE} {
		if set {
			n++
		}
	}
	if n != 1 {
		return nil, fmt.Errorf("background needs exactly one source: file, attachment, url, svg or ide")
	}
	dataURL := func(mime string, data []byte) string {
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	switch {
	case b.File != "":
		abs := absPath(rt.Root, b.File)
		if !fsx.Within(rt.Root, abs) {
			return nil, fmt.Errorf("%s is outside the project", b.File)
		}
		mime := imageExt[strings.ToLower(filepath.Ext(abs))]
		if mime == "" {
			return nil, fmt.Errorf("%s is not an image (png, jpg, webp, gif or svg)", b.File)
		}
		st, err := rt.FS.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", b.File, err)
		}
		if st.Size > maxBackground {
			return nil, fmt.Errorf("%s is too large (%d MB, 10 MB at most)", b.File, st.Size>>20)
		}
		data, err := rt.FS.Read(abs)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", b.File, err)
		}
		if mime == "image/svg+xml" {
			return checkSVG(string(data), "file "+b.File)
		}
		return map[string]any{"src": dataURL(mime, data), "origin": "file " + relPath(rt.Root, abs)}, nil
	case b.Attachment != "":
		r.mu.Lock()
		src, names := attachmentImage(r.chat.Messages, b.Attachment)
		r.mu.Unlock()
		if src == "" {
			for _, p := range pages {
				if p.Name == b.Attachment && p.PNG != "" {
					src = p.PNG
				}
			}
		}
		if src == "" {
			return nil, fmt.Errorf("no image or page named %q in this conversation (%s)", b.Attachment, strings.Join(names, ", "))
		}
		return map[string]any{"src": src, "origin": "attachment " + b.Attachment}, nil
	case b.URL != "":
		data, err := capture.URL(r.ctx, b.URL, b.Width, b.Height)
		if err != nil {
			return nil, err
		}
		return map[string]any{"src": dataURL(http.DetectContentType(data), data), "origin": "capture of " + b.URL}, nil
	case b.SVG != "":
		return checkSVG(b.SVG, "SVG written by you")
	}
	return nil, fmt.Errorf("the capture of the screen waits for the user")
}

// checkSVG keeps an SVG drawn as an image: no script, no handler, no external reference.
func checkSVG(svg, origin string) (map[string]any, error) {
	if len(svg) > 1<<20 {
		return nil, fmt.Errorf("the SVG is too large (1 MB at most)")
	}
	if !strings.Contains(svg, "<svg") {
		return nil, fmt.Errorf("the SVG has no <svg> element")
	}
	if m := unsafeSVG.FindString(svg); m != "" {
		return nil, fmt.Errorf("the SVG may not contain %q (no script, event handler, foreignObject or external reference)", m)
	}
	return map[string]any{"svg": svg, "origin": origin}, nil
}

// attachmentImage finds an image sent by the user by its name, most recent first: a doodle
// (its PNG) or an image (its part in the message: the image parts not of a doodle, in the
// order of the image attachments). Also the names it could have been.
func attachmentImage(msgs []*agent.Message, name string) (string, []string) {
	var names []string
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		var atts []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			PNG  string `json:"png"`
		}
		if m.Role != "user" || json.Unmarshal(m.Attachments, &atts) != nil {
			continue
		}
		var parts []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		_ = json.Unmarshal(m.Content, &parts)
		doodles := map[string]bool{}
		mixed := false
		for _, a := range atts {
			doodles[a.PNG] = a.Kind == "doodle"
			mixed = mixed || a.Kind == "video" || a.Kind == "pdf"
		}
		var images []string
		for _, p := range parts {
			if p.Type == "image_url" && !doodles[p.ImageURL.URL] {
				images = append(images, p.ImageURL.URL)
			}
		}
		k := 0
		for _, a := range atts {
			if a.Kind == "doodle" || a.Kind == "image" {
				names = append(names, a.Name)
			}
			if a.Kind == "image" {
				if a.Name == name && !mixed && k < len(images) {
					return images[k], names
				}
				k++
			}
			if a.Kind == "doodle" && a.Name == name && a.PNG != "" {
				return a.PNG, names
			}
		}
	}
	return "", names
}
