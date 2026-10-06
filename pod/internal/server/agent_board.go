package server

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
)

// The model draws new pages on the board of the conversation: board_draw_doodle (a frame or
// a copy of a page, with elements) and board_draw_image (an SVG it writes, or a capture of
// the screen of the user: agent_loop.go, agent.capture). Building a page needs the browser
// (text measured on a canvas, the description and the PNG of the doodle code), so a window
// of the project draws it (web/src/llm/board/build.ts) and the pod keeps it on the tool
// message. Pages never change: a fix is a new page, possibly a copy.

func (s *Server) boardDoodle(r *agentRun, a toolArgs) (toolResult, error) {
	var elements []map[string]json.RawMessage
	if err := json.Unmarshal(a["elements"], &elements); err != nil {
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
	if a.has("from") && a.str("size") != "" {
		return toolResult{}, fmt.Errorf("size and from cannot go together: a copy keeps the size of its page")
	}
	r.mu.Lock()
	pages := agent.Pages(r.chat.Messages)
	r.mu.Unlock()
	args := map[string]any{"title": a.str("title"), "size": a.str("size"), "elements": elements, "number": len(pages) + 1}
	if a.has("from") {
		n := a.num("from")
		if n < 1 || n > len(pages) {
			if len(pages) == 0 {
				return toolResult{}, fmt.Errorf("page %d does not exist: the board has no page yet", n)
			}
			return toolResult{}, fmt.Errorf("page %d does not exist: the board has pages 1 to %d", n, len(pages))
		}
		if pages[n-1].Doc == nil {
			return toolResult{}, fmt.Errorf("page %d cannot be copied: its image is not kept in the conversation", n)
		}
		// The document goes with the call: any window of the project can draw the copy.
		args["from"], args["fromDoc"] = n, pages[n-1].Doc
	}
	return s.drawPage(r, args)
}

var unsafeSVG = regexp.MustCompile(`(?i)<script|<foreignobject|\bon[a-z]+\s*=|javascript:|(href|src)\s*=\s*["'](https?:|//)`)

// boardImage draws an SVG written by the model as a page ("screen" stops the turn before:
// the capture needs a click of the user).
func (s *Server) boardImage(r *agentRun, a toolArgs) (toolResult, error) {
	svg := strings.TrimSpace(a.str("image"))
	if !strings.HasPrefix(svg, "<svg") && !strings.HasPrefix(svg, "<?xml") {
		return toolResult{}, fmt.Errorf(`image is an SVG ("<svg …>…</svg>") or "screen" (a capture of the screen of the user)`)
	}
	if len(svg) > 1<<20 {
		return toolResult{}, fmt.Errorf("the SVG is too large (1 MB at most)")
	}
	if m := unsafeSVG.FindString(svg); m != "" {
		return toolResult{}, fmt.Errorf("the SVG may not contain %q (no script, event handler, foreignObject or external reference)", m)
	}
	r.mu.Lock()
	n := len(agent.Pages(r.chat.Messages)) + 1
	r.mu.Unlock()
	return s.drawPage(r, map[string]any{"title": a.str("title"), "svg": svg, "number": n})
}

// drawPage asks a window to draw a page.
func (s *Server) drawPage(r *agentRun, args map[string]any) (toolResult, error) {
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
