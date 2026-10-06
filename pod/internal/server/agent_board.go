package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
)

// board_draw: the model draws a new page on the board of the conversation. Building a page
// needs the browser (text measured on a canvas, the description and the PNG of the doodle
// code), so a window of the project draws it (web/src/llm/board/build.ts) and the pod keeps
// it on the tool message. Pages never change: a fix is a new page, possibly a copy.

func (s *Server) boardDraw(r *agentRun, a toolArgs) (toolResult, error) {
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
