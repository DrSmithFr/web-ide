# Doodle

A drawing joined to a message of the assistant: a sketch, a layout or an annotated screenshot that the user shows the model instead of describing it.

## Entry points

- The composer bar starts with the mode button (Build / Plan / Briefing), then the paperclip.
- The paperclip opens a menu: **File…** (any file, as today), **Doodle…**, **Screenshot…**.
- Ctrl+Shift+D opens a new doodle (when the browser lets the page take it).
- *Screenshot…* asks the browser what to share (`getDisplayMedia`, one frame), then opens a new doodle with the capture as background image; the tool bar of the modal takes one as well.

## The modal

A modal over the IDE: the canvas on the left, the conversation (thread and composer) on the right, so the user draws while reading and writing.

- **Attach** adds the doodle to the draft of the message and closes the modal. Sending from the composer of the modal attaches the doodle first. A message may hold several doodles mixed with files and text.
- Closing without attaching discards the doodle, after a confirmation when it is not empty.
- The chip of a doodle in the draft shows a thumbnail; a click opens it again to retouch it before sending.
- In the thread, a sent doodle shows as a preview (click to enlarge in the diagram viewer, which copies the description) with the description sent folded below, and a **Reuse the doodle** button that opens an editable copy, joined as a new doodle (the sent one never changes).

## Canvas

- Infinite: Ctrl+wheel zooms, Space+drag or middle button pans, two-finger pinch and pan on touch screens.
- The **frame** is the area sent to the model. Presets set its ratio: 16:9, mobile, square, free, image (the size of the background image). It is moved by its label and resized by its corners (keeping the ratio of a 16:9, mobile or square preset; free over an image, to crop it); nothing outside it is sent.
- The canvas follows the theme of the IDE. Colors are semantic (ink, red, blue, green…): drawn for the current theme, always exported in their light version on a white background, so the model sees the same picture in both themes.
- A magnetic grid (G) for the frame, shapes and layout dividers, not for free strokes.
- The tool, sizes, colors and options are remembered by the browser.
- A background image (upload, paste with Ctrl+V, or screenshot) lies under everything, at full opacity, reduced to 2048 px at most. It takes the corner of the frame, and the frame its size (preset *image*); the tool bar removes it. Ctrl+V pastes an image as the background, else the copied elements.

## Tools

| Key | Tool | Notes |
|---|---|---|
| V | Select | click, Shift+click, rubber band, Ctrl+A; move, resize by the corners (Shift keeps the ratio), Delete, Ctrl+D duplicates, arrows nudge (Shift: 10), copy / cut / paste between doodles; a color or a text size applies to the selection; double click edits a text |
| P | Pen | thin or normal, 4 colors; width follows the stylus pressure (option to turn it off), fixed with a mouse |
| M | Marker | wide, semi-transparent, 4 fluo colors, drawn under the pen strokes |
| E | Eraser | pixel by default (cuts strokes; shapes and texts go whole), toggle to object (removes a whole element); the stylus eraser end switches to it by itself |
| R / O | Rectangle / ellipse | outline in the current color and pen width, optionally filled with a light tint of it; Shift draws a square / circle |
| L / A | Line / arrow | Shift by steps of 45°; an end dropped on a shape, a text or a layout is tied to it (the target is outlined) and follows it when it moves or is resized; selected alone, its ends are dragged by their handles (filled when tied); moved without its targets, it comes loose |
| T | Text | 3 sizes (S, M, L), current color; typed in place, Escape or Ctrl+Enter ends it, a click on a text edits it |
| K | Layout | see below |

Ctrl+Z / Ctrl+Shift+Z undo and redo every change. Escape closes the help or the zone menu, clears the selection, then closes the modal. `?` (key or button of the head) shows every key. Everything stays editable until the doodle is attached.

## Layouts

A layout is an element like the others (several per doodle, movable and resizable with the select tool, drawn under the rest): a rectangle split recursively.

