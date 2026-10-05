# Doodle

A drawing joined to a message of the assistant: a sketch, a layout or an annotated screenshot that the user shows the model instead of describing it.

## Entry points

- The composer bar starts with the mode button (Build / Plan / Briefing), then the paperclip.
- The paperclip opens a menu: **File…** (any file, as today), **Doodle…**, **Screenshot…**.
- Ctrl+Shift+D opens a new doodle (when the browser lets the page take it).
- *Screenshot…* asks the browser what to share (`getDisplayMedia`, one frame), then opens a new doodle with the capture as background image.

## The modal

A modal over the IDE: the canvas on the left, the conversation (thread and composer) on the right, so the user draws while reading and writing.

- **Attach** adds the doodle to the draft of the message and closes the modal. Sending from the composer of the modal attaches the doodle first. A message may hold several doodles mixed with files and text.
- Closing without attaching discards the doodle, after a confirmation when it is not empty.
- The chip of a doodle in the draft shows a thumbnail; a click opens it again to retouch it before sending.
- In the thread, a sent doodle shows as an image (click to enlarge) with its text description folded below, and a **Reuse the doodle** button that opens an editable clone (the sent one never changes).

## Canvas

- Infinite: Ctrl+wheel zooms, Space+drag or middle button pans, two-finger pinch and pan on touch screens.
- The **frame** is the area sent to the model. Presets set its ratio: 16:9, mobile, square, image (the size of the background image). It can be moved and resized like a shape; nothing outside it is sent.
- The canvas follows the theme of the IDE. Colors are semantic (ink, red, blue, green…): drawn for the current theme, always exported in their light version on a white background, so the model sees the same picture in both themes.
- A magnetic grid (G) for the frame, shapes and layout dividers, not for free strokes.
- The tool, sizes, colors and options are remembered by the browser.
- A background image (upload, paste with Ctrl+V, or screenshot) lies under everything, at full opacity.

## Tools

| Key | Tool | Notes |
|---|---|---|
| V | Select | click, Shift+click, rubber band; move, resize, Delete, Ctrl+D duplicates, arrows nudge, copy / paste between doodles |
| P | Pen | thin or normal, 4 colors; width follows the stylus pressure (option to turn it off), fixed with a mouse |
| M | Marker | wide, semi-transparent, 4 fluo colors, drawn under the pen strokes |
| E | Eraser | pixel by default (cuts strokes), toggle to object (removes a whole element); the stylus eraser end switches to it by itself |
| R / O | Rectangle / ellipse | outline in the current color |
| L / A | Line / arrow | |
| T | Text | 3 sizes (S, M, L), current color |
| — | Layout | see below |

Ctrl+Z / Ctrl+Shift+Z undo and redo every change. Everything stays editable until the doodle is attached.

## Layouts

A layout is an element like the others (several per doodle, movable, drawn over): a rectangle split recursively.

- Split a zone horizontally or vertically into 2 to N parts (columns, rows, grid), or as a border layout (north, south, west, east, center).
- Dividers can be dragged; they snap to the grid when it is on.
- A double-click names a zone (`sidebar`, `header`…).

The layout is a tree, which gives the model an exact description of the structure.

## What the model receives

For each doodle, in order:

1. A PNG of the frame (light version), when the model reads images.
2. A text description built from the elements: frame size and ratio, layouts as trees with proportions and names, shapes and texts with their position in the frame (as percentages), arrows with what they link when they touch an element, free strokes only counted and located ("3 strokes in the top right corner").

A model without image input gets the description alone.

The prompts of the three modes ask the model to rely on the description for proportions and on the image for the rest; in Briefing mode, a ticket written from a doodle carries its structure as a Mermaid diagram.

## Storage

The doodle lives in the conversation, in the message that sent it: the PNG, the description and the vector document (JSON, version field, the background as a data URL) used by *Reuse the doodle*. No file in the project.

## Code

Hand-made, no drawing library: SVG for the elements, a canvas only for the export. `web/src/llm/doodle/`: the document model and its undo stack, the tools, the layout tree, the export (PNG and description), the modal.
