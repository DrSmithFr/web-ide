package ai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// The model draws new pages on the board of the conversation: board_draw_doodle (a frame or
// a clone of a page, with elements) and board_draw_image (an SVG it writes, or a capture of
// the screen of the user: assistant_loop.go, assistant.capture). Building a page needs the browser
// (text measured on a canvas, the description and the PNG of the doodle code), so a window
// of the project draws it (web/src/llm/board/build.ts) and the pod keeps it on the tool
// message. Pages never change: a fix is a new page, possibly a copy.

func (s *A) boardDoodle(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	var elements []map[string]json.RawMessage
	if err := json.Unmarshal(a["elements"], &elements); err != nil {
		return assistant.ToolResult{}, fmt.Errorf("elements must be a list of elements")
	}
	if len(elements) > assistant.MaxPageElements {
		return assistant.ToolResult{}, fmt.Errorf("%d elements: %d at most", len(elements), assistant.MaxPageElements)
	}
	for i, el := range elements {
		var pts []json.RawMessage
		if json.Unmarshal(el["points"], &pts) == nil && len(pts) > assistant.MaxStrokePoints {
			return assistant.ToolResult{}, fmt.Errorf("element %d: %d points, %d at most", i+1, len(pts), assistant.MaxStrokePoints)
		}
	}
	if a.Has("clone") && a.Str("size") != "" {
		return assistant.ToolResult{}, fmt.Errorf("size and clone cannot go together: a clone keeps the size of its page")
	}
	r.mu.Lock()
	pages := assistant.Pages(r.chat.Messages)
	r.mu.Unlock()
	args := map[string]any{"title": a.Str("title"), "size": a.Str("size"), "elements": elements, "number": len(pages) + 1}
	if a.Has("clone") {
		n := a.Num("clone")
		if n < 1 || n > len(pages) {
			if len(pages) == 0 {
				return assistant.ToolResult{}, fmt.Errorf("page %d does not exist: the board has no page yet", n)
			}
			return assistant.ToolResult{}, fmt.Errorf("page %d does not exist: the board has pages 1 to %d", n, len(pages))
		}
		if pages[n-1].Doc == nil {
			return assistant.ToolResult{}, fmt.Errorf("page %d cannot be copied: its image is not kept in the conversation", n)
		}
		// The document goes with the call: any window of the project can draw the copy.
		args["clone"], args["cloneDoc"] = n, pages[n-1].Doc
	}
	return s.drawPage(r, args)
}

var unsafeSVG = regexp.MustCompile(`(?i)<script|<foreignobject|\bon[a-z]+\s*=|javascript:|(href|src)\s*=\s*["'](https?:|//)`)

// boardImage puts an image as a page: an SVG written by the model or an image file of the
// project ("screen" stops the turn before: the capture needs a click of the user).
func (s *A) boardImage(r *assistantRun, rt *runtime.Runtime, a assistant.Args) (assistant.ToolResult, error) {
	image := strings.TrimSpace(a.Str("image"))
	if !strings.HasPrefix(image, "<") {
		return s.boardFile(r, rt, a.Str("title"), image)
	}
	svg := image
	if !strings.HasPrefix(svg, "<svg") && !strings.HasPrefix(svg, "<?xml") {
		return assistant.ToolResult{}, fmt.Errorf(`image is an SVG ("<svg …>…</svg>"), the path of an image of the project, or "screen" (a capture of the screen of the user)`)
	}
	if len(svg) > 1<<20 {
		return assistant.ToolResult{}, fmt.Errorf("the SVG is too large (1 MB at most)")
	}
	if m := unsafeSVG.FindString(svg); m != "" {
		return assistant.ToolResult{}, fmt.Errorf("the SVG may not contain %q (no script, event handler, foreignObject or external reference)", m)
	}
	r.mu.Lock()
	n := len(assistant.Pages(r.chat.Messages)) + 1
	r.mu.Unlock()
	return s.drawPage(r, map[string]any{"title": a.Str("title"), "svg": svg, "number": n})
}

// boardFile puts an image file of the project as a page (an SVG file is checked like an SVG
// of the model).
func (s *A) boardFile(r *assistantRun, rt *runtime.Runtime, title, path string) (assistant.ToolResult, error) {
	abs := assistant.AbsPath(rt.Root, path)
	mime := assistant.ImageTypes[strings.ToLower(filepath.Ext(abs))]
	switch {
	case !fsx.Within(rt.Root, abs):
		return assistant.ToolResult{}, fmt.Errorf("%s is outside the project", path)
	case mime == "":
		return assistant.ToolResult{}, fmt.Errorf(`image is an SVG ("<svg …>…</svg>"), the path of an image of the project (png, jpg, webp, gif, svg), or "screen": %q is none of them`, path)
	}
	st, err := rt.FS.Stat(abs)
	if err != nil {
		return assistant.ToolResult{}, fmt.Errorf("%s: %v", path, err)
	}
	if st.Size > assistant.MaxImage {
		return assistant.ToolResult{}, fmt.Errorf("%s is too large (%d MB, 10 MB at most)", path, st.Size>>20)
	}
	data, err := rt.FS.Read(abs)
	if err != nil {
		return assistant.ToolResult{}, fmt.Errorf("%s: %v", path, err)
	}
	if mime == "image/svg+xml" {
		if m := unsafeSVG.FindString(string(data)); m != "" {
			return assistant.ToolResult{}, fmt.Errorf("%s may not contain %q (no script, event handler, foreignObject or external reference)", path, m)
		}
	}
	r.mu.Lock()
	n := len(assistant.Pages(r.chat.Messages)) + 1
	r.mu.Unlock()
	src := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	return s.drawPage(r, map[string]any{"title": title, "src": src, "origin": "the image " + assistant.RelPath(rt.Root, abs), "number": n})
}

// drawPage asks a window to draw a page.
func (s *A) drawPage(r *assistantRun, args map[string]any) (assistant.ToolResult, error) {
	res, window, answered := s.uiCall(r, "board_draw", args, true, 30*time.Second)
	switch {
	case !window:
		return assistant.ToolResult{}, fmt.Errorf("drawing needs an IDE window open on the project: describe it in text instead")
	case !answered:
		return assistant.ToolResult{}, fmt.Errorf("no IDE window drew the page in time: describe it in text instead")
	case res.Status == "error" || res.Page == nil:
		return assistant.ToolResult{}, fmt.Errorf("%s", strings.TrimPrefix(res.Content, "Error: "))
	}
	return assistant.ToolResult{Content: res.Content, Summary: res.Summary, Status: "ok", Page: res.Page}, nil
}