- The layout tool (K) draws its box; a click on a zone opens its menu: split it into 2 to 4 columns or rows, a grid (2×2, 3×3) or a border layout (north, south, west, east and center, named), merge its parts, name it, delete it (the previous part, else the next, takes its place; a zone left with one part takes its content), or delete the layout (on its outer zone). One line per group of options; Escape closes the menu.
- Dividers are dragged with the layout or the select tool (within the two parts they separate, 5 % at least); they snap to the grid when it is on.
- A double-click names a zone (`sidebar`, `header`…); the name is drawn at its center.

The layout is a tree, which gives the model an exact description of the structure.

## What the model receives

For each doodle, in order:

1. A PNG of the frame (light version), when the model reads images.
2. A text description built from the elements: frame size and ratio, layouts as trees with proportions and names, shapes and texts numbered with their position in the frame (as percentages), the texts inside a shape as its label, the shape or the zone of a layout each element is inside (a zone by its name, else by its place: `column 2 › row 1`), each zone with what it holds, arrows and lines with the elements their ends are tied to (else touch), free strokes with their color and place only.

A model without image input gets the description alone.

The tools text of the three modes (part of the prompt even when the template is edited) and the end of each description ask the model to rely on the description for positions, proportions, labels and structure, and on the image for the rest; a ticket or a plan written from a doodle carries its structure as a Mermaid diagram, not the image.

## Tickets

When the model creates a ticket (`kanban_create`) or updates its ticket (`kanban_update`), the doodles of the conversation not attached yet are joined to it as PNG files (`Doodle 1.png`, `Doodle 1 (2).png` for a second Doodle 1), and so are the pages drawn by the model (`Page 3 Login flow.png`); the tool result tells the model.

## Whiteboard

Each conversation has a board: its pages are the doodles sent in it (later also the drawings of the assistant), read-only. The *Board* button of the assistant head (with the number of pages) shows it: a column next to the conversation when the assistant is at least 1000 px wide (a splitter sets its share), else in place of the conversation. One page is shown large, with zoom (wheel, pinch), pan (drag) and *Fit* (or a double click); the others are a strip of thumbnails (← / → when the board has the focus). *Reuse* opens an editable copy in the modal, sent as a new doodle: a page never changes. *Show on the board* on a doodle of the thread selects its page.

The assistant draws pages too, with the `board_draw` tool (in every mode): a frame (16:9, mobile, square or a free size) or a copy of an earlier page (`from`), then elements in pixels from the top left corner of the frame: rectangles and ellipses (with a centred label), lines and arrows tied to elements by the ids the model gives, texts, layouts (a zone tree) and pen or marker strokes. The agent runs in the pod but a page needs the browser (text measures, description, PNG), so a window of the project draws it (`board/build.ts`), and without any window the tool answers that the model should describe it in text. The model gets the description of the page (`Page N "title" drawn by you`), and, if it reads images, its PNG in a user message right after the tool results (servers often take only text in a tool result). A new page selects itself, opens the board in a wide assistant or shows a toast in a narrow one, and has a card in the thread.

Pages are numbered from 1 in message order, then in attachment order within a message; the model refers to them by this number (the pod counts them the same way, `agent.Pages`). The board is a view over the messages of the conversation (`web/src/llm/board/pages.ts`), stored nowhere else.

## Storage

The doodle lives in the conversation, in the message that sent it: the PNG, the description and the vector document (JSON, version field, the background as a data URL) used by *Reuse the doodle*. No file in the project.

## Code

Hand-made, no drawing library: SVG for the elements, a canvas only for the export. `web/src/llm/doodle/`: the document model and its undo stack, the tools, the layout tree, the export (PNG and description), the modal, the drawing of the elements shared with the board (`Elements.tsx`). `web/src/llm/board/`: the pages of the conversation, the read-only view and the board panel.
